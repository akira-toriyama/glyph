// Package preview renders the merge preview: what merging a pull request does
// to the version, as one Markdown comment body.
//
// The arithmetic and the prose live here, not in the pr-verdict reusable, for
// three reasons. The fold is a VERSION decision — picking the higher of two
// levels in a jq rank table would duplicate in shell the one thing
// internal/bump exists to own.
// The prose makes claims that must stay true everywhere the workflow is
// distributed (it may not name a rolling draft a repo does not keep), and a
// claim that subtle belongs somewhere it can be unit-tested rather than
// eyeballed in a workflow log. And a caller that only has to paste
// `glyph preview --pr N` can be run locally, so the exact comment text is
// verifiable without a push.
//
// The package is pure: no git, no API, no clock. The cli layer resolves the two
// verdicts and hands them over.
package preview

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/markdown"
)

// Marker leads every rendered body. The comment is sticky — a caller finds its
// previous comment by this prefix and edits in place rather than posting a new
// one each push, so the marker is part of the contract, not decoration.
const Marker = "<!-- glyph-pr-verdict -->"

// Commit is one classified commit in the preview table.
type Commit struct {
	Sigil   string
	Level   bump.Level
	Subject string
}

// Verdict is one side of the fold: a classified commit set and the level it
// folds to. Next is the version that level steps to — empty when the level is
// none, because there is no next version to name.
type Verdict struct {
	Level   bump.Level
	Next    string
	Commits []Commit
}

// Input is everything the body is rendered from. PR is this pull request's own
// verdict; Pending is what is already merged but unreleased. Untagged marks a
// base branch whose history holds no v* release tag — the fact the caller
// resolved, stated of the branch because a tag on another branch is no
// release of this one (DESIGN §4). On the single line its Pending is then not
// merely empty but UNCOMPUTED — walking it would cost an API round-trip per
// commit of the whole history for an answer that cannot matter (nothing is
// unreleased when nothing was ever released), so the caller skips it and says
// so here; a packages line hands the two facts over separately
// (Package.PendingWalked). Either way Current is the null version, and the
// body never spells it as a tag (untaggedHeadline).
type Input struct {
	Current  string
	Untagged bool
	// Shallow marks a shallow checkout, the pending walk skipped or run: an
	// Untagged line then has no release tag within the clone's REACH, which
	// is all the clone can say — never "no tag", which a tag past the reach
	// refutes — and its headline names no version, since every version an
	// untagged line could name steps from a null floor the clone never
	// confirmed (t-txed; a --depth 3 clone that reached one line's tag and
	// not another's drew the second's first release, 2026-11-08). The
	// machine verdict beside it still carries the PR side's figure from that
	// floor, so a consumer reads what it always read and the prose claims
	// less than it. PendingShort carries the walk's shallow clause
	// (walk.ShallowShortfall) whether the walk said it or the floor guard
	// did, so the caveat under the headlines is one caveat.
	Shallow bool
	PR      Verdict
	Pending Verdict
	// Notes is the release-notes preview, folded into a <details> block. Empty
	// renders no block — the commits produced no notes sections (nothing
	// release-worthy and no removals to surface).
	Notes string
	// PendingShort names what the pending walk could not read, in the walk's own
	// words; empty when it read the range whole.
	//
	// It exists because this comment makes a POSITIVE claim to a human —
	// "nothing release-worthy is pending since v1.2.3" — and an incomplete walk
	// produces the same empty fold as a range that genuinely holds nothing. The
	// release path fails loud (4) on that ambiguity; here there is nothing to
	// refuse, only something to say. And it must be said HERE rather
	// than left to the ::warning:: the walk already emits: this body is pasted
	// into a pull request and read days later by a reviewer who never opens the
	// workflow log, which is the whole reason the prose lives in a testable
	// package instead of in the caller's jq.
	PendingShort string
	// PRShort names what the PR side could not read: the pull's own commits
	// whose file listing GitHub truncated at its cap or cut short with a 422,
	// in the walk's words, so a line one of them touches in files GitHub did
	// not list may be absent from every figure — and a refusal
	// attribution would have made over such a listing was withheld rather
	// than handed down (DESIGN §4.1). Empty when every listing was whole, and
	// always empty on the single line, which asks for no files. It is said
	// here for PendingShort's reason: the log is not read.
	PRShort string
	// Packages is the per-line fold of a repository that declares
	// [[packages]] (DESIGN §4.1): one entry per line the pull's commits are
	// attributed to, in config order — a line the pull does not touch is not
	// mentioned. When it is non-empty the scalar fields above describe no one
	// line and are ignored, except PendingShort and PRShort, which qualify
	// every line at once (one walk read them all; one listing per commit).
	Packages []Package
	// Participating is how many of the pull's commits participate — the fold
	// reads them: not an exclude_authors author, matched, not claimed by a
	// skip pattern (DESIGN §4.1) — each counted once, and OffLine how many of
	// those sit on no line, and so in no table. Both are the caller's counts,
	// read by the two packages bodies alone (Render with Packages set, and
	// RenderNoLine): the tables cannot give either. A commit moving two lines
	// has a row in each, one sitting on no line has none, and a row carries no
	// sha (DESIGN §4.1, "Participating"). On the single line PR.Commits is the
	// participating set itself, and its footer counts that.
	Participating int
	OffLine       int
}

