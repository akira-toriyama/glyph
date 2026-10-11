package workflows

// The install action's step, executed. Every fleet repo's lint, merge preview
// and release installs glyph through it, and what makes that install safe —
// both downloads fail at the HTTP status, the tarball matches checksums.txt,
// its provenance verifies, a verify that never succeeds fails the step, only
// the binary is extracted, and nothing runs before all of that — lives in
// shell lines no compiler reads. The harness runs the step's run: block under
// the `shell: bash` invocation against:
//   - an httptest server for the release download host, reached by the REAL
//     curl through a shim that rewrites only the URL's origin. A hand-written
//     curl stub would be a model of curl, and the flags under test (-f, -o,
//     --retry) would mean whatever the model says;
//   - the host's real tar and checksum tool (RUNNER_OS / RUNNER_ARCH name the
//     host, so the script picks the tool the host has);
//   - stubs for gh (records its argv, fails a set number of times), sleep (the
//     retry schedule costs no test time), and the release's own binary (a
//     script that leaves a marker when it runs and answers `version --json`
//     with the stamp the case gives it); the host's real jq reads that answer.
// The step's env: block — which inputs feed VER and GH_TOKEN — is outside the
// harness: it supplies both by name.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	installAction  = ".github/actions/install/action.yml"
	installStep    = "Install glyph (pinned release binary, checksum + provenance verified)"
	installVersion = "v9.8.7"
)

// releasePath is where the release host serves version's assets.
func releasePath(version string) string {
	return "/akira-toriyama/glyph/releases/download/" + version + "/"
}

// wantVerify is the provenance check the gate must ask of gh, after `gh`. The
// signer identity is part of the check: a verify against another workflow
// passes for anything that workflow built.
func wantVerify(tarball string) []string {
	return []string{"attestation", "verify", tarball,
		"--repo", "akira-toriyama/glyph",
		"--signer-workflow", "akira-toriyama/glyph/.github/workflows/goreleaser.yml"}
}

// hostRunner names the test host as a runner names itself.
func hostRunner(t *testing.T) (runnerOS, runnerArch string) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		runnerOS = "Linux"
		requireTool(t, "sha256sum")
	case "darwin":
		runnerOS = "macOS"
		requireTool(t, "shasum")
	default:
		t.Fatalf("no GitHub runner OS the install action serves maps to GOOS %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		runnerArch = "X64"
	case "arm64":
		runnerArch = "ARM64"
	default:
		t.Fatalf("no GitHub runner arch the install action serves maps to GOARCH %s", runtime.GOARCH)
	}
	return runnerOS, runnerArch
}

// releaseAsset names version's tarball for one platform as GoReleaser's
// archive name_template writes it: {{ .Version }}, the tag without its v.
func releaseAsset(version, goos, goarch string) string {
	return "glyph_" + strings.TrimPrefix(version, "v") + "_" + goos + "_" + goarch + ".tar.gz"
}

