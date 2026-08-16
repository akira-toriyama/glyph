package draftplan

import (
	"testing"

	"github.com/akira-toriyama/glyph/internal/bump"
)

func d(id int64, tag string) Draft { return Draft{ID: id, TagName: tag, Draft: true} }

// TestNoneWithoutFlagIsV1 pins v1 parity: draft_on_none off, a none verdict
// converges the draft state to "no release is due" — every managed draft is
// stale (including a lingering placeholder), nothing is kept.
func TestNoneWithoutFlagIsV1(t *testing.T) {
	p := PlanDraft(bump.LevelNone, "v1.3.0", false, []Draft{d(1, "v1.2.3"), d(2, PlaceholderTag)})
	if p.Action != ActionDelete || p.Keep != nil || len(p.Stale) != 2 {
		t.Fatalf("plan = %+v, want delete of both managed drafts", p)
	}
	empty := PlanDraft(bump.LevelNone, "v1.3.0", false, nil)
	if empty.Action != ActionNone || len(empty.Stale) != 0 {
		t.Fatalf("plan = %+v, want a no-op when nothing exists", empty)
	}
}

// TestNoneWithFlagMaintainsPlaceholder pins the ratified draft_on_none
// behaviour: a none verdict keeps an Unreleased placeholder alive — created
// when absent, kept when present — instead of converging to nothing.
func TestNoneWithFlagMaintainsPlaceholder(t *testing.T) {
	fresh := PlanDraft(bump.LevelNone, "v1.3.0", true, nil)
	if fresh.Action != ActionCreate || fresh.Tag != PlaceholderTag {
		t.Fatalf("plan = %+v, want create of %q", fresh, PlaceholderTag)
	}
	kept := PlanDraft(bump.LevelNone, "v1.3.0", true, []Draft{d(2, PlaceholderTag)})
	if kept.Action != ActionUpdate || kept.Keep == nil || kept.Keep.ID != 2 || kept.Tag != PlaceholderTag {
		t.Fatalf("plan = %+v, want the existing placeholder kept", kept)
	}
	if len(kept.Stale) != 0 {
		t.Fatalf("plan staled %+v, want none", kept.Stale)
	}
}

// TestRealVerdictRetagsThePlaceholder pins the promise's second half: when
// the bump becomes real, the SAME draft retags to the real version — v1's
// update-in-place, never a delete-and-recreate.
func TestRealVerdictRetagsThePlaceholder(t *testing.T) {
	p := PlanDraft(bump.LevelMinor, "v1.3.0", true, []Draft{d(7, PlaceholderTag)})
	if p.Action != ActionUpdate || p.Keep == nil || p.Keep.ID != 7 {
		t.Fatalf("plan = %+v, want the placeholder retagged in place", p)
	}
	if p.Tag != "v1.3.0" {
		t.Errorf("Tag = %q, want v1.3.0", p.Tag)
	}
}

func TestRealVerdictPrefersTheExactTag(t *testing.T) {
	p := PlanDraft(bump.LevelPatch, "v1.2.4", true, []Draft{d(1, PlaceholderTag), d(2, "v1.2.4")})
	if p.Keep == nil || p.Keep.ID != 2 {
		t.Fatalf("plan = %+v, want the draft already at v1.2.4 kept", p)
	}
	if len(p.Stale) != 1 || p.Stale[0].ID != 1 {
		t.Errorf("the placeholder should be stale, got %+v", p.Stale)
	}
}

// TestPlaceholderClaimedWithFlagOff pins the cleanup half of the placeholder
// claim: the name is glyph's own artifact, so turning draft_on_none off must
// converge a lingering placeholder away, not orphan it.
func TestPlaceholderClaimedWithFlagOff(t *testing.T) {
	p := PlanDraft(bump.LevelMinor, "v2.0.0", false, []Draft{d(9, PlaceholderTag)})
	if p.Keep == nil || p.Keep.ID != 9 || p.Tag != "v2.0.0" {
		t.Fatalf("plan = %+v, want the placeholder claimed and retagged even with the flag off", p)
	}
}

// TestUnmanagedIsUntouchable pins the v1 boundary: published releases and a
// human's hand-named drafts never appear in a plan, whatever the verdict.
func TestUnmanagedIsUntouchable(t *testing.T) {
	human := []Draft{
		{ID: 1, TagName: "v9.9.9", Draft: false}, // published
		d(2, "beta-preview"),                     // hand-named draft
		d(3, "1.2.3"),                            // no leading v — not house-shaped
	}
	for _, flag := range []bool{true, false} {
		p := PlanDraft(bump.LevelNone, "v1.0.1", flag, human)
		if p.Keep != nil || len(p.Stale) != 0 {
			t.Fatalf("flag=%v: plan touched unmanaged releases: %+v", flag, p)
		}
		q := PlanDraft(bump.LevelMajor, "v10.0.0", flag, human)
		if q.Action != ActionCreate || q.Keep != nil || len(q.Stale) != 0 {
			t.Fatalf("flag=%v: plan = %+v, want a fresh create touching nothing", flag, q)
		}
	}
}

// TestNeverASecondDraft pins the v1 invariant surviving into v2: whatever
// exists, at most one draft is kept and everything else managed is stale.
func TestNeverASecondDraft(t *testing.T) {
	many := []Draft{d(1, "v1.0.0"), d(2, "v1.1.0"), d(3, PlaceholderTag), d(4, "v0.9.0")}
	p := PlanDraft(bump.LevelMinor, "v1.2.0", true, many)
	if p.Keep == nil {
		t.Fatalf("plan kept nothing: %+v", p)
	}
	if len(p.Stale) != len(many)-1 {
		t.Fatalf("keep + stale must cover every managed draft exactly once: %+v", p)
	}
}
