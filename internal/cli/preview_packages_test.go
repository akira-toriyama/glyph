package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// This file pins `glyph preview` for a packages repository (DESIGN §4.1,
// "Preview"): one verdict per line the pull's commits are attributed to, in
// config order, a line the pull does not touch never mentioned.

// previewVerdictLines decodes preview --json in packages mode.
type previewVerdictLines struct {
	Current  string `json:"current"`
	Level    string `json:"level"`
	Next     string `json:"next"`
	PR       string `json:"pr"`
	Pending  string `json:"pending"`
	Body     string `json:"body"`
	Packages []struct {
		Path     string `json:"path"`
		Current  string `json:"current"`
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
	} `json:"packages"`
}

func decodePreviewLines(t *testing.T, stdout string) previewVerdictLines {
	t.Helper()
	var res previewVerdictLines
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("preview --json stdout is not JSON: %v\n%s", err, stdout)
	}
	return res
}

// crossLinePull is the open-pull twin of squashAcrossLines: pull #9's
// listing carries a ^ under haiku/, a ~ under curry/ and a shared-only =,
// each commit's files served on its own commits/{sha} route (the commits
// exist on the pull's branch only, so the API is where preview reads them).
func crossLinePull(number int) map[string]string {
	return map[string]string{
		pullCommitsPath(number): `[` +
			apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `,` +
			apiCommit("s1", "akira-toriyama", ":memo:= document the lines") + `]`,
		commitFilesPath("h1"): apiFiles("haiku/season.go"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
		commitFilesPath("s1"): apiFiles("README.md"),
	}
}

// TestPreviewPackagesOneVerdictPerTouchedLine: the defining probe previewed
// — two headlines, haiku minor and curry patch, each with its own table; a
// third declared line the pull does not touch (fish) is not mentioned; the
// shared-only = commit sits in no line; the scalars are empty and packages
// carries the folded verdict per line (mutation rows
// preview-packages-untouched-line-is-mentioned and
// preview-packages-scalar-verdict-describes-one-line).
func TestPreviewPackagesOneVerdictPerTouchedLine(t *testing.T) {
	dir, base := packagesRepo(t)
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \"fish\"\n")
	writeFile(t, dir, "fish/fish.go", "package fish\n")
	testGit(t, dir, "akira-toriyama", "add", ".")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare a third line")
	declared := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "fish/v0.1.0")
	routes := crossLinePull(9)
	routes[commitPullsPath(declared)] = `[]`
	_ = base
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if res.Current != "" || res.Level != "" || res.Next != "" || res.PR != "none" || res.Pending != "none" {
		t.Fatalf("scalars must describe no line under packages: %s", stdout)
	}
	if len(res.Packages) != 2 || res.Packages[0].Path != "haiku" || res.Packages[1].Path != "curry" {
		t.Fatalf("packages = %+v, want haiku then curry (fish untouched, unmentioned)", res.Packages)
	}
	h, c := res.Packages[0], res.Packages[1]
	if h.Current != "v0.1.0" || h.PR != "minor" || h.Level != "minor" || h.Next != "v0.2.0" || h.Untagged {
		t.Fatalf("haiku = %+v, want v0.1.0 → minor → v0.2.0", h)
	}
	if c.PR != "patch" || c.Next != "v0.1.1" {
		t.Fatalf("curry = %+v, want patch → v0.1.1", c)
	}
	body := res.Body
	if !strings.HasPrefix(body, "<!-- glyph-pr-verdict -->\n**haiku** — 🔼") || !strings.Contains(body, "\n**curry** — 🔧") {
		t.Fatalf("body must open with one headline per touched line:\n%s", body)
	}
	if !strings.Contains(body, "**haiku/v0.1.0 → haiku/v0.2.0**") || !strings.Contains(body, "**curry/v0.1.0 → curry/v0.1.1**") {
		t.Fatalf("versions must be spelled as the line's tags:\n%s", body)
	}
	if strings.Contains(body, "fish") {
		t.Fatalf("a line the pull does not touch must not be mentioned:\n%s", body)
	}
	if !strings.Contains(body, "### haiku\n") || !strings.Contains(body, "### curry\n") || strings.Contains(body, "document the lines") {
		t.Fatalf("one table per touched line, the shared-only commit in none:\n%s", body)
	}
	if !strings.Contains(body, "since **haiku/v0.1.0** (haiku), **curry/v0.1.0** (curry)") {
		t.Fatalf("the footer must name every line's base:\n%s", body)
	}
}