// releaseTarball is a release asset in GoReleaser's shape — LICENSE and
// README.md beside the binary — whose binary appends its argv to marker when
// it runs and answers `version --json` with stamp as its version. salt changes
// the bytes, not what the binary does.
func releaseTarball(t *testing.T, marker, stamp, salt string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{
		{"LICENSE", 0o644, "MIT\n"},
		{"README.md", 0o644, "# glyph\n"},
		{"glyph", 0o755, "#!/bin/sh\n# " + salt + "\nprintf '%s\\n' \"$*\" >> " + shq(marker) + "\n" +
			"if [ \"$*\" = 'version --json' ]; then printf '{\"version\":\"%s\",\"commit\":\"abc1234\"}\\n' " + shq(stamp) + "; fi\n"},
	} {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg, ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseHost serves a release's assets by path, answers status for the paths
// it is told to fail, and counts every request.
type releaseHost struct {
	files  map[string][]byte
	status map[string]int

	mu       sync.Mutex
	requests int
}

func (h *releaseHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests++
	h.mu.Unlock()
	if code := h.status[r.URL.Path]; code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	b, ok := h.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}

func (h *releaseHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests
}

// curlShim execs the real curl with each https://github.com/ URL moved onto
// origin and every other argument untouched. Any other URL is refused: the
// test must never reach the network.
func curlShim(realCurl, origin string) string {
	return `n=$#
for a do
  case $a in
    https://github.com/*) a=` + shq(origin+"/") + `"${a#https://github.com/}" ;;
    *://*) echo "curl shim: no URL but https://github.com/ is served here: $a" >&2; exit 98 ;;
  esac
  set -- "$@" "$a"
done
shift "$n"
exec ` + shq(realCurl) + ` "$@"
`
}

// TestInstallActionInstallsOnlyWhatItVerified runs the install step against a
// release it can trust and against each way a release can fail it, and reads
// the end state: the exit code, every gh call, and what landed in the PATH
// directory, in GITHUB_PATH and in the binary's marker. The verified release
// is the positive control: it proves the harness downloads, verifies,
// extracts and runs the real thing, so a refusal elsewhere is the step's and
// not the harness's.
func TestInstallActionInstallsOnlyWhatItVerified(t *testing.T) {
	script := extractRun(t, repoFile(t, filepath.FromSlash(installAction)), installStep)
	realCurl := requireTool(t, "curl")
	requireTool(t, "tar")
	requireTool(t, "jq")
	hostOS, hostArch := hostRunner(t)

	const (
		anyCalls = -1      // the case does not pin how many gh calls precede the refusal
		never    = 1 << 10 // a gh that fails every call
	)
	cases := []struct {
		name                 string
		version              string // the version input; "" is installVersion
		stamp                string // what the binary reports as its version; "" is the version input
		runnerOS, runnerArch string // "" names the host
		tarball, checksums   int    // the HTTP status each download answers; 0 serves it
		tampered             bool   // the tarball served is not the one checksums.txt hashes
		unlisted             bool   // checksums.txt has no line for the asset
		ghFails              int    // gh calls that fail before one verifies

		exit       int
		ghCalls    int
		installed  bool
		queried    bool   // a refused install asked the verified binary its version and went no further
		offline    bool   // nothing may be fetched
		annotation string // a line stdout must carry
		why        string
	}{
		{
			name: "a verified release installs the binary and only the binary", ghCalls: 1, installed: true,
			why: "the positive control: a release whose checksum, provenance and version all hold must install",
		},
		{
			name: "a missing tarball fails at its own download", tarball: http.StatusNotFound, exit: 22,
			why: "-f makes curl fail at the HTTP status (22). Without it curl exits 0 and saves the error page " +
				"as the tarball (measured 2026-09-29: a missing release asset fetched without -f writes the " +
				"9-byte body `Not Found`), and the step dies a stage later inside the checksum check, where the " +
				"failure reads as a tampered binary or a missing checksum line — in every fleet repo at once when " +
				"an outage outlives the retries",
		},
		{
			name: "a missing checksums.txt fails at its own download", checksums: http.StatusNotFound, exit: 22,
			why: "the checksums download carries the same -f as the tarball's, for the same misdiagnosis",
		},
		{
			name: "a tarball checksums.txt does not vouch for is refused", tampered: true, exit: 1, ghCalls: anyCalls,
			why: "README, DESIGN §6 and the action's description promise the tarball is checked against the " +
				"release's checksums.txt — with gh answering yes here, the checksum is all that stands between " +
				"these bytes and every fleet job's PATH",
		},
		{
			name: "an asset checksums.txt does not list is refused", unlisted: true, exit: 1, ghCalls: anyCalls,
			why: "a missing checksum line hands the check empty input, which must fail it, not pass it",
		},
		{
			name: "a provenance that never verifies fails after the fifth attempt", ghFails: never, exit: 1, ghCalls: 5,
			why: "the attestation gate retries five times and then fails closed — a gate that runs out of " +
				"attempts and carries on installs a tarball no attestation vouched for",
		},
		{
			name: "a provenance that verifies on the third attempt installs", ghFails: 2, ghCalls: 3, installed: true,
			why: "the retry exists so a transient attestations-API failure does not red the fleet's gates",
		},
		{
			name: "an unsupported runner OS is refused before any download", runnerOS: "Windows", exit: 1, offline: true,
			annotation: "::error::glyph install: unsupported runner OS 'Windows'",
			why:        "the release ships linux and darwin tarballs only",
		},
		{
			name: "an unsupported runner arch is refused before any download", runnerArch: "X86", exit: 1, offline: true,
			annotation: "::error::glyph install: unsupported runner arch 'X86'",
			why:        "the release ships amd64 and arm64 tarballs only",
		},
		{
			name: "latest is refused before any download", version: "latest", exit: 1, offline: true,
			annotation: "::error::glyph install: version latest is not a release tag (vX.Y.Z, or vX.Y.Z-<pre-release>)",
			why: "the version goes into the download URL, where `latest` is a 404 on a URL nobody published — " +
				"reported as a curl failure (exit 22) instead of as the input it is",
		},
		{
			name: "a version without its v is refused before any download", version: "9.8.7", exit: 1, offline: true,
			annotation: "::error::glyph install: version 9.8.7 is not a release tag (vX.Y.Z, or vX.Y.Z-<pre-release>)",
			why:        "glyph's tags carry the v, so a v-less version names a release that does not exist",
		},
		{
			name: "a truncated version is refused before any download", version: "v9.8", exit: 1, offline: true,
			annotation: "::error::glyph install: version v9.8 is not a release tag (vX.Y.Z, or vX.Y.Z-<pre-release>)",
			why:        "a typo is a 404 too, and must read as the input it is",
		},
		{
			name: "a version carrying a newline is refused on one annotation line", version: "v9.8.7\n::warning::x",
			exit: 1, offline: true,
			annotation: `::error::glyph install: version $'v9.8.7\n::warning::x' is not a release tag (vX.Y.Z, or vX.Y.Z-<pre-release>)`,
			why: "the refusal echoes the input onto stdout, where a raw newline would open a second workflow " +
				"command of the caller's choosing",
		},
		{
			name: "a release candidate installs", version: "v9.8.7-rc.1", ghCalls: 1, installed: true,
			why: "the reusables install release candidates by design — an rc is proved as a fleet gate before " +
				"the pins move (.goreleaser.yaml marks it prerelease), so the tag check must not refuse one",
		},
		{
			name: "a binary stamped without the v installs", stamp: strings.TrimPrefix(installVersion, "v"),
			ghCalls: 1, installed: true,
			why: "every release through v5.0.0 was stamped with GoReleaser's {{.Version}}, which drops the v " +
				"(measured 2026-10-11: the v5.0.0 asset reports \"5.0.0\"), and a reusable pinned at a SHA still " +
				"installs one through glyph-version",
		},
		{
			name: "a binary reporting another release is refused off PATH", stamp: "v9.8.6", exit: 1, ghCalls: 1,
			queried:    true,
			annotation: `::error::glyph install: the v9.8.7 release binary reports version v9.8.6`,
			why: "checksums and provenance prove who built the tarball, not which version it is — the caller " +
				"asked for one release and would run another",
		},
		{
			name: "a binary whose stamp missed is refused off PATH", stamp: "dev", exit: 1, ghCalls: 1, queried: true,
			annotation: `::error::glyph install: the v9.8.7 release binary reports version dev`,
			why: "Go's -X ignores an unknown symbol, so a release built with a stale injection path reports " +
				"\"dev\" and exits 0 — installed, it is a binary nobody can identify",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			version := installVersion
			if c.version != "" {
				version = c.version
			}
			stamp := version
			if c.stamp != "" {
				stamp = c.stamp
			}
			asset := releaseAsset(version, runtime.GOOS, runtime.GOARCH)
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			temp := filepath.Join(dir, "runner-temp")
			shims := filepath.Join(dir, "shims")
			for _, d := range []string{home, temp, shims} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			githubPath := filepath.Join(dir, "github-path")
			if err := os.WriteFile(githubPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "glyph-ran")
			ghLog := filepath.Join(dir, "gh-calls")

			release := releaseTarball(t, marker, stamp, "release")
			served := release
			if c.tampered {
				served = releaseTarball(t, marker, stamp, "tampered")
			}
			var sums strings.Builder
			for _, goos := range []string{"darwin", "linux"} {
				for _, goarch := range []string{"amd64", "arm64"} {
					a := releaseAsset(version, goos, goarch)
					switch {
					case a != asset:
						fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(a)), a)
					case !c.unlisted:
						fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(release), a)
					}
				}
			}
			host := &releaseHost{
				files: map[string][]byte{
					releasePath(version) + asset:           served,
					releasePath(version) + "checksums.txt": []byte(sums.String()),
				},
				status: map[string]int{
					releasePath(version) + asset:           c.tarball,
					releasePath(version) + "checksums.txt": c.checksums,
				},
			}
			srv := httptest.NewServer(host)
			t.Cleanup(srv.Close)

			writeStub(t, shims, "curl", curlShim(realCurl, srv.URL))
			writeStub(t, shims, "gh", recordArgv(ghLog)+fmt.Sprintf(
				"[ \"$(grep -c '' %s)\" -gt %d ] && exit 0\necho 'gh stub: verification failed' >&2\nexit 1\n",
				shq(ghLog), c.ghFails))
			writeStub(t, shims, "sleep", "exit 0\n")

			runnerOS, runnerArch := hostOS, hostArch
			if c.runnerOS != "" {
				runnerOS = c.runnerOS
			}
			if c.runnerArch != "" {
				runnerArch = c.runnerArch
			}
			run := runStep(t, script, bashShell, dir, []string{
				"PATH=" + shims + string(os.PathListSeparator) + os.Getenv("PATH"),
				"HOME=" + home,
				"RUNNER_TEMP=" + temp,
				"RUNNER_OS=" + runnerOS,
				"RUNNER_ARCH=" + runnerArch,
				"GITHUB_PATH=" + githubPath,
				"VER=" + version,
				"GH_TOKEN=stub-token",
			})

			if run.exit != c.exit {
				t.Errorf("the install step exited %d, want %d — %s\n%s", run.exit, c.exit, c.why, run)
			}
			calls := recordedCalls(t, ghLog)
			if c.ghCalls != anyCalls && len(calls) != c.ghCalls {
				t.Errorf("the install step called gh %d times, want %d — %s\n%s", len(calls), c.ghCalls, c.why, run)
			}
			for _, call := range calls {
				if want := wantVerify(filepath.Join(temp, asset)); !slices.Equal(call, want) {
					t.Errorf("the install step ran `gh %s`, want `gh %s` — the provenance gate must verify the "+
						"downloaded tarball against glyph's own release workflow, or it proves nothing about "+
						"who built it", strings.Join(call, " "), strings.Join(want, " "))
				}
			}
			if c.offline && host.count() != 0 {
				t.Errorf("the install step fetched %d asset(s) before refusing — %s\n%s", host.count(), c.why, run)
			}
			if c.annotation != "" && !hasLine(run.stdout, c.annotation) {
				t.Errorf("the install step printed no %q — a refusal must say what it refused\n%s", c.annotation, run)
			}

			binDir := filepath.Join(home, ".local", "bin")
			var extracted []string
			entries, err := os.ReadDir(binDir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			for _, e := range entries {
				extracted = append(extracted, e.Name())
			}
			ran := readIfAny(t, marker)
			onPath := readIfAny(t, githubPath)
			const query = "version --json\n"
			if !c.installed {
				wantRan := ""
				if c.queried {
					wantRan = query
				}
				if len(extracted) != 0 || ran != wantRan || onPath != "" {
					t.Errorf("a refused install extracted %v, ran the binary with %q (want %q) and wrote %q to "+
						"GITHUB_PATH — nothing unverified may be extracted, executed or put on PATH, the step "+
						"that would run it holds GH_TOKEN in its env, and a binary refused for its version is "+
						"one the caller's later steps must not find\n%s", extracted, ran, wantRan, onPath, run)
				}
				return
			}
			if !slices.Equal(extracted, []string{"glyph"}) {
				t.Errorf("the install extracted %v, want only glyph — the release tarball carries LICENSE and "+
					"README.md beside the binary (measured on v4.2.0's darwin_arm64 asset, 2026-09-29), and "+
					"this directory goes onto PATH for every later step of the caller's job", extracted)
			}
			if ran != query {
				t.Errorf("the installed binary ran with %q, want exactly one `glyph version --json` — the "+
					"question whose answer is compared with the version input", ran)
			}
			if want := binDir + "\n"; onPath != want {
				t.Errorf("GITHUB_PATH holds %q, want %q — the caller's later steps call bare `glyph`", onPath, want)
			}
		})
	}
}

