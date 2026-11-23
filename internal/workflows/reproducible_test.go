package workflows

// A rebuild of one commit is the same bytes, and `glyph version`'s date is
// the commit's in every build that states one. Both rest on lines no Go code
// compiles: template fields in .goreleaser.yaml and one assignment in
// build.sh. The release half is pinned as text, because running GoReleaser
// is not something a unit test can do; what the pinned keys buy was measured
// with the tool itself (the doc comment below). The source half is executed.

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// wallClockTemplate matches a GoReleaser template that reads the time of the
// run: .Date, .Timestamp and .Now (goreleaser.com/customization/templates).
// .CommitDate and .CommitTimestamp carry no dot before Date or Timestamp.
var wallClockTemplate = regexp.MustCompile(`\{\{[^}]*\.(Date|Timestamp|Now)\b`)

var (
	commitDateTemplate      = `"\{\{\s*\.CommitDate\s*\}\}"`
	commitTimestampTemplate = `"\{\{\s*\.CommitTimestamp\s*\}\}"`
)

// topLevelBlock returns the lines under the top-level key of a YAML text, up
// to the next top-level key.
func topLevelBlock(t *testing.T, body, key string) string {
	t.Helper()
	_, rest, found := strings.Cut("\n"+body, "\n"+key+":\n")
	if !found {
		t.Fatalf(".goreleaser.yaml has no top-level %q block", key)
	}
	if end := regexp.MustCompile(`(?m)^\S`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	return rest
}

// under returns the lines nested below the first line that is exactly head —
// its indentation included — or "" when no such line exists.
func under(block, head string) string {
	_, rest, found := strings.Cut("\n"+block, "\n"+head+"\n")
	if !found {
		return ""
	}
	indent := len(head) - len(strings.TrimLeft(head, " "))
	var nested []string
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSpace(line) != "" && len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		nested = append(nested, line)
	}
	return strings.Join(nested, "\n")
}