// Package is one line's fold: its path (the line's name), what its version
// currently is — spelled as the line's TAG, haiku/v0.1.0, so two lines can
// never be confused in one comment — whether the line has released at all,
// and the two verdicts, their Next spelled as tags too.
type Package struct {
	Path     string
	Current  string
	Untagged bool
	// PendingWalked says the pending side below WAS computed for this line.
	// It is independent of Untagged: on the packages path a line with no tag
	// of its own still gets walked whenever another line in the same pull
	// takes the walk to the whole history, and then "no tag yet" must not be
	// rendered as "nothing merged earlier is folded in" (DESIGN §4.1,
	// "Preview").
	PendingWalked bool
	PR            Verdict
	Pending       Verdict
}

// rank is the ONLY test this package applies to a level — never
// `== bump.LevelNone`. bump.Level is a string type whose zero value is "" and
// not "none", so an unset level (a caller that computed no pending verdict, a
// struct built field by field) compares unequal to bump.LevelNone and would
// fall through to the wrong sentence. The order is bump's (Level.Rank); what
// is preview's is that an unknown level ranks with none = nothing moves,
// which is also the only safe direction — a preview that over-claims a bump
// is worse than one that under-claims, because the reviewer checks the table
// against the claim.
func rank(b bump.Level) int {
	return max(0, b.Rank())
}

func icon(b bump.Level) string {
	switch b {
	case bump.LevelPatch:
		return "🔧"
	case bump.LevelMinor:
		return "🔼"
	case bump.LevelMajor:
		return "💥"
	case bump.LevelNone:
		return "⏸️"
	}
	return "⏸️"
}

// headline is the one sentence a reviewer actually reads.
//
// It says "the next release", never "the rolling draft". The arithmetic is
// identical whether that release is a draft release.yml keeps or a tag cut by
// hand, but the DRAFT is not: distributed fleet-wide this comment lands mostly
// on repos that tag straight from main, and naming a draft they do not have
// would not be noise — it would be false. The neutral noun is true in both
// worlds, because a draft's tag IS the next release.
func headline(in Input) string { return headlineOf(in, false) }