// TestInstallActionIsOneUnconditionalBashStep pins what the harness above
// cannot see, because it runs one step's script and nothing around it: the
// action is composite with that one step, the step runs under `shell: bash`
// (the invocation the harness reproduces), and no step-level key lets the
// action pass without it — `if:` skips the step, `continue-on-error:` passes
// the action when it fails, and a second step can carry either for a check
// moved out of the first.
func TestInstallActionIsOneUnconditionalBashStep(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(installAction))

	shape := readStepShape(t, raw, installStep)
	if shape.keys["run"] != "|" || shape.keys["shell"] == "" {
		t.Fatalf("canary: the reader found keys %v on the install step, which has a `shell:` and a "+
			"`run: |` — every absence below would hold over a reader that sees nothing", shape.keys)
	}
	for _, key := range []string{"if", "continue-on-error"} {
		if _, ok := readStepShape(t, withStepKey(t, raw, installStep, key, "true"), installStep).keys[key]; !ok {
			t.Fatalf("canary: the reader does not see a `%s:` added to the install step — the check below "+
				"would pass it", key)
		}
	}

	if !slices.Equal(shape.parents, []string{"steps", "runs"}) {
		t.Errorf("the install step sits under %v, want runs.steps — the action is no longer the composite "+
			"whose step the harness runs", shape.parents)
	}
	if shape.items != 1 {
		t.Errorf("the install action has %d steps, want 1 — the harness runs the first step's script alone, "+
			"so a check moved into a second step (one an `if: false` skips, say) is a check no test runs", shape.items)
	}
	if shape.keys["shell"] != "bash" {
		t.Errorf("the install step runs under `shell: %s`, want bash — the harness reproduces GitHub's "+
			"`bash --noprofile --norc -eo pipefail {0}`, and another shell is another errexit and pipefail story",
			shape.keys["shell"])
	}
	for _, key := range []string{"if", "continue-on-error"} {
		if v, ok := shape.keys[key]; ok {
			t.Errorf("the install step carries `%s: %s` — a skipped step, or a failure the action absorbs, "+
				"installs whatever the step did not get to refuse, in every fleet job", key, v)
		}
	}
}

// hasLine reports whether text has a line that is exactly line.
func hasLine(text, line string) bool {
	return slices.Contains(strings.Split(text, "\n"), line)
}
