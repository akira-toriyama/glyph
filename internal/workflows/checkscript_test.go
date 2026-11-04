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
	t                   *testing.T
	repo, hub, origin   string
	biteRecord, lintLog string
	stubs               string
	env                 []string
}

const (
	// floorArgs is how go-ci.yml hands its LINT_FLOOR to the action.
	floorArgs    = "--enable=${{ env.LINT_FLOOR }}"
	coverReports = `printf 'example.com/x/x.go:1:\tF\t100.0%%\ntotal:\t(statements)\t87.3%%\n'`
	coverRefuses = `echo 'cover: bad mode line: garbage' >&2; exit 1`
)

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
// shape at v2 (6a4ae14): the pinned linter version, the job's LINT_FLOOR and the
// lint step's args. An empty floor or args leaves that line out.
func hubGoCI(floor, args string) string {
	raw := "on:\n  workflow_call:\n    inputs:\n      golangci-lint-version:\n        type: string\n" +
		"        default: 'v2.12.2'\n\njobs:\n  test:\n"
	if floor != "" {
		raw += "    env:\n      LINT_FLOOR: " + floor + "\n"
	}
	raw += "    steps:\n      - name: golangci-lint\n" +
		"        uses: golangci/golangci-lint-action@" + strings.Repeat("0", 40) + " # v9.3.0\n" +
		"        with:\n          version: ${{ inputs.golangci-lint-version }}\n"
	if args != "" {
		raw += "          args: " + args + "\n"
	}
	return raw
}

// tagHub commits revision's go-bite.sh and goCI to the hub's origin and puts tag
// on that commit, annotated as the real v2 is. On a tag the clone has already
// fetched, that is the hub moving it under the clone.
func (f *checkFixture) tagHub(tag, revision, goCI string) {
	f.t.Helper()
	testutil.WriteFile(f.t, f.origin, "actions/go-bite/go-bite.sh", hubGoBite(revision))
	testutil.WriteFile(f.t, f.origin, ".github/workflows/go-ci.yml", goCI)
	testutil.Git(f.t, f.origin, "akira-toriyama", "add", "-A")
	testutil.Git(f.t, f.origin, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", revision)
	testutil.Git(f.t, f.origin, "akira-toriyama", "tag", "-f", "-a", "-m", tag, tag)
}

// newCheckFixture leaves HEAD on main with origin/main at it, and the hub's
// clone at v2 with a working tree holding another revision of both hub files.
func newCheckFixture(t *testing.T) *checkFixture {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	f := &checkFixture{
		t:          t,
		repo:       filepath.Join(root, "repo"),
		hub:        filepath.Join(root, "hub"),
		origin:     filepath.Join(root, "hub-origin"),
		biteRecord: filepath.Join(root, "bite.record"),
		lintLog:    filepath.Join(root, "golangci-lint.log"),
		stubs:      filepath.Join(root, "bin"),
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
	testutil.WriteFile(t, f.hub, ".github/workflows/go-ci.yml", hubGoCI("only-in-the-working-tree", floorArgs))

	for _, rel := range []string{"scripts/check.sh", "scripts/lib/trap.sh", ".github/workflows/build.yml", ".github/workflows/commit-lint.yml"} {
		testutil.WriteFile(t, f.repo, rel, repoFile(t, rel))
	}
	for _, gate := range []string{"dist-gate", "golden-gate", "mutations", "fuzz-smoke", "smoke"} {
		testutil.WriteFile(t, f.repo, "scripts/"+gate+".sh", "exit 0\n")
	}
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
func (f *checkFixture) biteSaw() map[string]string {
	f.t.Helper()
	saw := map[string]string{}
	for line := range strings.Lines(readIfAny(f.t, f.biteRecord)) {
		k, v, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		saw[k] = v
	}
	return saw
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
	})
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

// lintFloor is go-ci.yml's LINT_FLOOR at the tag build.yml pins, the list its lint
// step passes as `--enable`. It is a copy because a test runs offline and the tag
// is not in this repo: scripts/check.sh reads the real list at the pin, this list
// can only go stale beside it. When the hub changes the floor, change it here.
var lintFloor = []string{"errcheck", "govet", "ineffassign", "misspell", "revive", "staticcheck", "unconvert", "unused", "gosec"}

// disabledLinters reads linters.disable out of a .golangci.yml by indentation, in
// both shapes YAML lets it take — no YAML library, like the rest of this package.
func disabledLinters(raw string) []string {
	_, rest, ok := strings.Cut("\n"+raw, "\nlinters:\n")
	if !ok {
		return nil
	}
	var names []string
	inDisable := false
	for line := range strings.SplitSeq(rest, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		switch indent := len(line) - len(strings.TrimLeft(line, " ")); {
		case indent == 0:
			return names
		case indent == 2:
			key, value, _ := strings.Cut(s, ":")
			inDisable = key == "disable"
			if inDisable {
				names = append(names, strings.FieldsFunc(value, func(r rune) bool {
					return strings.ContainsRune("[], \t", r)
				})...)
			}
		case inDisable && strings.HasPrefix(s, "- "):
			names = append(names, strings.Fields(s)[1])
		}
	}
	return names
}

// TestGolangciConfigDisablesNoFloorLinter holds what `--enable` cannot: in
// golangci-lint v2.12.2 a config's `linters.disable:` beats the flag (measured
// 2026-11-04: `disable: [misspell]` gave 0 issues with the floor passed, with a
// misspelling planted), so a disable here drops a fleet floor linter. In CI,
// go-ci.yml's floor-check step catches it by asking the tool; check.sh runs no
// copy of that step, so this is the local half, read off the YAML — a key the tool
// renames gets past it, and the CI step stays the authority.
//
// bite-exempt: pins a state .golangci.yml already holds, so it passes on any tree
// that has not disabled a floor linter. testdata/mutations holds its red.
func TestGolangciConfigDisablesNoFloorLinter(t *testing.T) {
	// Positive control: a reader that found nothing would pass this test for ever.
	for canary, want := range map[string][]string{
		"linters:\n  disable:\n    - misspell # why\n    - unused\n  enable:\n    - errcheck\n": {"misspell", "unused"},
		"version: \"2\"\nlinters:\n  disable: [misspell, unused]\n":                             {"misspell", "unused"},
		"linters:\n  settings:\n    disable:\n      - misspell\n":                               nil,
		"linters:\n  enable:\n    - misspell\nformatters:\n  disable:\n    - misspell\n":        nil,
	} {
		if got := disabledLinters(canary); !slices.Equal(got, want) {
			t.Fatalf("disabledLinters(%q) = %v, want %v — the absence asserted below is vacuous", canary, got, want)
		}
	}

	raw := repoFile(t, ".golangci.yml")
	if !strings.Contains("\n"+raw, "\nlinters:\n") {
		t.Fatal(".golangci.yml has no linters: block for disabledLinters to read, and this test is checking nothing")
	}
	for _, name := range disabledLinters(raw) {
		if slices.Contains(lintFloor, name) {
			t.Errorf(".golangci.yml disables %s, a fleet floor linter: `linters.disable:` beats go-ci.yml's "+
				"--enable, so the floor no longer runs here or in CI, and nothing reports it. Scope the "+
				"linter with `linters.settings` or `exclusions` instead.", name)
		}
	}
}
