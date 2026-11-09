package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// prTrigger matches the `pull_request` key of a workflow's own `on:` block — two
// spaces of indent, the key on its own line. Matched as a key rather than as a
// substring so a `pull_request` inside a step's expression cannot pass for a
// trigger.
var prTrigger = regexp.MustCompile(`(?m)^  pull_request:?\s*$`)

// TestCheckScriptAccountsForEveryPRGate is the mechanical half of scripts/check.sh's
// header claim, which is that a green run there means a green CI.
//
// The claim goes false with nothing to tell when a pull-request gate check.sh
// does not mirror passes locally unseen. A prose header cannot notice a workflow
// appearing next to it, so this test asks the one question that keeps the
// header honest without freezing the script's shape — is every gate that runs
// on a pull request at least ACCOUNTED FOR, either mirrored or listed as not
// mirrored with a reason? The unit is the job, written <file> `<job>` in the header:
// build.yml alone carries six gates, so a file name it has already mentioned says
// nothing about a seventh job added to it (measured: a `newgate:` job in build.yml
// passed a check by file name).
//
// It deliberately does not check HOW a gate is handled. Deciding that is a
// judgement (reproducing zizmor's policy in shell would be a second source of
// truth for a fleet rule, and glyph has no .toml for taplo to read), and a test
// that encoded the judgement would have to be edited every time the judgement was
// revisited. What it refuses is the SILENT case: a new PR gate that check.sh has
// never heard of, which is how the header goes stale.
func TestCheckScriptAccountsForEveryPRGate(t *testing.T) {
	// Positive control: a trigger matcher that matched nothing would let every
	// workflow skip the loop below and pass this test vacuously for ever.
	const canary = "on:\n  push:\n    branches: [main]\n  pull_request:\n"
	if !prTrigger.MatchString(canary) {
		t.Fatalf("prTrigger no longer matches a real `on:` block (%q); every workflow would be "+
			"skipped and this test would pass vacuously — re-derive it from a workflow's on: block", canary)
	}

	script := repoFile(t, filepath.Join("scripts", "check.sh"))
	checked := 0
	for _, name := range workflowFiles(t) {
		raw := repoFile(t, filepath.Join(".github", "workflows", name))
		if !prTrigger.MatchString(raw) {
			continue // a reusable (workflow_call) or a tag-triggered build gates no pull request
		}
		jobs := jobIDsIn(code(raw))
		if len(jobs) == 0 {
			t.Errorf("%s runs on every pull request and jobIDsIn finds no job in it — the jobs: block "+
				"changed shape and this test is checking nothing there", name)
		}
		for _, job := range jobs {
			checked++
			if account := name + " `" + job + "`"; !strings.Contains(script, account) {
				t.Errorf("%s's job `%s` runs on every pull request, and scripts/check.sh's header never "+
					"writes %q. Its header claims a green run there means a green CI, so every PR gate "+
					"has to be either mirrored (add it, and add its name to MIRRORS so the run "+
					"reconciles) or listed in the NOT MIRRORED block WITH THE REASON. A gate the script "+
					"has not heard of is exactly how that claim went stale.", name, job, account)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no workflow under .github/workflows matched the pull_request trigger — either the " +
			"trigger shape changed or the directory moved, and this test is now checking nothing")
	}
}

// TestCheckScriptDeclaresNoSilentSkip pins the shape of the defect rather than one
// instance of it. The old script printed "(skipped — not installed; CI runs it)"
// for each optional linter and then finished with its success line, so a machine
// with neither installed reported a green verdict over two gates it never ran —
// which is what govulncheck did here, every run, unremarked.
//
// A gate is now either run or fatal. This test does not ask which; it refuses the
// third option of announcing an omission and carrying on, in any wording that
// still contains the word.
func TestCheckScriptDeclaresNoSilentSkip(t *testing.T) {
	script := repoFile(t, filepath.Join("scripts", "check.sh"))
	for i, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the header explains the retired behaviour on purpose
		}
		if strings.Contains(trimmed, "skipped") {
			t.Errorf("scripts/check.sh:%d announces a skip: %q. A CI gate that cannot run here must "+
				"be a hard error, not a line in the log followed by a success message — that is the "+
				"exact shape that let govulncheck never run and still report green (t-7tj3). If the "+
				"gate genuinely cannot be mirrored, move it to the NOT MIRRORED block with its reason.",
				i+1, trimmed)
		}
	}
}

