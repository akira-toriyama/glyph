package workflows

// lint.yml's range step ("Lint the commit range"), executed. Its run: block
// runs under the unspecified-shell invocation inside a throwaway repository —
// real git, so merge-base --is-ancestor answers for real, and real jq — with a
// glyph stub that records its argv and answers a set exit code and stderr.
// The step's env: block — which event fields feed EVENT, PR_BASE and the rest
// — is outside the harness: it supplies each by name.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/testutil"
)

// lintBody returns lint.yml's executable body.
func lintBody(t *testing.T) string {
	t.Helper()
	return code(repoFile(t, filepath.Join(".github", "workflows", "lint.yml")))
}

const (
	lintWorkflow  = ".github/workflows/lint.yml"
	lintRangeStep = "Lint the commit range"
	zeroSHA       = "0000000000000000000000000000000000000000"
)

// lintRepo holds root <- main and root <- rewritten: main is the default
// branch's tip, rewritten the commit a force push puts in its place.
type lintRepo struct{ dir, root, main, rewritten string }

func newLintRepo(t *testing.T) lintRepo {
	t.Helper()
	requireTool(t, "git")
	dir := t.TempDir()
	git := func(args ...string) string { return testutil.Git(t, dir, "fixture", args...) }
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "root")
	root := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "main")
	return lintRepo{
		dir:       dir,
		root:      root,
		main:      git("rev-parse", "HEAD"),
		rewritten: git("commit-tree", root+"^{tree}", "-p", root, "-m", "rewritten"),
	}
}

// lintEvent is the range step's env as the runner fills it: every name set,
// "" where the event carries no such field.
type lintEvent struct{ event, prBase, prHead, before, after, ref string }