// headlineOf is headline for the bare line (line false) and for one line of
// a packages body (line true, renderPackages leading it with the line's
// name). The two differ in one place: what an untagged line's absence is
// said OF. The bare line is the base branch, so "the base branch holds no
// release tag yet" is the fact; a packages line's absence is the line's
// alone, and the base branch may well hold a sibling's tag — the same
// comment's footer named one two paragraphs below the sentence that denied
// it (measured 2026-11-08, haiku/v0.1.0 beside a curry that moved nothing).
func headlineOf(in Input, line bool) string {
	pl, ql := in.PR.Level, in.Pending.Level
	pr, qr := rank(pl), rank(ql)
	if in.Untagged {
		return untaggedHeadline(in, line, pl, ql, pr, qr)
	}
	switch {
	case pr == 0 && qr == 0:
		return fmt.Sprintf("⏸️ Merging this PR moves nothing — and nothing release-worthy is pending since **%s**.", in.Current)
	case pr == 0:
		return fmt.Sprintf("⏸️ This PR does not move the version — the next release stays **%s**.", in.Pending.Next)
	case pr > qr && qr == 0:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the next release becomes **%s → %s**.", icon(pl), pl, in.Current, in.PR.Next)
	case pr > qr && in.PR.Next != "" && in.PR.Next == in.Pending.Next:
		// The level rises but the version does not: on 0.x a major steps the
		// minor (DESIGN §3), so a `!` pull over a pending `^` lands exactly
		// where the pending side already does. The escalates arm below drew
		// that as "v0.4.0 → v0.4.0" (t-d0d9, measured on glyph-monorepo-test
		// #31). "raises major" stays — classification is version-blind and
		// the breakingness must remain visible — and the sentence says the
		// version holds, and why.
		return fmt.Sprintf("%s Merging this PR raises **%s** — the next release stays **%s** (on 0.x a %s steps the minor, and a **%s** bump is already pending).", icon(pl), pl, in.Pending.Next, pl, ql)
	case pr > qr:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the next release escalates **%s → %s**.", icon(pl), pl, in.Pending.Next, in.PR.Next)
	default:
		// qr >= pr >= 1 here, so both levels are real and named.
		return fmt.Sprintf("%s Merging this PR adds **%s**-level changes — the next release stays **%s** (a **%s** bump is already pending).", icon(pl), pl, in.Pending.Next, ql)
	}
}

// untaggedHeadline is headline over a null floor. The line has released
// nothing, so there is no current version to name and no "X → Y": every
// version it names is the line's FIRST release, in one shape, whether the
// pending side was skipped (the single line's floor guard; Pending then
// ranks none) or walked (a packages line a tagged sibling took the walk to
// the whole history for). Drawn through the tagged arms, a walked line with
// no tag read "travel/v0.0.0 → travel/v0.1.0" — a tag that does not exist
// (t-1n1s, measured 2026-11-04 on glyph-monorepo-test-2). The level clauses
// are the tagged arms' own, so the two shapes differ only in how the version
// is named. On a shallow checkout (Input.Shallow) no version is named at all
// (shallowHeadline).
func untaggedHeadline(in Input, line bool, pl, ql bump.Level, pr, qr int) string {
	if in.Shallow {
		return shallowHeadline(line, pl, ql, pr, qr)
	}
	switch {
	case pr == 0 && qr == 0:
		if line {
			return "⏸️ Merging this PR moves nothing — and this line has no release tag yet, so there is no version to move."
		}
		return "⏸️ Merging this PR moves nothing — and the base branch holds no release tag yet, so there is no version to move."
	case pr == 0:
		return fmt.Sprintf("⏸️ This PR does not move the version — the first release here would be **%s** (a **%s** bump is already pending).", in.Pending.Next, ql)
	case qr == 0:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the first release here would be **%s**.", icon(pl), pl, in.PR.Next)
	case pr > qr && in.PR.Next == in.Pending.Next:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the first release here would still be **%s** (on 0.x a %s steps the minor, and a **%s** bump is already pending).", icon(pl), pl, in.Pending.Next, pl, ql)
	case pr > qr:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the first release here would be **%s** (a **%s** bump is already pending).", icon(pl), pl, in.PR.Next, ql)
	default:
		return fmt.Sprintf("%s Merging this PR adds **%s**-level changes — the first release here would be **%s** (a **%s** bump is already pending).", icon(pl), pl, in.Pending.Next, ql)
	}
}

