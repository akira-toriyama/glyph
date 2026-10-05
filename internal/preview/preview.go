// Package preview renders the merge preview: what merging a pull request does
// to the version, as one Markdown comment body.
//
// This is the arithmetic and the prose that used to live as ~60 lines of jq
// inside the pr-verdict reusable. It moved here for three reasons. The fold is
// a VERSION decision — the caller was picking the higher of two levels in a jq
// rank table, duplicating in shell the one thing internal/bump exists to own.
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

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/markdown"
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
// release of this one (DESIGN §4): its Pending is not merely empty but
// UNCOMPUTED — walking it would cost an API round-trip per commit of the whole
// history for an answer that cannot matter (nothing is unreleased when nothing
// was ever released), so the caller skips it and says so here.
type Input struct {
	Current  string
	Untagged bool
	PR       Verdict
	Pending  Verdict
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
	// sha, so the first cut's count of distinct sigil-and-subject pairs made
	// one commit of two that share a subject — "2 commit(s)" under a table of
	// three rows, for a pull the single line counts 4 (t-rrw0 (1);
	// TestPreviewPackagesCountsWhatTheFoldReads fails so on that source). On
	// the single line PR.Commits is the participating set itself, and its
	// footer counts that.
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
	// rendered as "nothing merged earlier is folded in" — the same run's
	// machine verdict says otherwise, and the release walk agrees with the
	// machine verdict (t-60dc, measured 2026-09-15: body curry/v0.0.1 vs JSON
	// and `bump` curry/v0.1.0).
	PendingWalked bool
	PR            Verdict
	Pending       Verdict
}