// runLintRange runs the range step in repo for ev, with glyph answering
// glyphExit and glyphStderr, and returns the run and glyph's recorded calls.
func runLintRange(t *testing.T, repo lintRepo, ev lintEvent, glyphExit int, glyphStderr string) (stepRun, [][]string) {
	t.Helper()
	requireTool(t, "jq")
	dir := t.TempDir()

	// The step keeps glyph's stream in fixed /tmp/ files; each run gets its
	// own, so concurrent runs on one host never read each other's. A path the
	// count does not know is one the move would miss.
	script := extractRun(t, repoFile(t, filepath.FromSlash(lintWorkflow)), lintRangeStep)
	const tmpRefs = 8
	if n := strings.Count(script, "/tmp/"); n != tmpRefs {
		t.Fatalf("the range step names /tmp/ %d times, want %d — each is moved into a per-run directory "+
			"so concurrent runs cannot read each other's files; recount, and check the new one is a "+
			"path the move should cover", n, tmpRefs)
	}
	script = strings.ReplaceAll(script, "/tmp/", dir+"/")

	stubs := filepath.Join(dir, "stubs")
	if err := os.Mkdir(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	glyphLog := filepath.Join(dir, "glyph-calls")
	stderrFile := filepath.Join(dir, "glyph-stderr")
	if err := os.WriteFile(stderrFile, []byte(glyphStderr), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStub(t, stubs, "glyph", recordArgv(glyphLog)+"cat "+shq(stderrFile)+" >&2\nexit "+strconv.Itoa(glyphExit)+"\n")

	run := runStep(t, script, unspecifiedShell, repo.dir, []string{
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"EVENT=" + ev.event,
		"PR_BASE=" + ev.prBase,
		"PR_HEAD=" + ev.prHead,
		"PUSH_BEFORE=" + ev.before,
		"PUSH_AFTER=" + ev.after,
		"PUSHED_REF=" + ev.ref,
		"DEFAULT_BRANCH=main",
	})
	return run, recordedCalls(t, glyphLog)
}

// TestLintNoRefusalPassesSilently runs the range step over each event it must
// refuse and over the two it must lint. A refusal stands where the
// alternative is a range linted as empty or never linted, so each must exit
// 1, say what it refused, and never reach glyph. The glyph stub answers 0 —
// what the real binary answers the empty range `..` — so a refusal that lets
// the step through ends green. The two linted events are the positive
// controls: glyph is reached, with exactly the range the event names.
func TestLintNoRefusalPassesSilently(t *testing.T) {
	repo := newLintRepo(t)
	cases := []struct {
		name    string
		ev      lintEvent
		refusal string // a phrase of the ::error:: annotation; "" for an event the step lints
		why     string
	}{
		{
			name: "a pull request is linted over base..head",
			ev:   lintEvent{event: "pull_request", prBase: repo.root, prHead: repo.main, ref: "refs/pull/7/merge"},
			why:  "the positive control for the pull_request arm",
		},
		{
			name: "a push to the default branch is linted over before..after",
			ev:   lintEvent{event: "push", before: repo.root, after: repo.main, ref: "refs/heads/main"},
			why:  "the positive control for the push arm",
		},
		{
			name: "a pull request without a base SHA is refused", refusal: "empty base/head SHA",
			ev: lintEvent{event: "pull_request", prHead: repo.main, ref: "refs/pull/7/merge"},
			why: "a pull_request payload never lacks it, and linted anyway the range collapses toward `..`, " +
				"which glyph answers 0 (measured: `glyph lint --range ..` exits 0 with a \"nothing linted\" " +
				"warning) — the merge gate itself green on a pull it never read",
		},
		{
			name: "a pull request without a head SHA is refused", refusal: "empty base/head SHA",
			ev:  lintEvent{event: "pull_request", prBase: repo.root, ref: "refs/pull/7/merge"},
			why: "either half of the range missing is the same collapse",
		},
		{
			name: "a push to another branch is refused", refusal: "lints pushes to the default branch only",
			ev: lintEvent{event: "push", before: repo.root, after: repo.main, ref: "refs/heads/topic"},
			why: "a topic branch is judged by the pull_request arm as the merge candidate it becomes; a push " +
				"there is mid-branch and rewritable, so a caller whose trigger widened is refused with its " +
				"one-line fix rather than linted or skipped",
		},
		{
			name: "a push that created the branch is refused", refusal: "(ref creation)",
			ev:  lintEvent{event: "push", before: zeroSHA, after: repo.main, ref: "refs/heads/main"},
			why: "an all-zeroes before is ref creation: no base exists to lint from, and the refusal must say so",
		},
		{
			name: "a force push is refused", refusal: "not an ancestor of event.after",
			ev: lintEvent{event: "push", before: repo.main, after: repo.rewritten, ref: "refs/heads/main"},
			why: "after a force push before..after holds everything EXCEPT the rewritten commits, which are " +
				"the ones in question",
		},
		{
			name: "an event the step does not support is refused", refusal: "(got: merge_group)",
			ev: lintEvent{event: "merge_group"},
			why: "merge_group lands here deliberately — the fleet runs no merge queue — and a check that " +
				"linted nothing on such a run must not read green",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			run, calls := runLintRange(t, repo, c.ev, 0, "")
			if c.refusal == "" {
				var base, head string
				if c.ev.event == "pull_request" {
					base, head = c.ev.prBase, c.ev.prHead
				} else {
					base, head = c.ev.before, c.ev.after
				}
				want := [][]string{{"lint", "--range", base + ".." + head}}
				if run.exit != 0 || !slices.EqualFunc(calls, want, slices.Equal) {
					t.Errorf("the range step exited %d having called glyph %v, want exit 0 after exactly %v — %s\n%s",
						run.exit, calls, want, c.why, run)
				}
				return
			}
			if run.exit != 1 {
				t.Errorf("the range step exited %d, want 1 — %s\n%s", run.exit, c.why, run)
			}
			if len(calls) != 0 {
				t.Errorf("the range step reached glyph (%v) instead of refusing — %s\n%s", calls, c.why, run)
			}
			if !slices.ContainsFunc(strings.Split(run.stdout, "\n"), func(l string) bool {
				return strings.HasPrefix(l, "::error::") && strings.Contains(l, c.refusal)
			}) {
				t.Errorf("the range step printed no ::error:: naming %q — a refusal must say what it refused, "+
					"or the caller cannot tell a trigger bug from a broken gate\n%s", c.refusal, run)
			}
		})
	}
}

// TestLintPushArmAnnotatesButNeverGates runs the range step with glyph
// answering a violation (3) and an infra failure (4) on both arms. Exit 3
// fails a pull_request run and is swallowed — alone — on the push arm:
// default-branch history is immutable, so a red verdict there could never be
// made green again, and a permanently red check trains a fleet to stop reading
// its gate. An infra failure is rerunnable and stays loud on both arms;
// absorbing every non-zero would turn a broken checkout into a green gate.
func TestLintPushArmAnnotatesButNeverGates(t *testing.T) {
	repo := newLintRepo(t)
	pr := lintEvent{event: "pull_request", prBase: repo.root, prHead: repo.main, ref: "refs/pull/7/merge"}
	push := lintEvent{event: "push", before: repo.root, after: repo.main, ref: "refs/heads/main"}
	cases := []struct {
		name            string
		ev              lintEvent
		glyphExit, exit int
		why             string
	}{
		{"a violation fails a pull request", pr, 3, 3,
			"the pull_request arm is the merge gate, and glyph's exit code is its verdict"},
		{"a violation on the default branch is annotated, not gated", push, 3, 0,
			"merged history cannot be made green again, so a red check there is permanent noise"},
		{"an infra failure fails a pull request", pr, 4, 4,
			"an infra failure keeps its own code on the gating arm"},
		{"an infra failure fails a default-branch push", push, 4, 4,
			"the push arm swallows 3 alone — a broken checkout swallowed with it reads as a green gate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stderr := fmt.Sprintf(`{"error":{"code":%d,"message":"stub verdict"}}`+"\n", c.glyphExit)
			if c.glyphExit == 3 {
				stderr = "::error::stub finding\n" + stderr
			}
			run, calls := runLintRange(t, repo, c.ev, c.glyphExit, stderr)
			if len(calls) != 1 {
				t.Fatalf("the range step called glyph %d times, want once — the exit below would not be "+
					"glyph's verdict\n%s", len(calls), run)
			}
			if run.exit != c.exit {
				t.Errorf("on %s, glyph's exit %d left the step at %d, want %d — %s\n%s",
					c.ev.event, c.glyphExit, run.exit, c.exit, c.why, run)
			}
		})
	}
}

