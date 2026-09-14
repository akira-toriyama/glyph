package draftplan

import (
	"testing"

	"github.com/akira-toriyama/glyph/v3/internal/bump"
	"github.com/akira-toriyama/glyph/v3/internal/config"
)

// TestManagedIsPerMajor: two lines on one prefix (pubsub and pubsub/v2)
// each manage their own major's drafts and their own placeholder — the v2
// line's pubsub/v2.8.0 is never a stray of the pubsub/ line's convergence,
// and pubsub/v2/Unreleased is the v2 line's placeholder alone (mutation row
// line-reads-tags-of-every-major; t-z9d3).
func TestManagedIsPerMajor(t *testing.T) {
	all := []Draft{
		d(1, "pubsub/v1.9.2"), d(2, "pubsub/Unreleased"),
		d(3, "pubsub/v2.8.0"), d(4, "pubsub/v2/Unreleased"),
		d(5, "pubsub/v3.0.0"),
	}
	free := config.Line{Prefix: "pubsub/", Claimed: []int{2}}
	v2 := config.Line{Prefix: "pubsub/", Major: 2}
	for name, tc := range map[string]struct {
		line config.Line
		want []int64
	}{
		"free line: every major but 2":             {free, []int64{1, 2, 5}},
		"v2 line: major 2 and its own placeholder": {v2, []int64{3, 4}},
	} {
		got := managed(tc.line, all)
		ids := make([]int64, 0, len(got))
		for _, g := range got {
			ids = append(ids, g.ID)
		}
		if len(ids) != len(tc.want) {
			t.Errorf("%s: managed = %v, want %v", name, ids, tc.want)
			continue
		}
		for i := range tc.want {
			if ids[i] != tc.want[i] {
				t.Errorf("%s: managed = %v, want %v", name, ids, tc.want)
			}
		}
	}
	if got := PlaceholderTagOn(v2); got != "pubsub/v2/Unreleased" {
		t.Errorf("PlaceholderTagOn(v2) = %q", got)
	}
	p := PlanDraft(free, bump.LevelPatch, "pubsub/v1.9.2", false, all)
	if p.Action != ActionUpdate || p.Keep == nil || p.Keep.ID != 1 || len(p.Stale) != 2 || p.Stale[0].ID != 2 || p.Stale[1].ID != 5 {
		t.Errorf("patch on the free line = %+v, want update keeping id 1 with ids 2 and 5 stale and the v2 drafts untouched", p)
	}
}
