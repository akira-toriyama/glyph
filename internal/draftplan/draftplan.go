// Package draftplan is the v2 release's draft-convergence brain: given a
// fold verdict, the flag draft_on_none and what drafts exist, it says which
// ONE draft survives, what tag it converges to, and which are stale. It is
// pure — no git, no API, no clock — and holds the v1 planning invariants
// unchanged: never a second draft, the kept draft is retagged in place, and
// everything glyph did not manage is not glyph's to touch. What is new is
// the placeholder: with draft_on_none = true a none verdict keeps an
// "Unreleased" draft alive instead of converging to nothing, and the next
// real verdict retags that same draft to the real version (the ratified
// retag; the machinery is v1's update-in-place).
package draftplan

import (
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// PlaceholderTag is the tag name of the draft a none verdict maintains when
// draft_on_none is on. It is deliberately NOT house-shaped (no leading v, no
// version): a human cannot publish it into a tag that wedges the published
// floor, and no version is claimed before one exists.
const PlaceholderTag = "Unreleased"

// Draft is the slice of a release this package reads. ID is the identity
// drafts are kept and deleted by (tag-name resolution can hit a published
// release sharing the tag — cli/cli#9367, the v1 lesson).
type Draft struct {
	ID      int64
	TagName string
	Draft   bool
}

// Action is which convergence the plan performs, mirroring v1's verdict
// vocabulary (README: create / update / delete / none).
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionNone   Action = "none"
)

// Plan is the convergence: at most one draft kept (retagged to Tag), the
// rest of the MANAGED drafts stale. Unmanaged releases never appear.
type Plan struct {
	Action Action
	// Tag is what the kept (or created) draft converges to: the next version
	// on a release verdict, PlaceholderTag on a maintained none. Empty when
	// the plan is a pure delete or a no-op.
	Tag   string
	Keep  *Draft
	Stale []Draft
}

// managed says which drafts are glyph's to converge: unpublished drafts
// tagged with a house-shaped version — v1's definition — plus the
// placeholder. The placeholder is claimed UNCONDITIONALLY, not only while
// draft_on_none is on: the name is glyph's own artifact, and a user who
// turns the flag off must see the placeholder converge away on the next
// run, not linger as an orphan glyph pretends not to know.
func managed(releases []Draft) []Draft {
	var drafts []Draft
	for _, r := range releases {
		if !r.Draft {
			continue
		}
		if r.TagName == PlaceholderTag {
			drafts = append(drafts, r)
			continue
		}
		if !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		if _, err := bump.ParseVersion(r.TagName); err != nil {
			continue
		}
		drafts = append(drafts, r)
	}
	return drafts
}

// PlanDraft computes the convergence for one verdict. level none with
// draft_on_none off is v1's residual arm: everything managed is stale and
// the state converges on "no release is due". level none with the flag on
// maintains the placeholder instead. A real level converges on the next
// tag; a lingering placeholder is simply the first claimable draft and
// retags to the real version — which is the whole draft_on_none promise.
//
// Keep selection is v1's planDrafts unchanged: prefer the draft already
// carrying the intended tag, else the first listed (GitHub lists newest
// first); every other managed draft is stale.
func PlanDraft(level gitmoji.Bump, nextTag string, draftOnNone bool, releases []Draft) Plan {
	drafts := managed(releases)

	if level == gitmoji.BumpNone && !draftOnNone {
		if len(drafts) == 0 {
			return Plan{Action: ActionNone}
		}
		return Plan{Action: ActionDelete, Stale: drafts}
	}

	tag := nextTag
	if level == gitmoji.BumpNone {
		tag = PlaceholderTag
	}

	var keep *Draft
	for i := range drafts {
		if keep == nil && drafts[i].TagName == tag {
			keep = &drafts[i]
		}
	}
	if keep == nil && len(drafts) > 0 {
		keep = &drafts[0]
	}
	var stale []Draft
	for i := range drafts {
		if keep == nil || drafts[i].ID != keep.ID {
			stale = append(stale, drafts[i])
		}
	}

	action := ActionCreate
	if keep != nil {
		action = ActionUpdate
	}
	return Plan{Action: action, Tag: tag, Keep: keep, Stale: stale}
}