// TestLintRangeStepIsUnconditional pins what the harness cannot see, because
// it runs the step's script and nothing around it: no `if:` skips the step,
// no `continue-on-error:` turns its failure into a passing job, and no
// `shell:` replaces the unspecified-shell invocation the harness reproduces.
func TestLintRangeStepIsUnconditional(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(lintWorkflow))

	shape := readStepShape(t, raw, lintRangeStep)
	if shape.keys["run"] != "|" {
		t.Fatalf("canary: the reader found keys %v on the range step, which has a `run: |` — every "+
			"absence below would hold over a reader that sees nothing", shape.keys)
	}
	for _, key := range []string{"if", "continue-on-error", "shell"} {
		if _, ok := readStepShape(t, withStepKey(t, raw, lintRangeStep, key, "x"), lintRangeStep).keys[key]; !ok {
			t.Fatalf("canary: the reader does not see a `%s:` added to the range step — the check below "+
				"would pass it", key)
		}
	}

	if !slices.Equal(shape.parents, []string{"steps", "lint", "jobs"}) {
		t.Errorf("the range step sits under %v, want jobs.lint.steps", shape.parents)
	}
	for _, key := range []string{"if", "continue-on-error"} {
		if v, ok := shape.keys[key]; ok {
			t.Errorf("the range step carries `%s: %s` — a skipped step, or a failure the job absorbs, is a "+
				"lint check that is green whatever the commits say, in every fleet repo at the pin", key, v)
		}
	}
	if v, ok := shape.keys["shell"]; ok {
		t.Errorf("the range step names `shell: %s` — the harness reproduces the unspecified shell's "+
			"`bash -e {0}`; teach it the new invocation", v)
	}
}
