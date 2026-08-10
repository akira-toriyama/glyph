package cli

import (
	"strings"
	"testing"
)

// TestFmtMessage is the command end of the paste-and-pass contract: stdout is
// the corrected message itself — the answer, not advice about it — and what
// fmt prints, lint passes. The measured loop this ends took one message
// through three lint round trips (invalid-scope short-circuits, and each
// paste surfaced the next rule); fmt answers them all at once.
func TestFmtMessage(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "fmt", "--message", ":sparkles: feat(Core): Add The Thing.")
	if code != 0 {
		t.Fatalf("fmt exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != ":sparkles:(core) add The Thing\n" {
		t.Fatalf("stdout = %q — the payload is the corrected message, one trailing newline", stdout)
	}
	if lintCode, _, _ := runGlyph(t, "lint", "--message", strings.TrimSuffix(stdout, "\n")); lintCode != 0 {
		t.Fatalf("what fmt printed, lint rejected — the invariant this command exists for")
	}

	// Idempotent: a clean message is not fmt's to touch.
	code, again, _ := runGlyph(t, "fmt", "--message", strings.TrimSuffix(stdout, "\n"))
	if code != 0 || again != stdout {
		t.Fatalf("fmt of its own output = %q (exit %d), want %q unchanged", again, code, stdout)
	}
}

// TestFmtRefusesWhatItCannotFix pins the refusal half: a violation with no
// mechanical fix is exit 3 with the violations in the envelope and NOTHING on
// stdout. A best-effort line on stdout would be piped into `git commit -F -`
// by the very caller the exit code was warning.
func TestFmtRefusesWhatItCannotFix(t *testing.T) {
	for name, message := range map[string]string{
		"undeclared removal": ":fire: Drop the preset.",
		"acronym first word": ":memo: TOML support arrives",
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, "fmt", "--message", message)
			if code != 3 {
				t.Fatalf("fmt exited %d, want 3\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("a refusal wrote %q to stdout — refusing WITH output is not refusing", stdout)
			}
			env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
			if env.Code != 3 || len(env.Details) == 0 {
				t.Fatalf("the refusal must carry the violations it could not fix:\n%s", stderr)
			}
		})
	}
}

// TestFmtStdinCleansLikeTheHook: --stdin reduces the file to the message git
// would record before formatting — same Cleanup, same reason as lint --stdin —
// so a COMMIT_EDITMSG with the template still attached formats to the message
// alone.
func TestFmtStdinCleansLikeTheHook(t *testing.T) {
	setStdin(t, ":bug: Fix the crash.\n\n# Please enter the commit message for your changes.\n# Lines starting with '#' will be ignored.\n")
	code, stdout, stderr := runGlyph(t, "fmt", "--stdin")
	if code != 0 {
		t.Fatalf("fmt --stdin exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != ":bug: fix the crash\n" {
		t.Fatalf("stdout = %q — the template must be cleaned away and the subject corrected", stdout)
	}
}

// TestFmtPassesThroughWhatIsNotItsToReword: subjects git writes itself are
// excluded exactly as lint excludes them — a message fmt would reword and lint
// would skip is the two commands disagreeing about whose message it is.
func TestFmtPassesThroughWhatIsNotItsToReword(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "fmt", "--message", "fixup! anything at all")
	if code != 0 || stdout != "fixup! anything at all\n" {
		t.Fatalf("an autosquash artifact must pass through unchanged, got %q (exit %d)\nstderr: %s",
			stdout, code, stderr)
	}
}

// TestFmtModeFlagsAreExclusiveAndRequired mirrors lint's discipline: exactly
// one input mode, and an explicit --stdin=false selects nothing rather than
// falling through to an arm it did not name.
func TestFmtModeFlagsAreExclusiveAndRequired(t *testing.T) {
	if code, _, _ := runGlyph(t, "fmt"); code != 2 {
		t.Fatalf("fmt with no mode exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "fmt", "--message", ":bug: x", "--stdin"); code != 2 {
		t.Fatalf("fmt with two modes exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "fmt", "--stdin=false"); code != 2 {
		t.Fatalf("fmt --stdin=false exited %d, want 2 — it selects no input mode", code)
	}
}
