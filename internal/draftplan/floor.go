package draftplan

import (
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// CheckPublishedFloor is the published-floor guard: the next version must be
// STRICTLY greater than the highest published release on the line — the
// maximum version, whatever was published last (highestPublished reads no
// date). An equal version's tag is taken and a lower one would regress below a
// published release, so this fails loud (4) before anything is written, and
// the refusal names every published release at or above next and how to clear
// each (DESIGN §4). A deleted published release is invisible here, since the
// releases listing is all this reads; where immutable releases are on, its tag
// name is burned for good, the blind spot DESIGN §4 names.
func CheckPublishedFloor(l config.Line, next bump.Version, releases []Draft) error {
	floor, ok := highestPublished(l, releases)
	if !ok || next.Compare(floor) > 0 {
		return nil
	}
	return core.APIf(
		"computed version %s is not greater than the highest published release %s — refusing the draft, whose tag would collide with or regress below a published release (published at or above %s: %s). A run at a commit behind these tags (a re-run at an older commit) needs nothing: the run at the branch tip walks from them. Otherwise clear each: one this line released off this branch, by landing its tagged commit with a merge commit (`git merge -s ours <tag>` keeps the tree); one published by mistake, by deleting it with its tag (`gh release delete <tag> --cleanup-tag`) — but where immutable releases are on (`gh api repos/{owner}/{repo}/immutable-releases`), a deleted release's tag name can never be reused, and the line could never publish that version",
		next.TagOn(l.Prefix), floor.TagOn(l.Prefix), next.TagOn(l.Prefix), strings.Join(publishedAtOrAbove(l, next, releases), ", "))
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

// publishedAtOrAbove lists, ascending and spelled as the line spells its tags,
// every published house version on the line at or above next: each one the
// refusal must clear, not the highest alone, because a reader who clears only
// the highest meets the next one on the following run (t-hrfr R3; mutation row
// published-floor-names-only-the-highest). It reads the releases as
// highestPublished does, so the floor is always its last entry.
func publishedAtOrAbove(l config.Line, next bump.Version, releases []Draft) []string {
	var inTheWay []bump.Version
	for _, r := range releases {
		if v, ok := bump.HouseTagOn(l, r.TagName); ok && !r.Draft && v.Compare(next) >= 0 {
			inTheWay = append(inTheWay, v)
		}
	}
	slices.SortFunc(inTheWay, bump.Version.Compare)
	tags := make([]string, len(inTheWay))
	for i, v := range inTheWay {
		tags[i] = v.TagOn(l.Prefix)
	}
	return tags
}
