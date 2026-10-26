package walk

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// unclaimed judges a walked commit against the pattern file — a message that
// yields no sigil, or one no pattern (or an unlandable one) claims — and
// returns it as the refusal's details row ({sha, subject, detail}, the shape
// every refusal of the gate code carries). ok is false when the commit is
// claimed. Both arms of the walk ask it, the pull's listing and the released
// branch's own commit, so the two can never word the same commit differently.
func unclaimed(cfg *config.Config, c gitsource.RawCommit) (row bump.Refusal, ok bool) {
	m, merr := cfg.Match(c.Message)
	detail := ""
	switch {
	case merr != nil:
		detail = merr.Error()
	case !m.Matched:
		detail = cfg.UnclaimedDetail(m)
	default:
		return bump.Refusal{}, false
	}
	return bump.Refusal{SHA: c.SHA, Subject: bump.FirstLine(c.Message), Detail: detail}, true
}

// wedge is one commit the release walk refused — inside a merged pull the
// walk resolved, or on the released branch itself — with where it sits and
// the base that clears it. Such a failure stays hard (Q1; glossary, "wedge"):
// published history is immutable, so the same commit wedges every release
// until the walk starts past it, and a bare per-commit lint line names
// neither the pull nor a base. WHERE THE ESCAPE POINTS is the footprint
// rule's other half (DESIGN §4, "Which base clears it"): a commit the walk
// sees on the released branch (onMain — a merge- or rebase-merged pull's, a
// direct push) is cleared by a base at or past itself, the nearest escape;
// a squash-merged pull's inner commit exists only over the API, so only a
// base at or past its merge point stops the walk re-reading the listing. A
// commit two pulls list has one escape per pull.
type wedge struct {
	row     bump.Refusal
	where   string
	escapes []escape
}

// escape is one base that clears a wedge: the sha, why that base is the one,
// and the pull the commit was refused inside ("" for a direct push).
type escape struct {
	base, why, via string
}

// newWedge places a refused commit: number is the pull the walk resolved it
// from (0 for a commit the API resolves to no pull), mergePoint that pull's
// merge point, and offending the commit's own sha when it is on the released
// branch (onMain), "" when it lives only in the pull's listing.
func newWedge(row bump.Refusal, owner, repo string, number int, mergePoint, offending string) wedge {
	e := placeEscape(owner, repo, number, mergePoint, offending)
	where := "pushed straight to the released branch (no merged pull request resolves it)"
	if number > 0 {
		where = fmt.Sprintf("inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s", owner, repo, number, mergePoint)
	}
	return wedge{row: row, where: where, escapes: []escape{e}}
}

// placeEscape is the base that clears one refusal, by the shape newWedge
// describes.
func placeEscape(owner, repo string, number int, mergePoint, offending string) escape {
	switch {
	case number == 0:
		return escape{base: offending, why: "the commit is on the released branch itself"}
	case offending != "":
		// Stated as the fact the walk measured (the commit is on the
		// branch and in range), not as the merge method it implies: a
		// squash pull's listing can hold a commit that also sits on main.
		return escape{base: offending, why: "the offending commit is itself on the released branch, and the walk no longer folds back a listed commit that landed outside the range", via: fmt.Sprintf("%s/%s#%d", owner, repo, number)}
	default:
		return escape{base: mergePoint, why: "the pull request was squash-merged, so its commits exist only over the API and re-reading the listing is the only way the walk sees them at all; its ONE commit on the released branch is that merge point", via: fmt.Sprintf("%s/%s#%d", owner, repo, number)}
	}
}

// walkRefusal is the walk's one refusal, built once the whole range is
// walked: the first refused commit's sentence and where it sits, the count
// of the rest, every refused commit in details, and every distinct escape —
// a base must clear all of them, and a pull's merge point is in no details
// row (the row names the inner commit, which a squash put on no branch), so
// the sentence is the only place a reader learns it. With one escape the
// sentence is the single-commit one; with several, each escape names the
// commit and pull it clears.
//
// Under [[packages]] a refused commit wedges every line whose range holds
// it, so the base clears it only as a tag on each such line, and the
// sentence names that tag per line (lineTags) where the single line names
// one tag. "Cut a release tag there" cleared no line: a bare tag is no
// line's, and one line's tag left the other wedged (t-wjbs, measured
// 2026-10-26; DESIGN §4.1).
func (w *walk) walkRefusal() error {
	first := w.wedges[0]
	more := ""
	if n := len(w.wedges) - 1; n > 0 {
		more = fmt.Sprintf(" (+%d more refused commit(s), all in details)", n)
	}
	rows := make([]bump.Refusal, 0, len(w.wedges))
	var escapes []string
	named := map[string]bool{}
	held := make(lineBases, len(w.ranges.lines))
	for _, wd := range w.wedges {
		rows = append(rows, wd.row)
		for _, e := range wd.escapes {
			if named[e.base] {
				continue
			}
			named[e.base] = true
			held.add(e.base, w.ranges.reach(e.base))
			of := ""
			if e.via != "" {
				of = " of " + e.via
			}
			escapes = append(escapes, fmt.Sprintf("%.7s (commit %.7s%s) — %s", e.base, wd.row.SHA, of, e.why))
		}
	}
	there := "at or past all of them"
	if len(escapes) == 1 {
		escapes[0] = fmt.Sprintf("%.7s — %s", first.escapes[0].base, first.escapes[0].why)
		there = "there"
	}
	wedges := "every release wedges here until the walk starts past it"
	cut := fmt.Sprintf("Cut a release tag %s by hand, or name such a tag with an explicit --since-tag=TAG (DESIGN §4)", there)
	if w.ranges.holds != nil {
		wedges = "every release of a line whose range holds it wedges here until that line's walk starts past it"
		cut = fmt.Sprintf("Cut %s by hand, or name such a tag with --since-tag=<prefix>vX.Y.Z (DESIGN §4.1)", lineTags(w.ranges.lines, held))
	}
	return core.Lintf(
		"commit %.7s: %s%s — %s; the commit is already on a published branch and cannot be rewritten, so %s. The walk must start AT OR PAST %s. %s",
		first.row.SHA, first.row.Detail, more, first.where, wedges, strings.Join(escapes, ", and AT OR PAST "), cut).WithDetails(rows)
}

// Outranks reports whether the refusal already found (found says one was) is
// the verdict over err, a failure to read the rest of the range, and says the
// failure on stderr when it is. The range holds a commit that wedges it
// whatever else it holds, and a walk that stopped at the first refusal never
// read past it either, so a 4 here would hide a verdict the operator must act
// on behind a failure that clears on retry. What was not read may hold more
// refused commits than the sentence counts. An interrupt carries out as
// itself. The walk asks it over its wedges, and the partition and preview's
// pull side over attribution's refusals, which each collects past the first.
func (w Walker) Outranks(found bool, err error) bool {
	if !found || core.IsInterrupted(err) {
		return false
	}
	w.Report.Warnf("the rest of the range could not be read (%v) — the refusal below is the verdict, and what was not read may hold more refused commits than it counts", err)
	return true
}

// onMain reports the sha a wedge escape can be cut at: the commit itself when
// the walk can see it on the released branch, else "" for a commit that lives
// only inside a pull request's API listing. By the time walk.expand asks, a listed
// commit that landed outside the range has already been dropped, so being in
// range is exactly the same question as having a landing site at all;
// walk.fallback's commit is the range's own and always answers itself.
func onMain(sha string, inRange map[string]bool) string {
	if sha != "" && inRange[sha] {
		return sha
	}
	return ""
}
