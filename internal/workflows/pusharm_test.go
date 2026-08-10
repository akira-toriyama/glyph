package workflows

import (
	"path/filepath"
	"strings"
	"testing"
)

// lintBody returns lint.yml's executable body — the subject of every test in
// this file. The push arm lives in one step of one reusable, so unlike the
// sweep-style guards these read a single file on purpose.
func lintBody(t *testing.T) string {
	t.Helper()
	return code(repoFile(t, filepath.Join(".github", "workflows", "lint.yml")))
}

// TestLintPushArmJudgesOnlyTheDefaultBranch pins the boundary of the push arm:
// pushes to the default branch and nothing else.
//
// The boundary is a ratification, not a convenience. DESIGN §2 makes
// :construction: a violation only for merge candidates — a topic branch is
// exactly where WIP is legal — so a push arm that linted every branch would
// reverse that decision repo-wide the day a caller widened its trigger. And the
// wrong ref must REFUSE, not skip: a silent skip on an unexpected ref is the
// very defect class the push arm was added to close (a gate that answers green
// without judging), so the guard's failure mode has to be loud.
func TestLintPushArmJudgesOnlyTheDefaultBranch(t *testing.T) {
	body := lintBody(t)
	const guard = `if [ "$PUSHED_REF" != "refs/heads/$DEFAULT_BRANCH" ]`
	if !strings.Contains(body, guard) {
		t.Errorf("lint.yml's push arm no longer compares the pushed ref against the default "+
			"branch (%s missing) — a caller with a wide push trigger would now lint topic "+
			"branches, where :construction: is legal by DESIGN §2, reversing that ratification "+
			"fleet-wide at the pin", guard)
	}
	if !strings.Contains(body, "lints pushes to the default branch only") {
		t.Errorf("the wrong-ref refusal no longer says what it refuses and why — the message is " +
			"what turns a caller's trigger bug into a one-line fix instead of a silent skip")
	}
}

// TestLintPushArmRefusesWhatItCannotJudgeLoudly pins the two ranges the push
// arm cannot compute — and that each of them is a refusal, never a pass.
//
// An all-zeroes `before` (ref creation) has no base; a `before` that is not an
// ancestor of `after` (force push) makes before..after hold everything EXCEPT
// the rewritten commits, which are the ones in question. Linting either as an
// empty range is the silent-green failure this epic exists to kill: exit 0 with
// nothing judged. The assertion is structural — every "cannot judge" refusal
// must be immediately followed by `exit 1` — so a mutation that downgrades one
// refusal to a pass goes red even though the echo line survives.
func TestLintPushArmRefusesWhatItCannotJudgeLoudly(t *testing.T) {
	body := lintBody(t)
	for _, want := range []string{
		`"0000000000000000000000000000000000000000"`,
		"git merge-base --is-ancestor",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("lint.yml's push arm no longer checks %s — the range it cannot compute "+
				"would be linted as empty, and an empty range is a pass over work never looked at", want)
		}
	}

	lines := strings.Split(body, "\n")
	refusals := 0
	for i, line := range lines {
		if !strings.Contains(line, "cannot judge this push") {
			continue
		}
		refusals++
		next := ""
		for _, l := range lines[i+1:] {
			if strings.TrimSpace(l) != "" {
				next = l
				break
			}
		}
		if !strings.Contains(next, "exit 1") {
			t.Errorf("the refusal %q is not followed by `exit 1` (got %q) — a refusal that "+
				"does not exit is an annotation on a green run, i.e. the silent pass wearing "+
				"a warning", strings.TrimSpace(line), strings.TrimSpace(next))
		}
	}
	// Non-emptiness, in the shape envelope_test.go argues for: zero refusals
	// means the arm stopped refusing or the sentinel moved, and either way the
	// loop above asserted nothing.
	if refusals != 2 {
		t.Errorf("found %d 'cannot judge this push' refusals in lint.yml, want 2 (ref creation, "+
			"force push) — if the wording moved, move this sentinel with it", refusals)
	}
}

// TestLintPushArmAnnotatesButNeverGates pins the verdict split between the two
// arms: exit 3 fails a pull_request run and is swallowed — alone — on the push
// arm.
//
// The argument for swallowing: default-branch history is immutable, so a red
// verdict there could never be made green again, and a permanently red check is
// the noise that trains a fleet to stop reading its own gate. The argument for
// swallowing ONLY 3: an infra failure (exit 4) is rerunnable, and absorbing
// every non-zero would turn a broken checkout into a green gate — the very
// silence the arm exists to close. Both directions are asserted.
func TestLintPushArmAnnotatesButNeverGates(t *testing.T) {
	body := lintBody(t)
	const swallow = `if [ "$verdict" = "annotates" ] && [ "$status" -eq 3 ]; then`
	idx := strings.Index(body, swallow)
	if idx < 0 {
		t.Fatalf("lint.yml no longer swallows the gate code on the annotate arm (%s missing) — "+
			"a direct-push violation now reds a check that can never turn green, and DESIGN §7's "+
			"noise argument says that check stops being read", swallow)
	}
	after := body[idx:]
	next := ""
	for _, l := range strings.Split(after, "\n")[1:] {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimSpace(l), "echo") {
			next = strings.TrimSpace(l)
			break
		}
	}
	if next != "exit 0" {
		t.Errorf("the annotate-arm swallow is not an `exit 0` (got %q) — the branch exists "+
			"precisely to end the job green after the annotations", next)
	}
	if !strings.Contains(body, `exit "$status"`) {
		t.Errorf("lint.yml no longer forwards glyph's exit code verbatim at the end of the " +
			"step — the pull_request arm's verdict, and every infra failure on both arms, " +
			"just lost the integer the fleet branches on")
	}
	if !strings.Contains(body, "verdict=annotates") || !strings.Contains(body, "verdict=gates") {
		t.Errorf("the two-arm verdict split (verdict=gates / verdict=annotates) is gone from " +
			"lint.yml — the swallow above is now either dead code or unconditional, and both " +
			"are wrong in a direction this test can no longer tell")
	}
}