// TestReleaseArtifactsCarryNoTimeButTheCommits: every time, every account and
// every file mode a release artifact carries is stated in .goreleaser.yaml,
// so two runs over one commit produce the same bytes — which is what lets a
// re-run of a published tag's build be compared with the published sha256,
// and an attestation be re-issued over the bytes it first named.
//
// Measured 2026-11-15 with GoReleaser v2.20.2 (`release --clean
// --skip=publish`, two fresh checkouts of one commit built 75s apart). At
// c06e426 all ten artifacts differed — four binaries, four archives,
// checksums.txt, the cask — and each key below is one of the differences:
// the binary answered `version --json` with the run's clock ({{.Date}}); the
// archives' glyph member carried the second the compiler wrote it, LICENSE
// and README.md the second the checkout wrote them; every member carried the
// building account's id and name. With the keys, the two runs agree on all
// ten sha256, and the members read uid 0 `root` at the committer time.
//
// Measured 2026-11-23, same tool: a clone and build under umask 077 packed
// LICENSE and README.md `-rw-------` where umask 022 packs `-rw-r--r--`, and
// all four archives, checksums.txt and the cask differed — GoReleaser copies
// a file's mode from the checkout unless `info.mode` states it. builds_info
// needs none: GoReleaser forces 0755 on a binary.
func TestReleaseArtifactsCarryNoTimeButTheCommits(t *testing.T) {
	for _, canary := range []string{"{{.Date}}", "{{ .Date }}", "{{ .Timestamp }}", `{{ .Now.Format "2006" }}`} {
		if !wallClockTemplate.MatchString(canary) {
			t.Fatalf("the wall-clock pattern no longer matches %q; the absence check below would pass vacuously", canary)
		}
	}
	for _, commit := range []string{"{{.CommitDate}}", "{{ .CommitTimestamp }}"} {
		if wallClockTemplate.MatchString(commit) {
			t.Fatalf("the wall-clock pattern matches %q, the commit's own time", commit)
		}
	}

	config := code(repoFile(t, ".goreleaser.yaml"))
	if found := wallClockTemplate.FindAllString(config, -1); len(found) > 0 {
		t.Errorf(".goreleaser.yaml reads the time of the run (%q): whatever that template reaches differs in "+
			"every rebuild of the same commit. The commit's time is {{.CommitDate}} / {{.CommitTimestamp}}",
			strings.Join(found, ", "))
	}

	stamp := "-X " + modulePath(t) + "/internal/version.Date={{.CommitDate}}"
	if !strings.Contains(config, stamp) {
		t.Errorf(".goreleaser.yaml does not inject %q: `glyph version`'s date is the commit date in every build "+
			"that states one (build.sh, the toolchain's vcs.time), and any other stamp makes each rebuild a "+
			"different binary", stamp)
	}

	builds := topLevelBlock(t, config, "builds")
	entries := len(regexp.MustCompile(`(?m)^  - `).FindAllString(builds, -1))
	stamped := len(regexp.MustCompile(`(?m)^    mod_timestamp:\s*`+commitTimestampTemplate+`\s*$`).FindAllString(builds, -1))
	if entries == 0 || stamped != entries {
		t.Errorf(".goreleaser.yaml has %d build(s) and %d `mod_timestamp: \"{{ .CommitTimestamp }}\"`: without it a "+
			"binary's file time is the second the compiler wrote it, and the archive copies that into the "+
			"member's header (builds_info states no mtime of its own)", entries, stamped)
	}

	archives := topLevelBlock(t, config, "archives")
	committed := func(what, info string) {
		t.Helper()
		for _, key := range []string{"owner", "group"} {
			if !regexp.MustCompile(`(?m)^\s+` + key + `:\s*root\s*$`).MatchString(info) {
				t.Errorf("the archive's %s states no `%s: root`: GoReleaser then copies the id and the name of the "+
					"account that ran the job into the tar header, so the archive's bytes depend on the host", what, key)
			}
		}
	}
	if info := under(archives, "    builds_info:"); info == "" {
		t.Error("the archive has no `builds_info:` block, so the binary's tar header carries the building account")
	} else {
		committed("builds_info", info)
	}
	files := under(archives, "    files:")
	if files == "" {
		t.Fatal("the archive has no `files:` list: GoReleaser then packs LICENSE and README.md by its default " +
			"globs with the times the checkout wrote them, which differ in every run")
	}
	items := regexp.MustCompile(`(?m)^      - `).Split(files, -1)[1:]
	if len(items) == 0 {
		t.Fatalf("the archive's `files:` holds no entry this guard can read:\n%s", files)
	}
	for _, item := range items {
		name, _, _ := strings.Cut(item, "\n")
		if !strings.HasPrefix(name, "src: ") {
			t.Errorf("the archive's files entry %q is not the `src:` form, the only one that takes an `info:`; "+
				"packed bare, the file carries the time the checkout wrote it", name)
			continue
		}
		committed("files entry "+name, item)
		if !regexp.MustCompile(`(?m)^\s+mtime:\s*` + commitDateTemplate + `\s*$`).MatchString(item) {
			t.Errorf("the archive's files entry %q states no `mtime: \"{{ .CommitDate }}\"`, so its tar header "+
				"carries the time the checkout wrote the file", name)
		}
		if !regexp.MustCompile(`(?m)^\s+mode:\s*0644\s*$`).MatchString(item) {
			t.Errorf("the archive's files entry %q states no `mode: 0644`, so its tar header carries the mode the "+
				"checkout's umask left: under umask 077 every archive, checksums.txt and the cask change", name)
		}
	}
	every := len(regexp.MustCompile(`(?m)^\s+mtime:`).FindAllString(archives, -1))
	fromCommit := len(regexp.MustCompile(`(?m)^\s+mtime:\s*`+commitDateTemplate+`\s*$`).FindAllString(archives, -1))
	if every != fromCommit {
		t.Errorf("%d of the archive's %d `mtime:` keys are not \"{{ .CommitDate }}\"", every-fromCommit, every)
	}
}

// TestReleaseArtifactsCarryNoBuildDirectory: every build passes -trimpath.
// Without it the compiler writes the checkout's absolute path into the
// binary, so two clones of one commit at different paths build different
// binaries — measured 2026-11-23 with GoReleaser v2.20.2: 78 build-directory
// strings in each, and `go test ./internal/workflows` still passed. build.sh
// passes it too, so a source build is not tied to where it was checked out.
func TestReleaseArtifactsCarryNoBuildDirectory(t *testing.T) {
	builds := topLevelBlock(t, code(repoFile(t, ".goreleaser.yaml")), "builds")
	entries := len(regexp.MustCompile(`(?m)^  - `).FindAllString(builds, -1))
	trimmed := len(regexp.MustCompile(`(?m)^    flags:\s*\[[^\]]*-trimpath[\s,\]]`).FindAllString(builds, -1))
	if entries == 0 || trimmed != entries {
		t.Errorf(".goreleaser.yaml has %d build(s) and %d `flags: [-trimpath]`: without it the compiler writes "+
			"the checkout's path into the binary, so a clone of the same commit at another path builds "+
			"different bytes", entries, trimmed)
	}
	if !strings.Contains(repoFile(t, "build.sh"), "go build -trimpath ") {
		t.Error("build.sh's `go build` passes no -trimpath: its binary then carries the checkout's path, so two " +
			"checkouts of one commit build different bytes")
	}
}