// TestCheckScriptRunsBiteUnderGoModsToolchain guards the one gate here that runs
// under a Go the script chooses rather than the one every other gate resolves.
//
// go-bite exports GOTOOLCHAIN=local, correctly: in CI, setup-go has already
// installed the toolchain go.mod names. Locally nothing has. `nix develop` supplies
// nixpkgs' current go (measured: 1.26.4 against go.mod's `toolchain go1.26.5`) and a
// mise install leaks a GOROOT for a different version into the shell, so the driver
// and its GOROOT disagree and every package fails to compile — `cannot build
// bitescan`, no verdict, and the run never reaches its ✓ line (t-wmwb).
//
// The fix that suggests itself is `unset GOROOT`, and it is the one this test
// refuses. It compiles, so it looks right, but it leaves bite judging the diff with
// nixpkgs' go while CI judges it with go.mod's — a loud failure swapped for a silent
// divergence, in the script whose entire purpose is the claim that they agree. So
// the requirement is positive: hand go-bite the toolchain root the other gates
// resolved, which is what setup-go hands it in CI.
func TestCheckScriptRunsBiteUnderGoModsToolchain(t *testing.T) {
	script := repoFile(t, filepath.Join("scripts", "check.sh"))

	var invocation string
	derived := false
	for _, stmt := range executableStatements(script) {
		if strings.TrimSpace(stmt) == `BITE_GOROOT="$(go env GOROOT)"` {
			derived = true
		}
		if invocation == "" && strings.Contains(stmt, `bash "$BITE"`) {
			invocation = stmt
		}
	}
	if invocation == "" {
		t.Fatal(`no executable statement in scripts/check.sh runs bash "$BITE" — either the bite gate ` +
			"was removed (it is a mirrored gate, so MIRRORS must lose it too) or it is invoked some " +
			"other way and this test is now checking nothing")
	}

	if !derived {
		t.Error("scripts/check.sh no longer derives a GOROOT from `go env GOROOT`. That call is how the " +
			"script learns which toolchain go.mod's `toolchain` line resolved to under GOTOOLCHAIN=auto — " +
			"the same one setup-go installs in CI. Without it the bite gate has no way to run under CI's Go.")
	}
	for _, handOver := range []string{`GOROOT="$BITE_GOROOT"`, `PATH="$BITE_GOROOT/bin:$PATH"`} {
		if !strings.Contains(invocation, handOver) {
			t.Errorf("the bite invocation in scripts/check.sh does not hand over %s: %q\n\n"+
				"go-bite exports GOTOOLCHAIN=local, so it runs whatever `go` it finds against whatever "+
				"GOROOT it inherits. Without the derived root handed over, bite judges the diff under the "+
				"devshell's go instead of go.mod's, or cannot build at all where a leaked GOROOT names "+
				"another version (t-wmwb). Clearing GOROOT (`unset GOROOT`, `GOROOT=`) is the wrong fix: it "+
				"builds, and this script's header claims a green run here means a green CI, which that makes "+
				"quietly false. Hand bite GOROOT=\"$BITE_GOROOT\" and PATH=\"$BITE_GOROOT/bin:$PATH\", what "+
				"setup-go hands it in CI.",
				handOver, invocation)
		}
	}
}