// shallowHeadline is the untagged headline on a shallow checkout: the levels
// the two sides folded to, said as the tagged arms say them, and NO version.
// A line with no release tag within the clone's reach may have one past it,
// and every version an untagged headline names steps from the null floor
// that tag would refute — "the first release here would be **curry/v0.0.1**"
// where curry/v0.1.0 exists past the reach (measured 2026-11-08, a --depth 3
// clone holding haiku's tag and not curry's) — so the sentence says why the
// release cannot be named rather than naming it on a condition. The absence
// is said of the line on a packages body (line), where a sibling's tag may
// be within reach, and of the clone on the bare line. The pending clause is
// what the walk folded over the clone's commits, a floor the caveat under
// the headline already makes of it.
func shallowHeadline(line bool, pl, ql bump.Level, pr, qr int) string {
	var lead string
	switch {
	case pr == 0 && qr == 0:
		lead = "⏸️ Merging this PR moves nothing — and"
	case pr == 0:
		lead = fmt.Sprintf("⏸️ This PR does not move the version (a **%s** bump is already pending) —", ql)
	case qr == 0:
		lead = fmt.Sprintf("%s Merging this PR raises **%s** —", icon(pl), pl)
	case pr > qr:
		lead = fmt.Sprintf("%s Merging this PR raises **%s** (a **%s** bump is already pending) —", icon(pl), pl, ql)
	default:
		lead = fmt.Sprintf("%s Merging this PR adds **%s**-level changes (a **%s** bump is already pending) —", icon(pl), pl, ql)
	}
	where := "no release tag is"
	if line {
		where = "no release tag on this line is"
	}
	return fmt.Sprintf("%s %s within this shallow checkout's reach, so the release it lands in cannot be named here.", lead, where)
}

// escapeCell makes a commit subject safe in a Markdown table cell: it is
// flattened to one line, its structure-injecting constructs are disarmed,
// would-be @mentions are fenced into code spans, and the column separator is
// escaped. Subjects are author-supplied text, and the table is the evidence the
// headline rests on — a subject that breaks the table takes the evidence with
// it. Both passes matter more here than in the notes: this table is a PR
// comment, so a bare "@v1" would not just link a stranger, it would notify them,
// and a raw "</details>" would close the collapsed block glyph wraps the notes
// preview in, promoting attacker-chosen content to the top of a bot's comment.
//
// Both passes model the rendered inline context and not the string this function
// was handed. A table cell is its own inline context (measured: a stray backtick
// in one cell forms no span with a backtick in the next), so each must see the
// cell exactly as it will render at the moment it runs.
// Flatten-then-markup-then-fence is markdown.Line's to enforce, and the measured
// leaks that forced each half of that order are written down on the builder.
//
// The pipe escape is the one pass that is the TABLE's and not the line's, and its
// order against Line is immaterial to the rendering: its backslash lands directly
// in front of a pipe, never in front of an at-sign or a fence, and a pipe escaped
// inside a code span still renders as a pipe. The two orders differ in bytes only
// on a cell shaped like a table delimiter row, which rule 5 escapes on its first
// marker byte: "|---" is "\\|---" this way and "\|---" pipes first, "-|-" is
// "\-\|-" against "-\|-". Measured 2026-11-08 in this table through POST
// /markdown, mode gfm, on eight such pairs ("|---", "-|-", "|:-:|", "-|", ":-|",
// "---|---", "| --- |", "|-"): each pair renders the same HTML, the cell intact
// and the text as the author wrote it. On every other cell the bytes are equal,
// measured the same day on this tree: every cell of up to seven tokens from
// | \ ` @ a space < _ : & (11,111,111 cells) differs in none, and every cell of up
// to six from | \ ` @ a space - # > 1 . ~ * = (8,108,731) differs in 724, each one
// a delimiter-row shape.
func escapeCell(s string) string {
	var l markdown.Line
	l.Prose(s)
	return strings.ReplaceAll(l.String(), "|", `\|`)
}

