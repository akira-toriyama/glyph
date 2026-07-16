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
