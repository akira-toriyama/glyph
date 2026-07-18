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

	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// Marker leads every rendered body. The comment is sticky — a caller finds its
// previous comment by this prefix and edits in place rather than posting a new
// one each push, so the marker is part of the contract, not decoration.
const Marker = "<!-- glyph-pr-verdict -->"

// Commit is one classified commit in the preview table.
type Commit struct {
	Code     string
	Level    gitmoji.Bump
	Breaking bool
	Subject  string
}

// Verdict is one side of the fold: a classified commit set and the level it
// folds to. Next is the version that level steps to — empty when the level is
// none, because there is no next version to name.
type Verdict struct {
	Level   gitmoji.Bump
	Next    string
	Commits []Commit
}

// Input is everything the body is rendered from. PR is this pull request's own
// verdict; Pending is what is already merged but unreleased. Untagged marks a
// repository with no v* release tag: its Pending is not merely empty but
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
}

// rank orders levels for the fold, and is the ONLY test this package applies to
// a level — never `== BumpNone`. Bump is a string type whose zero value is ""
// and not "none", so an unset level (a caller that computed no pending verdict,
// a struct built field by field) compares unequal to BumpNone and would fall
// through to the wrong sentence. Ranking is total: anything unrecognized ranks
// 0 = nothing moves, which is also the only safe direction — a preview that
// over-claims a bump is worse than one that under-claims, because the reviewer
// checks the table against the claim.
func rank(b gitmoji.Bump) int {
	switch b {
	case gitmoji.BumpPatch:
		return 1
	case gitmoji.BumpMinor:
		return 2
	case gitmoji.BumpMajor:
		return 3
	case gitmoji.BumpNone:
		return 0
	}
	return 0 // an unset or unrecognized level: nothing moves
}

func icon(b gitmoji.Bump) string {
	switch b {
	case gitmoji.BumpPatch:
		return "🔧"
	case gitmoji.BumpMinor:
		return "🔼"
	case gitmoji.BumpMajor:
		return "💥"
	case gitmoji.BumpNone:
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
		return "⏸️ Merging this PR moves nothing — and this repository has no release tag yet, so there is no version to move."
	case in.Untagged:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the first release here would be **%s**.", icon(pl), pl, in.PR.Next)
	case pr == 0 && qr == 0:
		return fmt.Sprintf("⏸️ Merging this PR moves nothing — and nothing release-worthy is pending since **%s**.", in.Current)
	case pr == 0:
		return fmt.Sprintf("⏸️ This PR does not move the version — the next release stays **%s**.", in.Pending.Next)
	case pr > qr && qr == 0:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the next release becomes **%s → %s**.", icon(pl), pl, in.Current, in.PR.Next)
	case pr > qr:
		return fmt.Sprintf("%s Merging this PR raises **%s** — the next release escalates **%s → %s**.", icon(pl), pl, in.Pending.Next, in.PR.Next)
	default:
		// qr >= pr >= 1 here, so both levels are real and named.
		return fmt.Sprintf("%s Merging this PR adds **%s**-level changes — the next release stays **%s** (a **%s** bump is already pending).", icon(pl), pl, in.Pending.Next, ql)
	}
}

// escapeCell makes a commit subject safe in a Markdown table cell: the column
// separator is escaped and any newline flattened. Subjects are author-supplied
// text, and the table is the evidence the headline rests on — a subject that
// breaks the table takes the evidence with it.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}

// Render composes the whole comment body.
func Render(in Input) string {
	var b strings.Builder
	b.WriteString(Marker + "\n")
	b.WriteString(Headline(in) + "\n")

	if len(in.PR.Commits) > 0 {
		b.WriteString("\n| commit | code | bump |\n|---|---|---|\n")
		for _, c := range in.PR.Commits {
			breaking := ""
			if c.Breaking {
				breaking = " 💥"
			}
			fmt.Fprintf(&b, "| %s | %s `%s` | %s%s |\n", escapeCell(c.Subject), c.Code, c.Code, c.Level, breaking)
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

func footer(in Input) string {
	n := len(in.PR.Commits)
	// "participate" rather than "are in": bots, merges, autosquash artifacts and
	// raw reverts are excluded upstream, so a bot's PR legitimately shows zero
	// here and the wording must not read as a miscount.
	if in.Untagged {
		return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. This repository has no v* release tag yet, so nothing merged earlier is folded in. Pushing more commits updates this comment.", n)
	}
	return fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with what is already merged on the base branch since **%s**. Pushing more commits updates this comment.", n, in.Current)
}