// versionFixture writes a module build.sh can build without the network:
// the real build.sh, the real internal/version under the real module path —
// so the -X injections land on the symbols they name — and a main that
// prints the resolved identity as JSON.
func versionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mod := modulePath(t)
	goLine := regexp.MustCompile(`(?m)^go \S+$`).FindString(repoFile(t, "go.mod"))
	if goLine == "" {
		t.Fatal("go.mod declares no go line")
	}
	testutil.WriteFile(t, dir, "build.sh", repoFile(t, "build.sh"))
	testutil.WriteFile(t, dir, "go.mod", "module "+mod+"\n\n"+goLine+"\n")
	testutil.WriteFile(t, dir, "internal/version/version.go", repoFile(t, filepath.Join("internal", "version", "version.go")))
	testutil.WriteFile(t, dir, "cmd/glyph/main.go", `package main

import (
	"encoding/json"
	"os"

	"`+mod+`/internal/version"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(version.Resolve()); err != nil {
		os.Exit(1)
	}
}
`)
	return dir
}

// TestSourceBuildIsDatedByItsCommit runs build.sh for real — real git, the
// real Go toolchain, the real internal/version — over a commit whose
// committer date is neither its author date nor in UTC, under a TZ that is
// neither. The binary must answer the committer date in RFC 3339 UTC, the
// form of GoReleaser's {{.CommitDate}}, and the oracle is the toolchain
// itself: a plain `go build` of the same commit answers from vcs.time, and
// the two dates must be one string. With `date -u` in its place, build.sh
// stamps the second it ran: every rebuild a new answer, and "date" meaning
// the commit in a release binary and the build in a source one.
//
// Outside a checkout there is no commit, and the binary states no date
// rather than the wall clock.
func TestSourceBuildIsDatedByItsCommit(t *testing.T) {
	testutil.RequireGit(t)
	sh, goTool, git := requireTool(t, "sh"), requireTool(t, "go"), requireTool(t, "git")
	const (
		authored  = "2026-01-02T03:04:05Z"
		committed = "2026-07-01T08:30:00+09:00"
		want      = "2026-06-30T23:30:00Z"
	)
	type identity struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	env := func(dir string) []string {
		return testutil.GitEnv("akira-toriyama",
			"TZ=America/New_York",
			"GIT_AUTHOR_DATE="+authored,
			"GIT_COMMITTER_DATE="+committed,
			// git stops looking for a repository at the fixture, so the
			// case with none cannot find one above the temporary directory.
			"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir),
			"GOFLAGS=", "GOWORK=off", "GOPROXY=off",
		)
	}
	run := func(dir string, argv ...string) string {
		t.Helper()
		r := execute(t, argv, dir, env(dir))
		if r.exit != 0 {
			t.Fatalf("%v exited %d, want 0\n%s", argv, r.exit, r)
		}
		return strings.TrimSpace(r.stdout)
	}
	answer := func(dir, binary string) identity {
		t.Helper()
		var id identity
		out := run(dir, binary)
		if err := json.Unmarshal([]byte(out), &id); err != nil {
			t.Fatalf("%s printed %q, not the identity: %v", binary, out, err)
		}
		return id
	}

	t.Run("in a checkout", func(t *testing.T) {
		dir := versionFixture(t)
		run(dir, git, "init", "-q", "-b", "main")
		run(dir, git, "add", ".")
		run(dir, git, "commit", "-q", "-m", ":tada:= begin the project")
		head := run(dir, git, "rev-parse", "HEAD")

		run(dir, sh, "build.sh")
		built := answer(dir, filepath.Join(dir, "bin", "glyph"))
		if built.Date != want {
			t.Errorf("build.sh's binary answers date %q, want %q — the committer date (%s) in UTC; the author "+
				"date is %s", built.Date, want, committed, authored)
		}
		if built.Commit == "" || !strings.HasPrefix(head, built.Commit) {
			t.Errorf("build.sh's binary answers commit %q, want a prefix of %s", built.Commit, head)
		}

		plain := filepath.Join(t.TempDir(), "glyph")
		run(dir, goTool, "build", "-o", plain, "./cmd/glyph")
		stamped := answer(dir, plain)
		if stamped.Commit != head || stamped.Date == "" {
			t.Fatalf("a plain `go build` in the checkout answers %+v, want commit %s and vcs.time — the "+
				"toolchain stamped no VCS data, so the oracle below has nothing to say", stamped, head)
		}
		if stamped.Date != built.Date {
			t.Errorf("build.sh dates the commit %q and the toolchain's vcs.time %q: `glyph version`'s date "+
				"has two meanings, or two forms, for one commit", built.Date, stamped.Date)
		}
	})

	t.Run("outside a checkout", func(t *testing.T) {
		dir := versionFixture(t)
		run(dir, sh, "build.sh")
		built := answer(dir, filepath.Join(dir, "bin", "glyph"))
		if want := (identity{Version: "dev", Commit: "unknown"}); built != want {
			t.Errorf("build.sh outside a checkout builds a binary answering %+v, want %+v: there is no commit, "+
				"so there is no date to state — the time of the build is not one", built, want)
		}
	})
}
