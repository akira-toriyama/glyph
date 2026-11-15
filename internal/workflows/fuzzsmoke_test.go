package workflows

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// realGoEnv is the whole environment for a fixture that runs the real go over a
// throwaway module: the caller's tool and caches, no module graph or flags of
// the caller's, no toolchain switch and no network. GOROOT stays out on purpose:
// a shell's GOROOT can name another version than the go on PATH (t-wmwb).
func realGoEnv(t *testing.T) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + t.TempDir(),
		"GOWORK=off",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
	}
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "XDG_CACHE_HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

const fuzzFixtureMod = "module example.com/fixture\n\ngo 1.21\n"

// TestFuzzSmokeFailsWhenDiscoveryFails runs the script under the real go, because
// what it pins is how go's failures reach `set -e`. Through `for x in $(cmd)` and a
// piped grep none of these reaches it: each exits 0 having fuzzed nothing, and in CI
// the extras job's fuzz step stays green beside the red `ci` job (measured
// 2026-11-04 under sh and dash; t-8f31). The broken package sorts ahead of a real
// target so a swallowed failure goes on to exit 0 rather than falling into the
// zero-targets refusal, which would prove nothing about the listing.
func TestFuzzSmokeFailsWhenDiscoveryFails(t *testing.T) {
	sh := requireTool(t, "sh")
	requireTool(t, "go")

	const fuzzTarget = "package p\n\nimport \"testing\"\n\nfunc FuzzP(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }\n"

	for _, tc := range []struct {
		name     string
		files    map[string]string
		carries  string
		misreads string // a diagnosis this failure must not get
	}{
		{
			name:     "go list refuses the module",
			files:    map[string]string{"go.mod": "junk\n", "p/p.go": "package p\n"},
			carries:  "errors parsing go.mod",
			misreads: "no Fuzz target",
		},
		{
			name: "a test file does not build",
			files: map[string]string{
				"go.mod":      fuzzFixtureMod,
				"a/a.go":      "package a\n",
				"a/a_test.go": "package a\n\nfunc init() { undefinedSymbol() }\n",
				"p/p.go":      "package p\n",
				"p/p_test.go": fuzzTarget,
			},
			carries: "undefined: undefinedSymbol",
		},
		{
			name: "no package has a target",
			files: map[string]string{
				"go.mod":      fuzzFixtureMod,
				"p/p.go":      "package p\n",
				"p/p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n",
			},
			carries: "no Fuzz target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, dir, "scripts/fuzz-smoke.sh", repoFile(t, "scripts/fuzz-smoke.sh"))
			for rel, content := range tc.files {
				testutil.WriteFile(t, dir, rel, content)
			}
			run := execute(t, []string{sh, filepath.Join(dir, "scripts", "fuzz-smoke.sh")}, dir, realGoEnv(t))
			if run.exit != 1 {
				t.Fatalf("fuzz-smoke exited %d, want 1\n%s", run.exit, run)
			}
			if !strings.Contains(run.stderr, tc.carries) {
				t.Errorf("fuzz-smoke's failure does not carry %q\n%s", tc.carries, run)
			}
			if tc.misreads != "" && strings.Contains(run.stderr, tc.misreads) {
				t.Errorf("fuzz-smoke reads this failure as %q\n%s", tc.misreads, run)
			}
		})
	}
}

// TestFuzzSmokeRunsEveryTargetAndSaysHowMany drives the script over a stub go,
// because a real -fuzztime 200000x run per target costs more than what it would
// add: the call shape is the claim. Every discovered target gets exactly one run at
// the count bound, a package with no tests is no failure, and the last line says
// how many ran, the number a reader has to set against the targets they committed.
func TestFuzzSmokeRunsEveryTargetAndSaysHowMany(t *testing.T) {
	sh := requireTool(t, "sh")
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "scripts/fuzz-smoke.sh", repoFile(t, "scripts/fuzz-smoke.sh"))
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "fuzz.log")
	writeStub(t, bin, "go", `case "$*" in
  'list ./...') printf 'example.com/a\nexample.com/b\nexample.com/c\n' ;;
  'test -list ^Fuzz example.com/a') printf 'FuzzOne\nFuzzTwo\nok  \texample.com/a\t0.1s\n' ;;
  'test -list ^Fuzz example.com/b') printf '?   \texample.com/b\t[no test files]\n' ;;
  'test -list ^Fuzz example.com/c') printf 'FuzzThree\nok  \texample.com/c\t0.1s\n' ;;
  'test example.com/'*) `+recordArgv(log)+` ;;
  *) echo "unexpected: go $*" >&2; exit 99 ;;
esac
`)
	run := execute(t, []string{sh, filepath.Join(dir, "scripts", "fuzz-smoke.sh")}, dir, []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")})
	if run.exit != 0 {
		t.Fatalf("fuzz-smoke exited %d, want 0\n%s", run.exit, run)
	}

	fuzzRun := func(pkg, target string) []string {
		return []string{"test", pkg, "-run", "^$", "-fuzz", "^" + target + "$", "-fuzztime", "200000x"}
	}
	want := [][]string{fuzzRun("example.com/a", "FuzzOne"), fuzzRun("example.com/a", "FuzzTwo"), fuzzRun("example.com/c", "FuzzThree")}
	if got := recordedCalls(t, log); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("fuzz-smoke ran %v, want %v", got, want)
	}
	if last := lastLine(run.stdout); last != "✓ fuzz-smoke: 3 Fuzz target(s) ran" {
		t.Errorf("fuzz-smoke's last line is %q, want the count of targets that ran", last)
	}
}