// executableStatements drops comment lines and joins backslash continuations, so a
// guard reads one shell command as one string. Without the join, an invocation
// spread over two lines looks like two statements and a guard on it matches neither
// half.
func executableStatements(script string) []string {
	var out []string
	pending := ""
	for line := range strings.SplitSeq(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if head, continued := strings.CutSuffix(line, `\`); continued {
			pending += head
			continue
		}
		out = append(out, pending+line)
		pending = ""
	}
	if pending != "" {
		out = append(out, pending)
	}
	return out
}

// checkFixture runs scripts/check.sh's real text end to end in a throwaway
// repository, so a test reads what each gate was HANDED where a guard over the
// script's lines reads only what it says. Every tool but git is a stub and the
// gates this repo owns are empty scripts, so a run takes a moment; the hub is a
// real clone of a real origin, because a moved tag and a working tree on another
// branch are git's behaviour, not a stub's. Under CI's `sh` (dash on Linux) this
// also proves the script parses and runs there.
type checkFixture struct {
	t                                     *testing.T
	repo, hub, origin                     string
	biteRecord, floorRecord, policyRecord string
	lintLog, distLog, goldenLog           string
	stubs                                 string
	env                                   []string
}

const (
	// floorArgs is how go-ci.yml hands its LINT_FLOOR to the action.
	floorArgs    = "--enable=${{ env.LINT_FLOOR }}"
	coverReports = `printf 'example.com/x/x.go:1:\tF\t100.0%%\ntotal:\t(statements)\t87.3%%\n'`
	coverRefuses = `echo 'cover: bad mode line: garbage' >&2; exit 1`

	// floorStepRecords is a 'lint floor' step's run: block reduced to what the
	// gate is handed — the job's LINT_FLOOR, and the shell and options the runner
	// gives `shell: bash` — so a run of it is told apart from a copy of the real
	// step's text: check.sh reads the step out of go-ci.yml at the pin, and a
	// retyped copy would never write this record.
	floorStepRecords = `shell=not-bash
if [ -n "${BASH_VERSION:-}" ]; then shell=bash; fi
opts=""
case $- in *e*) opts=errexit ;; esac
if [ "$shell" = bash ] && shopt -oq pipefail; then opts="$opts pipefail"; fi
{
  echo "floor=$LINT_FLOOR"
  echo "shell=$shell"
  echo "opts=$opts"
} >"$FLOOR_RECORD"
`
	floorStepRefuses = `echo "::error::golangci-lint ran without the fleet lint floor: misspell"
exit 1
`
)

// hubRepoPolicy is repo-policy-check.sh reduced to what the gate is handed: the
// revision of the hub it came from and the directory it judges, which the real
// checker reads as `${1:-.}`: the working directory unless an argument names
// another (v2's scripts/repo-policy-check.sh).
func hubRepoPolicy(revision string) string {
	return `#!/usr/bin/env bash
{
  echo "revision=` + revision + `"
  echo "dir=$(cd "${1:-.}" && pwd)"
  echo "self=$0"
} >"$POLICY_RECORD"
`
}

const hubRepoPolicyRefuses = `#!/usr/bin/env bash
echo "README.ja.md: translation file — the fleet stores no translations"
exit 1
`

// hubGoBite is go-bite.sh reduced to what the gate is handed: the revision of
// the hub it came from, and whether it runs under bash proper. `sh` is dash on
// CI's Linux and bash in POSIX mode on macOS, so BASH_VERSION alone cannot tell.
func hubGoBite(revision string) string {
	return `#!/usr/bin/env bash
shell=not-bash
if [ -n "${BASH_VERSION:-}" ]; then
  if shopt -oq posix; then shell=bash-in-posix-mode; else shell=bash; fi
fi
{
  echo "revision=` + revision + `"
  echo "shell=$shell"
  echo "base=$BASE_SHA"
  echo "head=$HEAD_SHA"
  echo "self=$0"
} >"$BITE_RECORD"
`
}

// hubGoCI carries what check.sh reads out of the hub's go-ci.yml, in that file's
// shape at v2 (6a4ae14 and 97506e9): the pinned linter version, the job's
// LINT_FLOOR, the lint step's args and the 'lint floor' step after it, whose run:
// block here is floorStepRecords. An empty floor or args leaves that line out.
// v2's module hygiene step leads the job as it does there: the real file carries
// two run: blocks ahead of the floor step's, and an extraction keyed on any step
// name instead of 'lint floor' takes the first (on v2's go-ci.yml, `go mod tidy
// -diff` and `go mod verify`).
func hubGoCI(floor, args string) string {
	return hubGoCIWithFloorStep(floor, args, floorStepRecords)
}

// hubGoCIWithFloorStep is hubGoCI with the 'lint floor' step's run: block given;
// empty leaves the step out altogether.
func hubGoCIWithFloorStep(floor, args, floorStep string) string {
	raw := "on:\n  workflow_call:\n    inputs:\n      golangci-lint-version:\n        type: string\n" +
		"        default: 'v2.12.2'\n\njobs:\n  test:\n"
	if floor != "" {
		raw += "    env:\n      LINT_FLOOR: " + floor + "\n"
	}
	raw += "    steps:\n      - name: module hygiene (go mod tidy -diff + verify)\n" +
		"        if: runner.os == 'Linux'\n        run: |\n          go mod tidy -diff\n          go mod verify\n\n" +
		"      - name: golangci-lint\n" +
		"        uses: golangci/golangci-lint-action@" + strings.Repeat("0", 40) + " # v9.3.0\n" +
		"        with:\n          version: ${{ inputs.golangci-lint-version }}\n"
	if args != "" {
		raw += "          args: " + args + "\n"
	}
	if floorStep == "" {
		return raw
	}
	var b strings.Builder
	b.WriteString(raw)
	b.WriteString("\n      - name: lint floor (every floor linter was in the set)\n" +
		"        if: runner.os == 'Linux'\n        shell: bash\n        run: |\n")
	for line := range strings.Lines(floorStep) {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("          " + line)
	}
	return b.String()
}

// tagHub commits revision's go-bite.sh, repo-policy-check.sh and goCI to the
// hub's origin and puts tag on that commit, annotated as the real v2 is. On a
// tag the clone has already fetched, that is the hub moving it under the clone.
func (f *checkFixture) tagHub(tag, revision, goCI string) {
	f.t.Helper()
	f.tagHubWithRepoPolicy(tag, revision, goCI, hubRepoPolicy(revision))
}

func (f *checkFixture) tagHubWithRepoPolicy(tag, revision, goCI, repoPolicy string) {
	f.t.Helper()
	testutil.WriteFile(f.t, f.origin, "actions/go-bite/go-bite.sh", hubGoBite(revision))
	testutil.WriteFile(f.t, f.origin, "scripts/repo-policy-check.sh", repoPolicy)
	testutil.WriteFile(f.t, f.origin, ".github/workflows/go-ci.yml", goCI)
	testutil.Git(f.t, f.origin, "akira-toriyama", "add", "-A")
	testutil.Git(f.t, f.origin, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", revision)
	testutil.Git(f.t, f.origin, "akira-toriyama", "tag", "-f", "-a", "-m", tag, tag)
}

// hubRefs is every ref of the hub's clone with what it points at, the state a
// run must leave as it found it.
func (f *checkFixture) hubRefs() string {
	f.t.Helper()
	return testutil.Git(f.t, f.hub, "akira-toriyama", "for-each-ref", "--format=%(refname) %(objectname)")
}

// newCheckFixture leaves HEAD on main with origin/main at it, and the hub's
// clone at v2 with a working tree holding another revision of both hub files.
func newCheckFixture(t *testing.T) *checkFixture {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	f := &checkFixture{
		t:            t,
		repo:         filepath.Join(root, "repo"),
		hub:          filepath.Join(root, "hub"),
		origin:       filepath.Join(root, "hub-origin"),
		biteRecord:   filepath.Join(root, "bite.record"),
		floorRecord:  filepath.Join(root, "floor.record"),
		policyRecord: filepath.Join(root, "policy.record"),
		lintLog:      filepath.Join(root, "golangci-lint.log"),
		distLog:      filepath.Join(root, "dist-gate.log"),
		goldenLog:    filepath.Join(root, "golden-gate.log"),
		stubs:        filepath.Join(root, "bin"),
	}
	for _, d := range []string{f.repo, f.origin, f.stubs} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	testutil.Git(t, f.origin, "akira-toriyama", "init", "-q", "-b", "main")
	f.tagHub("v2", "v2", hubGoCI("errcheck,misspell", floorArgs))
	testutil.Git(t, root, "akira-toriyama", "clone", "-q", f.origin, f.hub)
	testutil.WriteFile(t, f.hub, "actions/go-bite/go-bite.sh", hubGoBite("the clone's working tree"))
	testutil.WriteFile(t, f.hub, "scripts/repo-policy-check.sh", hubRepoPolicy("the clone's working tree"))
	testutil.WriteFile(t, f.hub, ".github/workflows/go-ci.yml", hubGoCI("only-in-the-working-tree", floorArgs))

	for _, rel := range []string{"scripts/check.sh", "scripts/lib/trap.sh", ".github/workflows/build.yml", ".github/workflows/commit-lint.yml", ".github/workflows/repo-policy.yml"} {
		testutil.WriteFile(t, f.repo, rel, repoFile(t, rel))
	}
	for _, gate := range []string{"mutations", "fuzz-smoke", "smoke"} {
		testutil.WriteFile(t, f.repo, "scripts/"+gate+".sh", "exit 0\n")
	}
	testutil.WriteFile(t, f.repo, "scripts/dist-gate.sh", recordArgv(f.distLog))
	testutil.WriteFile(t, f.repo, "scripts/golden-gate.sh", recordArgv(f.goldenLog))
	f.git("init", "-q", "-b", "main")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "base")
	f.git("update-ref", "refs/remotes/origin/main", "HEAD")

	f.stubGo(coverReports)
	writeStub(t, f.stubs, "golangci-lint", `if [ "$1" = --version ]; then
  echo 'golangci-lint has version 2.12.2 built with go1.26.5 from (unknown) on (unknown)'
  exit 0
fi
`+recordArgv(f.lintLog))

	f.env = []string{
		"PATH=" + f.stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + root,
		"TMPDIR=" + t.TempDir(),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GLYPH_FLEET_HUB=" + f.hub,
		"BITE_RECORD=" + f.biteRecord,
		"FLOOR_RECORD=" + f.floorRecord,
		"POLICY_RECORD=" + f.policyRecord,
	}
	return f
}

// stubGo answers `go env GOROOT` and `go tool cover` with cover, and exits 0 for
// every gate the script runs through go.
func (f *checkFixture) stubGo(cover string) {
	f.t.Helper()
	writeStub(f.t, f.stubs, "go", `case "$1" in
  env) echo `+shq(filepath.Join(f.stubs, "goroot"))+` ;;
  tool) `+cover+` ;;
esac
`)
}

func (f *checkFixture) git(args ...string) string {
	f.t.Helper()
	return testutil.Git(f.t, f.repo, "akira-toriyama", args...)
}

// commitToRepo commits content at rel on the current branch.
func (f *checkFixture) commitToRepo(rel, content, message string) string {
	f.t.Helper()
	testutil.WriteFile(f.t, f.repo, rel, content)
	f.git("add", "-A")
	f.git("commit", "-q", "-m", message)
	return f.git("rev-parse", "HEAD")
}

func (f *checkFixture) run() stepRun {
	f.t.Helper()
	return execute(f.t, []string{requireTool(f.t, "sh"), filepath.Join(f.repo, "scripts", "check.sh")}, f.repo, f.env)
}

// runGreen runs the script and fails the test unless it reached its ✓ line,
// which it returns.
func (f *checkFixture) runGreen() string {
	f.t.Helper()
	run := f.run()
	last := lastLine(run.stdout)
	if run.exit != 0 || !strings.HasPrefix(last, "✓ ") {
		f.t.Fatalf("check.sh exited %d with final line %q, want 0 and a ✓ line\n%s", run.exit, last, run)
	}
	return last
}

// refused fails the test unless the run ended 1 with no ✓ line and carries on
// stderr.
func (f *checkFixture) refused(run stepRun, carries string) {
	f.t.Helper()
	if run.exit != 1 || strings.Contains(run.stdout, "✓") {
		f.t.Fatalf("check.sh exited %d, want 1 and no ✓ line\n%s", run.exit, run)
	}
	if !strings.Contains(run.stderr, carries) {
		f.t.Errorf("check.sh's refusal does not carry %q\n%s", carries, run)
	}
}

// biteSaw reads back what the hub's go-bite.sh recorded; empty when it never ran.
func (f *checkFixture) biteSaw() map[string]string { return f.recorded(f.biteRecord) }

// floorSaw reads back what the 'lint floor' step recorded; empty when it never ran.
func (f *checkFixture) floorSaw() map[string]string { return f.recorded(f.floorRecord) }

// policySaw reads back what the hub's repo-policy-check.sh recorded; empty when
// it never ran.
func (f *checkFixture) policySaw() map[string]string { return f.recorded(f.policyRecord) }

func (f *checkFixture) recorded(path string) map[string]string {
	f.t.Helper()
	saw := map[string]string{}
	for line := range strings.Lines(readIfAny(f.t, path)) {
		k, v, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		saw[k] = v
	}
	return saw
}

// gateArgv returns the one call a recorded gate stub saw, failing the test on
// any other count.
func (f *checkFixture) gateArgv(log string) []string {
	f.t.Helper()
	calls := recordedCalls(f.t, log)
	if len(calls) != 1 {
		f.t.Fatalf("%s ran %d times, want once: %v", filepath.Base(log), len(calls), calls)
	}
	return calls[0]
}

// lintArgv returns golangci-lint's one `run`, failing the test on any other count.
func (f *checkFixture) lintArgv() []string {
	f.t.Helper()
	calls := recordedCalls(f.t, f.lintLog)
	if len(calls) != 1 {
		f.t.Fatalf("golangci-lint ran %d times, want once: %v", len(calls), calls)
	}
	return calls[0]
}

// TestCheckScriptHandsBiteTheMergeCommit holds the bite gate to the commit CI
// runs it on. go-bite.yml hands the script github.sha, the merge of the branch
// into its base, and the script's pre-change tree is their merge base: base itself
// for a merge, the fork point for a branch head. So on a branch main has moved past,
// a test pinning what main has shipped since the fork bites here and pins nothing
// in CI (measured on a toy repository, 2026-11-04: exit 0 handed the branch head,
// exit 1 handed the merge; t-vqc7).
func TestCheckScriptHandsBiteTheMergeCommit(t *testing.T) {
	t.Run("a branch origin/main has moved past", func(t *testing.T) {
		f := newCheckFixture(t)
		fork := f.git("rev-parse", "HEAD")
		onMain := f.commitToRepo("main.txt", "main\n", "main moves")
		f.git("update-ref", "refs/remotes/origin/main", onMain)
		f.git("checkout", "-q", "-b", "work", fork)
		head := f.commitToRepo("work.txt", "work\n", "the branch")

		f.runGreen()

		saw := f.biteSaw()
		if saw["base"] != onMain {
			t.Errorf("bite's BASE_SHA is %q, want origin/main %s", saw["base"], onMain)
		}
		parents := strings.Fields(f.git("rev-list", "--parents", "-1", saw["head"]))
		if want := []string{saw["head"], onMain, head}; !slices.Equal(parents, want) {
			t.Errorf("bite's HEAD_SHA has commit and parents %v, want the merge of origin/main and HEAD %v", parents, want)
		}
		if got, want := f.git("rev-parse", saw["head"]+"^{tree}"), f.git("merge-tree", "--write-tree", onMain, head); got != want {
			t.Errorf("bite's HEAD_SHA holds tree %s, want the merge's %s", got, want)
		}
	})

	t.Run("nothing ahead of origin/main", func(t *testing.T) {
		f := newCheckFixture(t)
		head := f.git("rev-parse", "HEAD")
		f.git("update-ref", "refs/remotes/origin/main", f.git("commit-tree", "-p", head, "-m", "main moves", f.git("rev-parse", "HEAD^{tree}")))

		f.runGreen()

		if got := f.biteSaw()["head"]; got != head {
			t.Errorf("bite's HEAD_SHA is %q, want HEAD %s: nothing is ahead, so there is no merge to make and go-bite stands down on the empty range", got, head)
		}
		for _, log := range []string{f.distLog, f.goldenLog} {
			if argv, want := f.gateArgv(log), []string{"origin/main", head}; !slices.Equal(argv, want) {
				t.Errorf("%s was handed %v, want %v: nothing is ahead, so HEAD is what CI would check out", filepath.Base(log), argv, want)
			}
		}
	})

	t.Run("a conflict with origin/main", func(t *testing.T) {
		f := newCheckFixture(t)
		fork := f.git("rev-parse", "HEAD")
		onMain := f.commitToRepo("both.txt", "main\n", "main moves")
		f.git("update-ref", "refs/remotes/origin/main", onMain)
		f.git("checkout", "-q", "-b", "work", fork)
		f.commitToRepo("both.txt", "work\n", "the branch")

		run := f.run()

		f.refused(run, "does not merge cleanly")
		if !strings.Contains(run.stderr, "both.txt") {
			t.Errorf("the refusal does not name the conflicted path\n%s", run)
		}
		if saw := f.biteSaw(); len(saw) != 0 {
			t.Errorf("bite ran with no merge to run on: %v", saw)
		}
		for _, log := range []string{f.distLog, f.goldenLog} {
			if calls := recordedCalls(t, log); calls != nil {
				t.Errorf("%s ran with no merge to judge: %v", filepath.Base(log), calls)
			}
		}
	})
}

// TestCheckScriptHandsTheDiffGatesTheMergeCommit: build.yml's dist-gate and
// golden-gate jobs check out refs/pull/N/merge and diff BASE...HEAD from there,
// so their diff is the merge's. From the branch head the diff runs from the fork
// point, and on a branch stacked on a pull request main has since squashed it
// carries the parent's files: dist-gate counted the parent's test as evidence
// (exit 0 here, exit 1 in CI) and golden-gate demanded a trailer for the parent's
// golden (exit 1 here, exit 0 in CI) — measured 2026-11-08 on the real scripts
// (TestDistGateJudgesTheMergeItIsHanded, TestGoldenGateJudgesTheMergeItIsHanded).
// check.sh builds that merge once and hands the same commit to both gates and to
// bite.
func TestCheckScriptHandsTheDiffGatesTheMergeCommit(t *testing.T) {
	f := newCheckFixture(t)
	fork := f.git("rev-parse", "HEAD")
	onMain := f.commitToRepo("main.txt", "main\n", "main moves")
	f.git("update-ref", "refs/remotes/origin/main", onMain)
	f.git("checkout", "-q", "-b", "work", fork)
	head := f.commitToRepo("work.txt", "work\n", "the branch")

	f.runGreen()

	merge := f.biteSaw()["head"]
	if merge == "" || merge == head {
		t.Fatalf("bite's HEAD_SHA is %q, want the merge commit (TestCheckScriptHandsBiteTheMergeCommit holds its shape)", merge)
	}
	for _, log := range []string{f.distLog, f.goldenLog} {
		if argv, want := f.gateArgv(log), []string{"origin/main", merge}; !slices.Equal(argv, want) {
			t.Errorf("%s was handed %v, want %v — origin/main and the one merge commit bite judges, "+
				"so the gate diffs what CI's checkout diffs instead of the fork point's", filepath.Base(log), argv, want)
		}
	}
}

// TestCheckScriptRunsBiteUnderBash: the hub's action runs go-bite.sh with `shell:
// bash`, and the script is bash. Under dash it dies on `set -o pipefail` or on
// `done < <(…)` with exit 2, no verdict at all (measured: /bin/dash, 2026-11-04);
// `sh` works only where it happens to be bash (t-m29e).
func TestCheckScriptRunsBiteUnderBash(t *testing.T) {
	f := newCheckFixture(t)
	f.runGreen()
	if got := f.biteSaw()["shell"]; got != "bash" {
		t.Errorf("go-bite.sh ran under %q, want bash, as the action's `shell: bash` runs it", got)
	}
}

// TestCheckScriptReadsTheHubAtThePin holds both hub reads to the revision CI
// resolves: the tag build.yml pins each reusable at, as origin has it now. The
// clone's working tree holds whichever branch someone has out (on 2026-11-04 the
// real one held another session's), and the clone's copy of a moving tag falls
// behind without saying so, since a plain fetch leaves it where it was (t-n4xg).
func TestCheckScriptReadsTheHubAtThePin(t *testing.T) {
	floorMoved := hubGoCI("moved", floorArgs)

	t.Run("the clone's working tree holds another revision", func(t *testing.T) {
		f := newCheckFixture(t)
		f.runGreen()
		saw := f.biteSaw()
		if saw["revision"] != "v2" {
			t.Errorf("go-bite.sh came from %q, want the tag v2", saw["revision"])
		}
		if strings.HasPrefix(saw["self"], f.hub) {
			t.Errorf("go-bite.sh ran out of the clone, at %s", saw["self"])
		}
		if argv := f.lintArgv(); !slices.Contains(argv, "--enable=errcheck,misspell") {
			t.Errorf("golangci-lint's floor is not the tag's: %v", argv)
		}
	})

	t.Run("the tag moved on origin", func(t *testing.T) {
		f := newCheckFixture(t)
		f.tagHub("v2", "moved", floorMoved)
		before := f.hubRefs()

		final := f.runGreen()

		if got := f.biteSaw()["revision"]; got != "moved" {
			t.Errorf("go-bite.sh came from %q, want the tag where origin has it now", got)
		}
		if argv := f.lintArgv(); !slices.Contains(argv, "--enable=moved") {
			t.Errorf("golangci-lint's floor is not the moved tag's: %v", argv)
		}
		if strings.Contains(final, "SKEWED") {
			t.Errorf("the final line claims a skew over a tag it refreshed: %s", final)
		}
		if after := f.hubRefs(); after != before {
			t.Errorf("the run wrote the hub's clone, a repository it does not own — refs before:\n%s\nafter:\n%s", before, after)
		}
	})

	// A tag moved in the clone and not yet pushed is the clone owner's: the house
	// leaves a hub tag's force-push to the user, so this is exactly the state the
	// clone sits in while that waits. A forced fetch into the clone reset it to
	// origin's without a trace, tags keeping no reflog (measured 2026-11-08).
	t.Run("the clone's own moved tag is left where it is", func(t *testing.T) {
		f := newCheckFixture(t)
		testutil.Git(t, f.hub, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", "the next release, push pending")
		testutil.Git(t, f.hub, "akira-toriyama", "tag", "-f", "-a", "-m", "v2 moved here", "v2")
		before := f.hubRefs()

		f.runGreen()

		if got := f.biteSaw()["revision"]; got != "v2" {
			t.Errorf("go-bite.sh came from %q, want origin's v2, the revision CI resolves", got)
		}
		if after := f.hubRefs(); after != before {
			t.Errorf("the run rewrote the clone's refs — before:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("origin cannot be reached", func(t *testing.T) {
		f := newCheckFixture(t)
		testutil.Git(t, f.hub, "akira-toriyama", "remote", "set-url", "origin", filepath.Join(f.hub, "no-such-remote"))

		final := f.runGreen()

		if got := f.biteSaw()["revision"]; got != "v2" {
			t.Errorf("go-bite.sh came from %q, want the clone's copy of the tag", got)
		}
		if !strings.Contains(final, "SKEWED FROM CI: hub v2 read at the clone's copy") {
			t.Errorf("the final line does not say the tag could not be refreshed: %s", final)
		}
	})

	t.Run("the tag is nowhere", func(t *testing.T) {
		f := newCheckFixture(t)
		testutil.Git(t, f.origin, "akira-toriyama", "tag", "-d", "v2")
		testutil.Git(t, f.hub, "akira-toriyama", "tag", "-d", "v2")

		f.refused(f.run(), "cannot read the hub at v2")
	})

	t.Run("each reusable has its own pin", func(t *testing.T) {
		f := newCheckFixture(t)
		f.tagHub("v3", "v3", floorMoved)
		build := filepath.Join(".github", "workflows", "build.yml")
		f.commitToRepo(build, strings.Replace(repoFile(t, build), "go-bite.yml@v2", "go-bite.yml@v3", 1), "pin bite at v3")

		f.runGreen()

		if got := f.biteSaw()["revision"]; got != "v3" {
			t.Errorf("go-bite.sh came from %q, want v3, the tag build.yml pins go-bite.yml at", got)
		}
		if argv := f.lintArgv(); !slices.Contains(argv, "--enable=errcheck,misspell") {
			t.Errorf("golangci-lint's floor is not v2's, the tag build.yml pins go-ci.yml at: %v", argv)
		}
	})
}

// TestCheckScriptRunsTheLintFloorStep: `--enable` cannot hold a linter the config
// drops — a `linters.disable` entry beats the flag (golangci-lint v2.12.2,
// measured 2026-11-04), so the lint run exits 0 with that linter never run — and
// go-ci.yml's 'lint floor' step after it asks the tool what came out enabled
// (measured 2026-11-08 at v2: misspell disabled in .golangci.yml, the lint run 0
// issues, the step `::error::` exit 1). check.sh runs that step as its own gate,
// off the step's run: block read out of go-ci.yml at the pin — never a copy, which
// would be a second home for the fleet's floor check — under the shell the runner
// gives `shell: bash`, with the job's LINT_FLOOR (t-fmy1).
func TestCheckScriptRunsTheLintFloorStep(t *testing.T) {
	t.Run("the step from go-ci.yml at the pin runs as the runner runs it", func(t *testing.T) {
		f := newCheckFixture(t)
		f.runGreen()
		saw := f.floorSaw()
		if saw["floor"] != "errcheck,misspell" {
			t.Errorf("the step saw LINT_FLOOR=%q, want the tag's errcheck,misspell — or it never ran at all (%v)", saw["floor"], saw)
		}
		if saw["shell"] != "bash" {
			t.Errorf("the step ran under %q, want bash, as `shell: bash` runs it", saw["shell"])
		}
		if saw["opts"] != "errexit pipefail" {
			t.Errorf("the step ran with %q, want `errexit pipefail`, the -eo pipefail the runner gives `shell: bash`", saw["opts"])
		}
	})

	t.Run("a floor the tool dropped ends the run", func(t *testing.T) {
		f := newCheckFixture(t)
		f.tagHub("v2", "v2", hubGoCIWithFloorStep("errcheck,misspell", floorArgs, floorStepRefuses))

		run := f.run()

		if run.exit != 1 || strings.Contains(run.stdout, "✓") {
			t.Fatalf("check.sh exited %d, want 1 and no ✓ line: CI's job fails at this step\n%s", run.exit, run)
		}
		if !strings.Contains(run.stdout, "::error::golangci-lint ran without the fleet lint floor") {
			t.Errorf("the run does not carry the step's own verdict\n%s", run)
		}
		if argv := f.lintArgv(); argv[0] != "run" {
			t.Errorf("golangci-lint ran as %v, want the lint run ahead of the floor check, CI's order", argv)
		}
	})

	for _, tc := range []struct{ name, goCI string }{
		{name: "a go-ci.yml with no lint floor step", goCI: hubGoCIWithFloorStep("errcheck,misspell", floorArgs, "")},
		{name: "a step the runner would substitute into", goCI: hubGoCIWithFloorStep("errcheck,misspell", floorArgs, "echo \"${{ env.LINT_FLOOR }}\"\n")},
	} {
		t.Run(tc.name+" ends the run before the lint", func(t *testing.T) {
			f := newCheckFixture(t)
			f.tagHub("v2", "v2", tc.goCI)
			f.refused(f.run(), "'lint floor' step")
			if calls := recordedCalls(t, f.lintLog); calls != nil {
				t.Errorf("golangci-lint ran under a floor check this script cannot mirror: %v", calls)
			}
		})
	}
}

// TestCheckScriptRunsRepoPolicyAtThePin: repo-policy.yml's job checks the hub out
// at job.workflow_sha — the commit the caller's @v2 resolved to — and runs its
// repo-policy-check.sh over the caller's tree with bash. Read at the pin the same
// way bite and the lint floor are, that is the checker CI runs at the revision CI
// runs it (measured 2026-11-08: `repo-policy: clean`, exit 0, over this tree at v2),
// so the gate is mirrored and leaves the NOT MIRRORED list (t-2rj5).
func TestCheckScriptRunsRepoPolicyAtThePin(t *testing.T) {
	t.Run("the hub's script at the tag, over this tree", func(t *testing.T) {
		f := newCheckFixture(t)
		f.runGreen()
		saw := f.policySaw()
		if saw["revision"] != "v2" {
			t.Errorf("repo-policy-check.sh came from %q, want the tag v2 — or it never ran (%v)", saw["revision"], saw)
		}
		if strings.HasPrefix(saw["self"], f.hub) {
			t.Errorf("repo-policy-check.sh ran out of the clone's working tree, at %s", saw["self"])
		}
		// git answers the toplevel with symlinks resolved (macOS: /var is
		// /private/var), the stub records the path it was handed: compare both resolved.
		got, want := saw["dir"], f.git("rev-parse", "--show-toplevel")
		if r, err := filepath.EvalSymlinks(got); err == nil {
			got = r
		}
		if r, err := filepath.EvalSymlinks(want); err == nil {
			want = r
		}
		if got != want {
			t.Errorf("repo-policy-check.sh judged %q, want this checkout %s", got, want)
		}
	})

	t.Run("a violation ends the run", func(t *testing.T) {
		f := newCheckFixture(t)
		f.tagHubWithRepoPolicy("v2", "v2", hubGoCI("errcheck,misspell", floorArgs), hubRepoPolicyRefuses)

		run := f.run()

		if run.exit != 1 || strings.Contains(run.stdout, "✓") {
			t.Fatalf("check.sh exited %d, want 1 and no ✓ line: CI's repo-policy job fails here\n%s", run.exit, run)
		}
		if !strings.Contains(run.stdout, "translation file") {
			t.Errorf("the run does not carry the checker's own finding\n%s", run)
		}
	})

	// repo-policy.yml arrives by fleet-sync and build.yml is glyph's own, so the
	// two pins can stand at different tags.
	t.Run("the tag repo-policy.yml pins, not build.yml's", func(t *testing.T) {
		f := newCheckFixture(t)
		f.tagHub("v3", "v3", hubGoCI("errcheck,misspell", floorArgs))
		caller := filepath.Join(".github", "workflows", "repo-policy.yml")
		f.commitToRepo(caller, strings.Replace(repoFile(t, caller), "repo-policy.yml@v2", "repo-policy.yml@v3", 1), "pin repo-policy at v3")

		f.runGreen()

		if got := f.policySaw()["revision"]; got != "v3" {
			t.Errorf("repo-policy-check.sh came from %q, want v3, the tag repo-policy.yml pins it at — build.yml pins go-bite and go-ci at v2", got)
		}
	})
}

// TestCheckScriptLintsWithTheFleetFloor: go-ci.yml adds its floor with `--enable`,
// so a bare `golangci-lint run` judges with .golangci.yml's list alone and a linter
// the config leaves out is a finding only CI sees (measured 2026-11-04: misspell
// left out and a misspelling planted gave 0 issues bare, exit 1 with the floor;
// t-fmy1). The floor is the job's LINT_FLOOR as go-ci.yml hands it over, and any
// other args are a command this gate does not mirror.
func TestCheckScriptLintsWithTheFleetFloor(t *testing.T) {
	t.Run("the floor reaches the linter", func(t *testing.T) {
		f := newCheckFixture(t)
		f.runGreen()
		if argv, want := f.lintArgv(), []string{"run", "--enable=errcheck,misspell", "./..."}; !slices.Equal(argv, want) {
			t.Errorf("golangci-lint ran as %v, want %v", argv, want)
		}
	})

	for _, tc := range []struct{ name, goCI string }{
		{name: "a go-ci.yml with no floor", goCI: hubGoCI("", floorArgs)},
		{name: "args of another shape", goCI: hubGoCI("errcheck,misspell", floorArgs+" --timeout=5m")},
	} {
		t.Run(tc.name+" ends the run", func(t *testing.T) {
			f := newCheckFixture(t)
			f.tagHub("v2", "v2", tc.goCI)
			f.refused(f.run(), "no longer lints with --enable=$LINT_FLOOR")
			if calls := recordedCalls(t, f.lintLog); calls != nil {
				t.Errorf("golangci-lint ran on a command this gate does not mirror: %v", calls)
			}
		})
	}
}

// TestCheckScriptFinalLineSaysWhoJudgedTheLint: check.sh's commit-lint is this
// tree's binary and CI's is the tag commit-lint.yml pins, and the two can split on
// any rule this tree changed. The final line is what gets read, so that is where
// the difference is stated, on every run (t-6m8v).
func TestCheckScriptFinalLineSaysWhoJudgedTheLint(t *testing.T) {
	lint := filepath.Join(".github", "workflows", "commit-lint.yml")
	pin := regexp.MustCompile(`(lint\.yml)@\S+`)

	t.Run("the pin is read, not written down", func(t *testing.T) {
		f := newCheckFixture(t)
		raw := repoFile(t, lint)
		if !pin.MatchString(raw) {
			t.Fatalf("commit-lint.yml no longer pins lint.yml at a tag:\n%s", raw)
		}
		f.commitToRepo(lint, pin.ReplaceAllString(raw, "${1}@v9.8.7"), "another pin")
		f.git("update-ref", "refs/remotes/origin/main", "HEAD")

		if final := f.runGreen(); !strings.Contains(final, "commit-lint judged by this tree, CI judges at v9.8.7") {
			t.Errorf("the final line does not name the pin: %s", final)
		}
	})

	t.Run("a pin it cannot read", func(t *testing.T) {
		f := newCheckFixture(t)
		f.commitToRepo(lint, "jobs:\n  lint:\n    runs-on: ubuntu-latest\n", "no reusable")
		f.git("update-ref", "refs/remotes/origin/main", "HEAD")

		if final := f.runGreen(); !strings.Contains(final, "commit-lint judged by this tree, CI judges at a tag commit-lint.yml no longer names") {
			t.Errorf("the final line does not say the pin is unreadable: %s", final)
		}
	})
}

// TestCheckScriptRecordsCoverageAsAReport: coverage is a report and never a gate,
// so a profile go tool cover refuses must not end the run, and must not read as a
// gate that passed either: recording `ran coverage` past a lost pipeline status
// counts it in the ✓ line over a "bad mode line" (measured 2026-11-04; t-mxhe).
func TestCheckScriptRecordsCoverageAsAReport(t *testing.T) {
	gates := regexp.MustCompile(`^✓ (\d+)/(\d+) mirrored gates passed`)

	t.Run("a report", func(t *testing.T) {
		f := newCheckFixture(t)
		final := f.runGreen()
		if !strings.Contains(final, "coverage reported: 87.3%") {
			t.Errorf("the final line does not carry the total: %s", final)
		}
		if m := gates.FindStringSubmatch(final); m == nil || m[1] != m[2] {
			t.Errorf("the final line does not reconcile its gates: %s", final)
		}
	})

	t.Run("no report", func(t *testing.T) {
		f := newCheckFixture(t)
		f.stubGo(coverRefuses)
		final := f.runGreen()
		if !strings.Contains(final, "coverage NOT reported") {
			t.Errorf("the final line does not say the report failed: %s", final)
		}
		if strings.Contains(final, "coverage reported") {
			t.Errorf("the final line claims a report nobody made: %s", final)
		}
	})
}
