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
//
// The judging skeleton both checks run is here too (judgeCallers); each check
// is a callerSpec of what differs.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
)

// callerRow is a row of either caller table: bound is its After.
type callerRow interface{ bound() string }

func (r permRow) bound() string  { return r.After }
func (r inputRow) bound() string { return r.After }

// callerSpec is what one caller check judges with; judgeCallers holds the
// rest. The fail and pass Observed strings are formats: failObserved takes
// the finding count and the caller-file count, passObserved the caller-file
// count.
type callerSpec[R callerRow] struct {
	id, expected string
	// consequence is the check's sentence about what an unlisted workflows
	// directory leaves unverified (unlistableWorkflows).
	consequence string
	glyph       string // the running binary's version (judgeAt)
	floor       string // the release the check first shipped in (judgeAt)
	// rows is the check's table, keyed by the reusable's lower-cased file name.
	rows map[string]R
	// file prepares one caller file read whole and returns the judge of each
	// call it makes, which answers that call's finding lines — or nil when the
	// file is not judged at all. A nil comes before any pin is judged, so an
	// exempt file is never could-not-run over its pins.
	file         func(f workflowFile) func(call reusableCall, row R) []string
	failObserved string
	failMessage  string
	failFix      string
	death        string // what goes unverified when a caller is not judged (unverifiedCallers)
	noCaller     string // the pass Observed when no workflow calls a glyph reusable
	passObserved string
	passMessage  string
}

// judgeCallers is the one skeleton of both caller checks: every workflow file
// read whole, every call into a glyph reusable judged at its pin (judgeAt) or
// named as not judged, and the verdict — a finding outranks a caller not
// judged or a file not read, and either of those outranks a pass.
func judgeCallers[R callerRow](root string, rootVerified bool, workflows []workflowFile, workflowsErr error, spec callerSpec[R]) Check {
	c := Check{ID: spec.id, Expected: spec.expected}
	if workflowsErr != nil {
		unlistableWorkflows(&c, root, rootVerified, workflowsErr, spec.consequence, ". "+spec.consequence)
		return c
	}

	callers := 0
	var findings, skipped, remedies, unreadable []string
	for _, f := range workflows {
		if f.Err != nil {
			// Not left to the pin check: its unknown answers whether a ref is
			// concrete, not whether a caller starts, and skipping the file here
			// turned chmod 000 on a failing caller into this check's "nothing
			// to judge" pass (t-fdd8 (3)).
			unreadable = append(unreadable, f.unreadableLine())
			continue
		}
		calls := reusablesCalled(f.Name, f.Body)
		if len(calls) == 0 {
			continue
		}
		callers++
		judge := spec.file(f)
		if judge == nil {
			continue
		}
		for _, call := range calls {
			row, known := spec.rows[strings.ToLower(call.Reusable)]
			nj := unknownReusable(call.Reusable)
			if known {
				nj = judgeAt(call.Ref, row.bound(), spec.glyph, spec.floor)
			}
			if nj != nil {
				skipped = append(skipped, fmt.Sprintf(".github/workflows/%s calls %s@%s — not judged: %s", f.Name, call.Reusable, call.Ref, nj.Why))
				remedies = remember(remedies, nj.Remedy)
				continue
			}
			findings = append(findings, judge(call, row)...)
		}
	}
	sort.Strings(findings)

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf(spec.failObserved, len(findings), callers)
		c.Details = slices.Concat(findings, skipped, unreadable)
		c.Message = spec.failMessage
		c.Fix = spec.failFix
		return c
	}
	if unverifiedCallers(&c, skipped, remedies, unreadable, spec.death) {
		return c
	}
	c.Status = StatusPass
	if callers == 0 {
		c.Observed = spec.noCaller
		c.Message = "nothing to judge, observed — not assumed"
		return c
	}
	c.Observed = fmt.Sprintf(spec.passObserved, callers)
	c.Message = spec.passMessage
	return c
}

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
// bump.ParseVersion and trust their own tree. bump.ParseBaseVersionOn must not
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
		// has no caller checks (measured with git grep at the tags,
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
