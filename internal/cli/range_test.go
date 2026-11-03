package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestBumpRangeReturnsEveryViolation pins the accumulating walk: a range with
// two unmatched commits is ONE red run carrying both, each with its sha and
// detail, not two runs each revealing the next (a walk that stops at the first
// commit answers with no details, and the caller re-runs lint in a loop to
// find the rest). The Msg keeps the first failure's exact `commit <sha>:`
// head — the walk's own refusal (walk.walkRefusal) opens the same way, one
// shape at both gates.
func TestBumpRangeReturnsEveryViolation(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji one")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ a fine commit rides along")
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji two")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 3 {
		t.Fatalf("bump over unmatched commits exited %d, want 3\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("a refused range wrote a payload: %q", stdout)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if !strings.HasPrefix(env.Message, "commit ") || !strings.Contains(env.Message, "+1 more") {
		t.Errorf("message = %q — the first failure keeps its `commit <sha>:` head (the walk's refusal "+
			"opens the same way) and the rest ride as a count", env.Message)
	}
	var details []bump.Refusal
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("decoding details: %v\n%s", err, stderr)
	}
	if len(details) != 2 {
		t.Fatalf("details carry %d finding(s), want 2 — every unmatched commit in one answer:\n%s", len(details), stderr)
	}
	shas := map[string]bool{}
	for _, d := range details {
		if !strings.Contains(d.Detail, "matches none") {
			t.Errorf("finding detail %q should name the pattern mismatch", d.Detail)
		}
		shas[d.SHA] = true
	}
	if len(shas) != 2 {
		t.Errorf("findings anchor %d distinct commit(s), want 2:\n%s", len(shas), stderr)
	}
}

// TestBumpRangeHasNoTaste is the control that keeps the accumulation from
// quietly strictening the walk — glyph has no taste: a subject the pattern
// claims folds whatever its capitalization or punctuation; only a message NO
// pattern claims refuses the range.
func TestBumpRangeHasNoTaste(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ Fix The Crash.")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("bump over a claimed subject with capitals and a period exited %d, want 0 — "+
			"glyph has no taste (DESIGN §2)\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 — the commit classifies as the patch it is", stdout)
	}
}
