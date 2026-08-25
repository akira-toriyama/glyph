package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// captureErr swaps the diagnostic stream for one test and returns the buffer.
func captureErr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := errOut
	errOut = &buf
	t.Cleanup(func() { errOut = old })
	return &buf
}

// TestFinishSilentInterrupt: both adapters classify a canceled context as a
// Silent CodeInterrupted — the user's own Ctrl-C. finish must return 130 and
// write NO stderr envelope: the abort is the user's own doing, and a release
// job grepping stderr for {"error":...} must not see one.
func TestFinishSilentInterrupt(t *testing.T) {
	stderr := captureErr(t)
	code := finish(&core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true})
	if code != 130 {
		t.Fatalf("finish(interrupted) = %d, want 130", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a Silent error must write no envelope, stderr got %q", stderr.String())
	}
}

// TestFinishBareErrorIsUsage: a non-*core.Error reaching finish can only be a
// cobra parse/usage problem (app and core always classify), so it maps to
// usage (2) with the envelope rendered — the ONE place the unclassified
// default flips from API to usage.
func TestFinishBareErrorIsUsage(t *testing.T) {
	stderr := captureErr(t)
	code := finish(errors.New("unknown flag: --nope"))
	if code != 2 {
		t.Fatalf("finish(bare error) = %d, want 2 (usage)", code)
	}
	env := decodeErrorEnvelope(t, stderr.String())
	if env.Code != 2 || env.Message != "unknown flag: --nope" {
		t.Fatalf("envelope = %+v, want code 2 carrying the cobra message", env)
	}
}

// TestCompletionRejectsAnUnknownShell pins the exit code on the one
// invalid-argument path in this CLI that was not usage(2).
//
// cobra owns the `completion` command and sets Args: NoArgs on it, but gives it
// no Run — and Command.execute() returns flag.ErrHelp for an unrunnable command
// BEFORE validating arguments, so that NoArgs never ran. The measured result was
// exit 0 with 563 bytes of the parent's help on STDOUT, which matters because
// this command's output is redirected into a file the shell sources: `glyph
// completion zshh > _glyph` reported success and wrote English prose where a
// completion script belongs, breaking every later shell start with nothing
// having failed. Every other shape already exited 2 (`glyph bogus`, `glyph hook
// bogus`, `glyph completion zsh extra`).
//
// The assertion is the CODE, not merely non-zero: 2 is usage and 3 is the gate
// code a fleet lint job hard-fails on, and nothing downstream can tell them
// apart from truthiness.
func TestCompletionRejectsAnUnknownShell(t *testing.T) {
	code, stdout, _ := runGlyph(t, "completion", "bogus")
	if code != 2 {
		t.Fatalf("`completion bogus` exited %d, want 2 (usage) — an unknown shell must not look like success", code)
	}
	if stdout != "" {
		// The payload stream is what a caller redirects into the file it sources.
		t.Fatalf("`completion bogus` wrote %q to the payload stream, want nothing", stdout)
	}
}

// The positive control for the guard above — that `completion zsh` still emits a
// script — is deliberately NOT here, and the reason is a property of cobra worth
// knowing before touching this. Cobra captures the completion writer ONCE, at the
// moment the command tree is built (`out := c.OutOrStdout()` in completions.go,
// closed over by every shell subcommand's RunE), so it cannot be redirected
// afterwards. Building the command eagerly — which is what lets us fix its RunE
// at all — therefore binds that writer to os.Stdout before runGlyph's SetOut can
// reach it. Identical in the real process, where Execute() never calls SetOut;
// but it means an in-process test cannot observe the script, and asserting on a
// stream a test cannot capture is how a green assertion stops meaning anything.
//
// So the control lives where the claim is actually observable: scripts/check.sh
// and build.yml's smoke run the real binary and assert the split — the script on
// stdout for a known shell, nothing on stdout and exit 2 for an unknown one.

// TestCompletionBareIsHelpAtZero: a parent command with no subcommand named
// prints help and exits 0 — the shape bare `glyph` and `glyph hook` already
// have. Pinned so the fix above cannot be mistaken for "any `completion`
// invocation without a shell is an error".
func TestCompletionBareIsHelpAtZero(t *testing.T) {
	code, _, _ := runGlyph(t, "completion")
	if code != 0 {
		t.Fatalf("bare `completion` exited %d, want 0 (help)", code)
	}
}

// TestFinishNil: a nil error is the silent success — exit 0, nothing written.
func TestFinishNil(t *testing.T) {
	stderr := captureErr(t)
	if code := finish(nil); code != 0 {
		t.Fatalf("finish(nil) = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("finish(nil) wrote %q to stderr, want nothing", stderr.String())
	}
}