// TestPreviewPackagesFoldsEachLinesPendingSide: the pending side is one walk
// and applies per line — a ^ already merged under curry escalates curry's
// answer while haiku's stays the PR's own.
func TestPreviewPackagesFoldsEachLinesPendingSide(t *testing.T) {
	dir, _ := packagesRepo(t)
	merged := touch(t, dir, "akira-toriyama", ":sparkles:^ add an ingredient", "curry/curry.go")
	routes := crossLinePull(9)
	routes[commitPullsPath(merged)] = `[]`
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Pending != "none" || h.Level != "minor" {
		t.Fatalf("haiku = %+v, want nothing pending and the PR's minor", h)
	}
	if c.Pending != "minor" || c.PR != "patch" || c.Level != "minor" || c.Next != "v0.2.0" {
		t.Fatalf("curry = %+v, want the pending minor to win over the PR's patch", c)
	}
	if !strings.Contains(res.Body, "**curry** — 🔧 Merging this PR adds **patch**-level changes — the next release stays **curry/v0.2.0**") {
		t.Fatalf("curry's headline must say the pending bump wins:\n%s", res.Body)
	}
}

// TestPreviewPackagesUntaggedLinesSkipTheWalk: when no touched line has a
// release tag there is no pending side to walk — walkServer proves the
// walk never ran — and each line reports its first release.
func TestPreviewPackagesUntaggedLinesSkipTheWalk(t *testing.T) {
	dir, _ := packagesRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "haiku/v0.1.0")
	testGit(t, dir, "akira-toriyama", "tag", "-d", "curry/v0.1.0")
	usePR(t, walkServer(t, crossLinePull(9)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if !res.Packages[0].Untagged || res.Packages[0].Next != "v0.1.0" {
		t.Fatalf("haiku = %+v, want untagged with a first release of v0.1.0", res.Packages[0])
	}
	if !strings.Contains(res.Body, "the first release here would be **haiku/v0.1.0**") || !strings.Contains(res.Body, "haiku and curry has no release tag yet") {
		t.Fatalf("body must say the lines have not released:\n%s", res.Body)
	}
}

// TestPreviewPackagesNothingTouchedSaysSo: a pull whose commits carry
// nothing on any line — shared-only = — says it moves nothing, with the
// marker in place and no walk.
func TestPreviewPackagesNothingTouchedSaysSo(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9):    `[` + apiCommit("s1", "akira-toriyama", ":memo:= document the lines") + `]`,
		commitFilesPath("s1"): apiFiles("README.md"),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if len(res.Packages) != 0 {
		t.Fatalf("packages = %+v, want none", res.Packages)
	}
	if !strings.HasPrefix(res.Body, "<!-- glyph-pr-verdict -->\n⏸️ Merging this PR moves nothing — its 1 commit(s) touch no declared package.") {
		t.Fatalf("body = %q", res.Body)
	}
}

// TestPreviewPackagesSharedOnlyBumpIsRefused: a ^ under no package in the
// pull is refused here, at exit 3, the way the walk and lint --range refuse
// it — preview says what CI will say, while the branch can still be fixed.
func TestPreviewPackagesSharedOnlyBumpIsRefused(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9):    `[` + apiCommit("s1", "akira-toriyama", ":sparkles:^ add a workspace file") + `]`,
		commitFilesPath("s1"): apiFiles("go.work"),
	}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "preview", "--pr", "9")
	if code != 3 || !strings.Contains(stderr, "touches no declared package") || !strings.Contains(stderr, "fix it on the branch") {
		t.Fatalf("preview exited %d, want 3 with the attribution refusal\nstderr: %s", code, stderr)
	}
}

// TestPreviewPackagesNotesPerLine: --notes folds one notes preview per
// touched line into the block, under the line's own heading.
func TestPreviewPackagesNotesPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, crossLinePull(9)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--notes")
	if code != 0 {
		t.Fatalf("preview --notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "<summary>Release notes preview</summary>\n\n# haiku\n\n## Features\n") || !strings.Contains(stdout, "\n# curry\n\n## Fixes\n") {
		t.Fatalf("notes must render per line under the line's heading:\n%s", stdout)
	}
}