// Render composes the whole comment body.
func Render(in Input) string {
	if len(in.Packages) > 0 {
		return renderPackages(in)
	}
	var b strings.Builder
	b.WriteString(Marker + "\n")
	b.WriteString(headline(in) + "\n")

	// Directly under the headline, because it QUALIFIES the headline: every
	// sentence headline can produce about the pending side is a claim this walk
	// did not fully earn. Below the table would read as a footnote to the
	// evidence rather than a caveat on the conclusion.
	b.WriteString(pendingShortBlock(in.PendingShort, false, in.Shallow && in.Untagged))

	if len(in.PR.Commits) > 0 {
		b.WriteString("\n" + commitTable(in.PR.Commits))
	}

	b.WriteString(notesDetails(in.Notes))

	b.WriteString("\n" + footer(in) + "\n")
	return b.String()
}

// renderPackages is Render for a repository with lines: one headline per
// touched line — the single line's sentence, led by the line's name, with
// the versions spelled as tags — then one commit table per line (a commit
// that touches two lines sits in both, as it participates in both), the
// notes preview once (its per-line headings are the notes' own), and a
// footer that counts the participating commits, says how many of them no
// table holds, and names every line's base. The marker, the pending-side
// warning, the commit table and the notes block keep the single line's shape
// and producer: the sticky-comment contract and the incomplete-walk caveat do
// not change shape because there are two lines. The warning's words differ in
// grammatical number only — it qualifies one figure there and one per line here.
func renderPackages(in Input) string {
	var b strings.Builder
	b.WriteString(Marker + "\n")
	unreached := false
	for _, p := range in.Packages {
		// A line with no tag renders its first release whether or not its
		// pending side was walked (untaggedHeadline); PendingWalked decides
		// the footer's sentence about what was folded in, not the headline.
		fmt.Fprintf(&b, "**%s** — %s\n", p.Path, headlineOf(Input{Current: p.Current, Untagged: p.Untagged, Shallow: in.Shallow, PR: p.PR, Pending: p.Pending}, true))
		unreached = unreached || (in.Shallow && p.Untagged)
	}
	b.WriteString(pendingShortBlock(in.PendingShort, true, unreached))
	b.WriteString(prShortBlock(in.PRShort, true, unreached))
	for _, p := range in.Packages {
		if len(p.PR.Commits) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", p.Path)
		b.WriteString(commitTable(p.PR.Commits))
	}
	b.WriteString(notesDetails(in.Notes))
	b.WriteString("\n" + packagesFooter(in) + "\n")
	return b.String()
}

// RenderNoLine is the body for a pull, in a repository that declares
// [[packages]], whose commits sit on no line: nothing moves, so there is no
// headline per line, no table and no base to name. It reads PRShort,
// Participating and nothing else of the Input.
//
// The sentence carries no count: the count is the footer's alone, and the
// sentence is about every participating commit (DESIGN §4.1, "Participating"
// and "Preview", holds the miscounts a count here would read). With the PR
// side short it claims no more than the files GitHub listed, and the caveat
// makes a floor of it.
//
// The caller picks this over Render: an Input with no Packages is the single
// line's to Render, and nothing in it says a repository declares lines.
func RenderNoLine(in Input) string {
	nothing := "⏸️ Merging this PR moves nothing — no commit participating in it touches a declared package."
	if in.PRShort != "" {
		nothing = "⏸️ Merging this PR moves nothing in the files GitHub listed — no commit participating in it touches a declared package there."
	}
	return Marker + "\n" + nothing + "\n" + prShortBlock(in.PRShort, false, false) + "\n" + packagesFooter(in) + "\n"
}

