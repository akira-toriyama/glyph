package doctor

// This file is the one rule both caller checks judge a pin by (DESIGN §7 has
// the measurements behind it and the alternatives it beat). GitHub starts a
// caller against the reusable at the caller's @ref, while reusableNeeds and
// reusableRequiredInputs mirror the reusables of the tree this binary was
// built from — one object only while the declarations hold still. So each row
// carries After — the newest release whose reusable declared otherwise,
// verified against glyph's own tags by TestCallerDeclarationBoundsMatchReleasedTags
// — and a caller is judged only where this binary can speak for its release.
// Everything else is could-not-run, never a pass and never a fail.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
)

// notJudged is one caller this binary cannot speak for: why, and the remedy
// for that reason.
type notJudged struct {
	Why    string
	Remedy string
}

// judgeAt reports why a caller pinning ref cannot be judged against a row
// whose bound is after, or nil when it can. glyph is the running binary's
// version and floor the release the asking check first shipped in.
//
// Judged means all of: ref is a release tag; it is above after; and, for a
// stamped build, it is not above that build's own release. A plain vX.Y.Z (or
// GoReleaser's X.Y.Z) is the only stamp that names an upper end — `dev`, a Go
// pseudo-version and build.sh's git-describe output (v4.2.0-3-g…) fail
// bump.ParseVersion and trust their own tree. bump.ParseBaseVersion must not
// be used here: it reads the describe output as its base triple v4.2.0, an
// upper end the build does not have, and every pin above v4.2.0 would be
// unknown to a tree built past it.
func judgeAt(ref, after, glyph, floor string) *notJudged {
	if !isReleaseTag(ref) {
		return &notJudged{
			Why: fmt.Sprintf("@%s is not a release tag: a branch moves, and a sha names a commit doctor cannot map "+
				"to a release offline", ref),
			Remedy: "pin a release tag (" + idWorkflowPinned + " names each such ref)",
		}
	}
	pin, _ := bump.ParseVersion(ref)
	if bound, err := bump.ParseVersion(after); after != "" && err == nil && pin.Compare(bound) <= 0 {
		nj := &notJudged{
			Why: fmt.Sprintf("%s is at or below %s, the newest release whose reusable declared otherwise — this glyph "+
				"knows the declarations differ there, not what they were", ref, after),
			Remedy: "move the pin to a release after " + after + " (this glyph judges those)",
		}
		// The pinned release's own doctor is a remedy only from the release
		// the check shipped in: glyph v0.4.0 has no doctor at all and v1.0.0's
		// has no caller checks (D2a review, measured; git grep at the tags,
		// 2026-10-05: workflow-caller-permissions first ships at v2.1.0,
		// workflow-caller-inputs at v3.1.0), so below the floor the offer would
		// send the reader to a check that does not exist.
		if f, ferr := bump.ParseVersion(floor); ferr == nil && pin.Compare(f) >= 0 {
			nj.Remedy += ", or run doctor with glyph " + ref + ", whose tables are lockstep with its own reusables"
		}
		return nj
	}
	if own, err := bump.ParseVersion(glyph); err == nil && pin.Compare(own) > 0 {
		return &notJudged{
			Why:    fmt.Sprintf("%s is newer than this glyph (%s), which cannot know what that release declares", ref, glyph),
			Remedy: "run doctor with glyph " + ref + " or later",
		}
	}
	return nil
}

// unknownReusable is a call into glyph's .github/workflows that names no row:
// a reusable added after this binary was built, or one it never shipped. It
// is unjudged, never skipped — a skip read such a caller as no caller at all
// (DESIGN §7).
func unknownReusable(file string) *notJudged {
	return &notJudged{
		Why:    "this glyph ships no reusable named " + file + "; the pinned release may",
		Remedy: "run doctor with the glyph release the caller pins, or later",
	}
}

// unverifiedCallers fills c's could-not-run arm for a caller check that found
// no defect: callers it could not judge at their pin (skipped, one Details
// line each, with remedies) and files it could not read. It reports false when
// there is neither, and the check passes. death names what goes unverified.
func unverifiedCallers(c *Check, skipped, remedies, unreadable []string, death string) bool {
	if len(skipped) == 0 && len(unreadable) == 0 {
		return false
	}
	c.Status = StatusUnknown
	var gaps, why, fixes []string
	if len(skipped) > 0 {
		gaps = append(gaps, fmt.Sprintf("%d caller(s) not judged at the release they pin", len(skipped)))
		why = append(why, "GitHub starts a caller against the reusable at its pin, not against this binary, and this "+
			"glyph cannot speak for the release those callers pin")
		fixes = append(fixes, remedies...)
	}
	if len(unreadable) > 0 {
		gaps = append(gaps, fmt.Sprintf("%d workflow file(s) could not be read", len(unreadable)))
		why = append(why, "a file doctor cannot read could hold any caller")
		fixes = append(fixes, "fix the file permissions and re-run")
	}
	c.Observed = strings.Join(gaps, "; ") + "; no judged caller is short"
	c.Message = strings.Join(why, ", and ") + " — " + death + " is unverified here, not verified"
	c.Fix = strings.Join(fixes, "; ")
	c.Details = append(append([]string{}, skipped...), unreadable...)
	return true
}

// remember appends s to list unless it is already there — the remedies of
// several skipped callers collapse to one each.
func remember(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// judgedRange renders, for a check's Expected, which pins this binary judges.
func judgedRange(glyph string) string {
	s := "a release-tag pin above the row's bound"
	if own, err := bump.ParseVersion(glyph); err == nil {
		s += " and at or below this glyph's own release (" + own.String() + ")"
	}
	return s
}
