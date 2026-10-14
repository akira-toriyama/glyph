package draftplan

import (
	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// CheckPublishedFloor is the deadlock guard (immutable releases): the next
// version must be STRICTLY greater than the latest published release, or the
// draft could never be published — its tag is taken, or permanently burned if
// a published release was deleted. The repository's state is off, so this
// fails loud (4) instead of creating an unpublishable draft.
func CheckPublishedFloor(l config.Line, next bump.Version, releases []Draft) error {
	floor, ok := highestPublished(l, releases)
	if !ok || next.Compare(floor) > 0 {
		return nil
	}
	return core.APIf(
		"computed version %s is not greater than the latest published release %s — refusing the draft (it would collide with or regress below a published release; if a published release was deleted its tag is permanently burned — bump past it)",
		next.TagOn(l.Prefix), floor.TagOn(l.Prefix))
}

// highestPublished returns the highest published (non-draft) house version
// (bump.HouseTagOn) ON ONE LINE among the releases; ok is false when there is none —
// a line before its first publish has no floor. The floor is per line by
// construction (DESIGN §4.1): a published haiku/v2.0.0 is no floor under a
// curry/ draft, a published bare v2.0.0 is none under either package, and a
// published pubsub/v2.7.0 is none under the pubsub/ v1 line beside it
// (mutation rows published-floor-read-across-lines and
// line-reads-tags-of-every-major).
func highestPublished(l config.Line, releases []Draft) (bump.Version, bool) {
	var floor bump.Version
	found := false
	for _, r := range releases {
		if r.Draft {
			continue
		}
		v, ok := bump.HouseTagOn(l, r.TagName)
		if !ok {
			continue
		}
		if !found || v.Compare(floor) > 0 {
			floor, found = v, true
		}
	}
	return floor, found
}