// pendingShortBlock is the caveat Render and renderPackages place directly
// under their headlines when the pending side's walk came up short, and ""
// when it did not. plural says the body carries one headline per line rather
// than one; unreached says a headline above names no figure because its
// line has no release tag within a shallow checkout's reach
// (shallowHeadline), so the caveat qualifies what the headline SAYS — its
// level — and names the second thing that may lie past the reach, the tag it
// would step from. Pointed at "the figure above" there, it qualified a
// figure no headline carried (measured 2026-11-08, a `=` pull on a --depth 1
// clone: "moves nothing … there is no version to move" over "may be missing
// from the figure above").
func pendingShortBlock(short string, plural, unreached bool) string {
	if short == "" {
		return ""
	}
	floor := "the figure above, so treat it"
	if plural {
		floor = "the figures above, so treat each"
	}
	if unreached {
		floor = "what each headline says, and so may the release tag a line with none within reach would step from, so treat each"
		if !plural {
			floor = "what the headline says, and so may the release tag it would step from, so treat it"
		}
	}
	return fmt.Sprintf("\n> [!WARNING]\n> The pending side of this fold is INCOMPLETE: %s. Anything already merged but unreleased may be missing from %s as a floor rather than the answer.\n", short, floor)
}

// commitTable is the evidence table under a headline: the header, then one
// row per commit with its subject made safe for a cell by escapeCell.
func commitTable(commits []Commit) string {
	var b strings.Builder
	b.WriteString("| commit | sigil | bump |\n|---|---|---|\n")
	for _, c := range commits {
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n", escapeCell(c.Subject), c.Sigil, c.Level)
	}
	return b.String()
}

// notesDetails is the collapsed release-notes preview, and "" for no notes.
func notesDetails(notes string) string {
	if notes == "" {
		return ""
	}
	return "\n<details>\n<summary>Release notes preview</summary>\n\n" + strings.TrimRight(notes, "\n") + "\n\n</details>\n"
}

// prShortBlock is the caveat both packages bodies place under their headline
// when PRShort is set, and "" when it is not: a pull whose one unlisted
// commit was attributed to no line is exactly the body that must carry it.
// figures says whether the body carries figures for the caveat to
// make floors of: the headlines do, the "moves nothing" sentence does not,
// and a caveat pointing at "the figures above" there points at nothing.
// unreached is pendingShortBlock's: a headline above names no figure, its
// line having no release tag within a shallow checkout's reach, so the
// caveat qualifies what each headline says — "the figures above" there
// pointed at figures no headline carried (measured 2026-11-09, a --depth 3
// clone and a pull commit whose listing GitHub answered 422 for). The
// sentence names no cause — PRShort already names the cause, and GitHub's 422
// cuts a listing short below the cap too (t-esm5) — and says a line MAY be
// missing, the walk's own word: a scope can carry a commit onto the very line
// its unlisted files touch.
func prShortBlock(short string, figures, unreached bool) string {
	if short == "" {
		return ""
	}
	floor := "may be missing from the figures above, so treat each as a floor rather than the answer"
	switch {
	case !figures:
		floor = `may move all the same, so treat "moves nothing" as a floor rather than the answer`
	case unreached:
		floor = "may be missing from what each headline says, so treat each as a floor rather than the answer"
	}
	return fmt.Sprintf("\n> [!WARNING]\n> This PR's own side of this fold is INCOMPLETE: %s. A line one of those commits touches in files GitHub did not list %s.\n", short, floor)
}