// rank orders levels for the fold, and is the ONLY test this package applies to
// a level — never `== bump.LevelNone`. bump.Level is a string type whose zero
// value is "" and not "none", so an unset level (a caller that computed no
// pending verdict, a struct built field by field) compares unequal to
// bump.LevelNone and would fall through to the wrong sentence. Ranking is
// total: anything unrecognized ranks 0 = nothing moves, which is also the only
// safe direction — a preview that over-claims a bump is worse than one that
// under-claims, because the reviewer checks the table against the claim.
func rank(b bump.Level) int {
	switch b {
	case bump.LevelPatch:
		return 1
	case bump.LevelMinor:
		return 2
	case bump.LevelMajor:
		return 3
	case bump.LevelNone:
		return 0
	}
	return 0 // an unset or unrecognized level: nothing moves
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

// Headline is the one sentence a reviewer actually reads.
//
// It says "the next release", never "the rolling draft". The arithmetic is
// identical whether that release is a draft release.yml keeps or a tag cut by
// hand, but the DRAFT is not: distributed fleet-wide this comment lands mostly
// on repos that tag straight from main, and naming a draft they do not have
// would not be noise — it would be false. The neutral noun is true in both
// worlds, because a draft's tag IS the next release.
func Headline(in Input) string {
	pl, ql := in.PR.Level, in.Pending.Level
	pr, qr := rank(pl), rank(ql)
	switch {
	case in.Untagged && pr == 0:
		return "⏸️ Merging this PR moves nothing — and the base branch holds no release tag yet, so there is no version to move."
	case in.Untagged:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the first release here would be **%s**.", icon(pl), pl, in.PR.Next)
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
// THE ORDER IS THE SAFETY PROPERTY, because both passes model the rendered
// inline context and not the string this function was handed. A table cell is
// its own inline context (measured: a stray backtick in one cell forms no span
// with a backtick in the next), so each pass must see the cell EXACTLY as it
// will render at the moment it runs. Flatten-then-markup-then-fence is
// markdown.Line's to enforce now (t-3f4s), and the measured leaks that forced
// each half of that order are written down on the builder; this function keeps
// the one pass that is the TABLE's and not the line's:
//
//   - pipe escaping comes last, after the fence. It adds backslashes, and an
//     escaping backslash in front of a fence swallows it — the fence goes out
//     of its way to write a separating space where the author put one there. A
//     pipe escaped inside a code span still renders as a pipe, so nothing is
//     lost by doing it after.
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
	b.WriteString(Headline(in) + "\n")

	// Directly under the headline, because it QUALIFIES the headline: every
	// sentence Headline can produce about the pending side is a claim this walk
	// did not fully earn. Below the table would read as a footnote to the
	// evidence rather than a caveat on the conclusion.
	if in.PendingShort != "" {
		fmt.Fprintf(&b, "\n> [!WARNING]\n> The pending side of this fold is INCOMPLETE: %s. Anything already merged but unreleased may be missing from the figure above, so treat it as a floor rather than the answer.\n", in.PendingShort)
	}

	if len(in.PR.Commits) > 0 {
		b.WriteString("\n| commit | sigil | bump |\n|---|---|---|\n")
		for _, c := range in.PR.Commits {
			fmt.Fprintf(&b, "| %s | `%s` | %s |\n", escapeCell(c.Subject), c.Sigil, c.Level)
		}
	}

	if in.Notes != "" {
		b.WriteString("\n<details>\n<summary>Release notes preview</summary>\n\n")
		b.WriteString(strings.TrimRight(in.Notes, "\n"))
		b.WriteString("\n\n</details>\n")
	}

	b.WriteString("\n" + footer(in) + "\n")
	return b.String()
}

// renderPackages is Render for a repository with lines: one headline per
// touched line — the single line's sentence, led by the line's name, with
// the versions spelled as tags — then one commit table per line (a commit
// that touches two lines sits in both, as it participates in both), the
// notes preview once (its per-line headings are the notes' own), and a
// footer that counts the participating commits, says how many of them no
// table holds, and names every line's base. The marker and the warning block are
// the single line's, byte for byte: the sticky-comment contract and the
// incomplete-walk caveat do not change shape because there are two lines.
func renderPackages(in Input) string {
	var b strings.Builder
	b.WriteString(Marker + "\n")
	for _, p := range in.Packages {
		// Untagged reaches Headline only when the pending side really is
		// uncomputed — its documented meaning. A walked line with no tag
		// renders like any other: its floor is the null version.
		fmt.Fprintf(&b, "**%s** — %s\n", p.Path, Headline(Input{Current: p.Current, Untagged: p.Untagged && !p.PendingWalked, PR: p.PR, Pending: p.Pending}))
	}
	if in.PendingShort != "" {
		fmt.Fprintf(&b, "\n> [!WARNING]\n> The pending side of this fold is INCOMPLETE: %s. Anything already merged but unreleased may be missing from the figures above, so treat each as a floor rather than the answer.\n", in.PendingShort)
	}
	b.WriteString(prShortBlock(in.PRShort, true))
	for _, p := range in.Packages {
		if len(p.PR.Commits) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n| commit | sigil | bump |\n|---|---|---|\n", p.Path)
		for _, c := range p.PR.Commits {
			fmt.Fprintf(&b, "| %s | `%s` | %s |\n", escapeCell(c.Subject), c.Sigil, c.Level)
		}
	}
	if in.Notes != "" {
		b.WriteString("\n<details>\n<summary>Release notes preview</summary>\n\n")
		b.WriteString(strings.TrimRight(in.Notes, "\n"))
		b.WriteString("\n\n</details>\n")
	}
	b.WriteString("\n" + packagesFooter(in) + "\n")
	return b.String()
}

// RenderNoLine is the body for a pull, in a repository that declares
// [[packages]], whose commits sit on no line: nothing moves, so there is no
// headline per line, no table and no base to name. It reads PRShort,
// Participating and nothing else of the Input.
//
// The sentence carries no count. It did — "its N commit(s) touch no declared
// package", N the raw listing — and said that of a skipped merge commit whose
// diff nothing reads, above a footer repeating the same N as
// "participating": 3 for a pull of a shared =, a bot and a merge
// commit, which the single line counts 1 (t-rrw0 (2)+(3)). With the
// participating count in its place it would read "its 1 commit(s)" in that
// pull of three, and "its 0 commit(s)" in a pull of a bot and a merge — the
// miscount reading footer's wording exists to avoid. So the count is the
// footer's alone, and the sentence is about every participating commit. With
// the PR side short it claims no more than the files GitHub listed, and the
// caveat makes a floor of it.
//
// The caller picks this over Render: an Input with no Packages is the single
// line's to Render, and nothing in it says a repository declares lines.
func RenderNoLine(in Input) string {
	nothing := "⏸️ Merging this PR moves nothing — no commit participating in it touches a declared package."
	if in.PRShort != "" {
		nothing = "⏸️ Merging this PR moves nothing in the files GitHub listed — no commit participating in it touches a declared package there."
	}
	return Marker + "\n" + nothing + "\n" + prShortBlock(in.PRShort, false) + "\n" + packagesFooter(in) + "\n"
}

// prShortBlock is the caveat both packages bodies place under their headline
// when PRShort is set, and "" when it is not: a pull whose one unlisted
// commit was attributed to no line is exactly the body that must carry it.
// figures says whether the body carries figures for the caveat to
// make floors of: the headlines do, the "moves nothing" sentence does not,
// and a caveat pointing at "the figures above" there points at nothing. The
// sentence names no cause — PRShort already does, and "only past the cap"
// was false for a listing GitHub answered 422 for (t-esm5) — and says a line
// MAY be missing, the walk's own word: a scope can carry a commit onto the
// very line its unlisted files touch.
func prShortBlock(short string, figures bool) string {
	if short == "" {
		return ""
	}
	floor := "may be missing from the figures above, so treat each as a floor rather than the answer"
	if !figures {
		floor = `may move all the same, so treat "moves nothing" as a floor rather than the answer`
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
	if len(untaggedWalked) > 0 {
		s += fmt.Sprintf(" %s has no release tag on the base branch yet, so everything merged so far is folded in for it.", strings.Join(untaggedWalked, " and "))
	}
	if len(untagged) > 0 {
		s += fmt.Sprintf(" %s has no release tag on the base branch yet, so nothing merged earlier is folded in for it.", strings.Join(untagged, " and "))
	}
	return s + " Pushing more commits updates this comment."
}

func footer(in Input) string {
	n := len(in.PR.Commits)
	// "participate" rather than "are in": bots and skip-pattern commits (merges,
	// under the presets) are excluded upstream, so a bot's PR legitimately shows
	// zero here and the wording must not read as a miscount. PR.Commits is the
	// fold's rows, the participating set (DESIGN §4.1) — a raw revert among
	// them: the presets' revert pattern claims it, at ~.
	if in.Untagged {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. The base branch holds no v* release tag yet, so nothing merged earlier is folded in. Pushing more commits updates this comment.", n)
	}
	if in.PendingShort != "" {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with as much of what is already merged on the base branch since **%s** as the walk could read. Pushing more commits updates this comment.", n, in.Current)
	}
	return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with what is already merged on the base branch since **%s**. Pushing more commits updates this comment.", n, in.Current)
}