// packagesFooter is footer for both packages bodies: the participating
// commits, counted by the caller (Input.Participating) — the single line's
// set, so one pull reads one number whichever body it gets — and the base
// every line was folded since, or the line said to have no release tag on
// the base branch yet.
//
// Beside tables it also says how many of those commits sit on no line. They
// are in no table, so without it a reader who counts the rows above finds
// fewer than the footer claims: the miscount footer's wording exists to
// avoid. It is a sentence of its own, after the one that names the bases:
// spliced in ahead of "squash-safe, a squash-merge cannot erase them", that
// clause would read as said of the commits on no line alone. A body with no
// line has no table to disagree with, and its one sentence has already said
// that no participating commit touches a package.
func packagesFooter(in Input) string {
	var bases, untagged, untaggedWalked []string
	for _, p := range in.Packages {
		if p.Untagged {
			if p.PendingWalked {
				untaggedWalked = append(untaggedWalked, p.Path)
			} else {
				untagged = append(untagged, p.Path)
			}
			continue
		}
		bases = append(bases, fmt.Sprintf("**%s** (%s)", p.Current, p.Path))
	}
	s := fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them", in.Participating)
	if len(bases) > 0 {
		how := "with what is already merged on the base branch"
		if in.PendingShort != "" {
			how = "with as much of what is already merged on the base branch as the walk could read"
		}
		s += fmt.Sprintf(" — folded, per line, %s since %s", how, strings.Join(bases, ", "))
	}
	s += "."
	if len(in.Packages) > 0 && in.OffLine > 0 {
		sit := "sit"
		if in.OffLine == 1 {
			sit = "sits"
		}
		s += fmt.Sprintf(" %d of them %s on no line.", in.OffLine, sit)
	}
	// The absence is said of the clone's reach on a shallow checkout: a tag
	// past it is no less a tag, and the walked sentence's "everything merged
	// so far" is then everything the clone holds.
	where, soFar := "on the base branch yet", "everything merged so far"
	if in.Shallow {
		where, soFar = "within this shallow checkout's reach", "everything merged within that reach"
	}
	if len(untaggedWalked) > 0 {
		subject, have, them := lines(untaggedWalked)
		s += fmt.Sprintf(" %s %s no release tag %s, so %s is folded in for %s.", subject, have, where, soFar, them)
	}
	if len(untagged) > 0 {
		subject, have, them := lines(untagged)
		s += fmt.Sprintf(" %s %s no release tag %s, so nothing merged earlier is folded in for %s.", subject, have, where, them)
	}
	return s + " Pushing more commits updates this comment."
}

// lines is the subject of a footer sentence about several lines, with the
// verb and the pronoun that agree with it: "haiku has … for it", "haiku and
// curry have … for them", "a, b and c" past two. Joined with " and " alone,
// two lines read "haiku and curry has … for it" (t-c2a9).
func lines(paths []string) (subject, have, them string) {
	switch len(paths) {
	case 1:
		return paths[0], "has", "it"
	case 2:
		return paths[0] + " and " + paths[1], "have", "them"
	default:
		return strings.Join(paths[:len(paths)-1], ", ") + " and " + paths[len(paths)-1], "have", "them"
	}
}

func footer(in Input) string {
	n := len(in.PR.Commits)
	// "participate" rather than "are in": bots and skip-pattern commits (merges,
	// under the presets) are excluded upstream, so a bot's PR legitimately shows
	// zero here and the wording must not read as a miscount. PR.Commits is the
	// fold's rows, the participating set (DESIGN §4.1) — a raw revert among
	// them: the presets' revert pattern claims it, at ~.
	if in.Untagged && in.Shallow {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. No v* release tag is within this shallow checkout's reach, so nothing merged earlier is folded in. Pushing more commits updates this comment.", n)
	}
	if in.Untagged {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. The base branch holds no v* release tag yet, so nothing merged earlier is folded in. Pushing more commits updates this comment.", n)
	}
	if in.PendingShort != "" {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with as much of what is already merged on the base branch since **%s** as the walk could read. Pushing more commits updates this comment.", n, in.Current)
	}
	return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with what is already merged on the base branch since **%s**. Pushing more commits updates this comment.", n, in.Current)
}
