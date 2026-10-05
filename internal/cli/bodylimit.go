package cli

import (
	"strings"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/v4/internal/core"
)

// GitHub caps the two write surfaces glyph composes bodies for, the caps are
// CHARACTER counts (runes) and not bytes, and the two surfaces get opposite
// degradations. Both numbers and both policies live here so neither is ever
// re-derived from lore at a call site.
//
// releaseBodyMaxChars was measured 2026-08-11 against api.github.com, the
// internal/markdown way — ask the real system and keep the method beside the
// number: POST /repos/akira-toriyama/glyph-test/releases with a 125000-char
// body → 201 created; 125001 → 422 "body is too long (maximum is 125000
// characters)"; 124999 ASCII plus one 2-byte rune — 125000 CHARS, 125001
// BYTES — → 201. So the unit is characters, and a len() guard would refuse
// bodies GitHub accepts.
//
// commentBodyMaxChars is the issue-comment cap the preview's sticky comment is
// POSTed against (pr-verdict.yml upserts it through the comments API): the
// 65536 already established across the fleet's other projects.
const (
	releaseBodyMaxChars = 125000
	commentBodyMaxChars = 65536
)

// checkReleaseBody refuses a composed release body GitHub would reject, before
// anything is written. A refusal and never a truncation, on purpose: the walk
// READ the whole range, so a body missing part of it is a wrong document
// published as the release — §4's fails-loud family, the incomplete walk's
// exit, reached from the other side (the walk read everything and the OUTPUT
// cannot carry it). Without this guard the run computes a correct verdict and
// dies on the write at exit 4 anyway, having spent the run's one chance to
// land the notes on a body GitHub was always going to 422. The remedy mirrors
// the wedge escape: cut an intermediate tag, so the next walk — and its body —
// is smaller.
func checkReleaseBody(body string) error {
	if n := utf8.RuneCountInString(body); n > releaseBodyMaxChars {
		return core.APIf("the composed release body is %d characters and GitHub rejects a release body over %d — refusing to ship a truncated range: cut an intermediate tag so the walk (and its body) is smaller, or shorten the --footer-file", n, releaseBodyMaxChars)
	}
	return nil
}

// truncateComment keeps the preview comment postable: over the cap it cuts at
// a line boundary and says so, in the comment AND on stderr. Truncation and
// not refusal — the inverse of checkReleaseBody's policy, on purpose: the
// sticky comment is advisory and refreshed on every push, so a shortened one
// costs a scroll, while a refusal would take the whole verdict surface down
// with it. The notice is part of the comment because a truncated preview
// otherwise reads as a complete document that simply lists fewer commits.
//
// And the notice has to be READ, so the cut closes every <details> block it
// leaves open before the notice goes on. The notes preview is the body's last
// section and sits in one: a comment whose tables fit the cap and whose notes
// do not is cut inside it, and GitHub closes an open block at the end of the
// comment — the rule and the notice inside it, folded away under "Release
// notes preview" (t-rrw0 (5); the first cut knew lines and no blocks, and
// posted one <details> with no </details>). Both renderings were asked of
// GitHub's renderer, the open body and the closed one (DESIGN §4 keeps the
// method). The footer is past the cut either way: a truncated comment has
// none.
func truncateComment(body string) string {
	if utf8.RuneCountInString(body) <= commentBodyMaxChars {
		return body
	}
	const notice = "\n\n---\n\n… truncated: the full preview exceeds GitHub's comment cap. `glyph preview --pr <N> --notes` prints the whole document.\n"
	budget := commentBodyMaxChars - utf8.RuneCountInString(notice)
	head := cutAtLine(body, budget)
	// The closers count against the cap too, and what must close depends on
	// where the cut falls, so the two are settled together: each pass either
	// fits the head and its closers in the budget or cuts the head shorter by
	// at least a character, so it ends.
	for {
		closers := strings.Repeat(detailsCloser, openDetails(head))
		if utf8.RuneCountInString(head)+utf8.RuneCountInString(closers) <= budget {
			head += closers
			break
		}
		head = cutAtLine(head, budget-utf8.RuneCountInString(closers))
	}
	warnf("the preview body is %d characters and GitHub caps a comment at %d — posting a truncated preview (the cut is marked in the comment)", utf8.RuneCountInString(body), commentBodyMaxChars)
	return head + notice
}

// detailsCloser ends a <details> block the way internal/preview ends its own:
// a blank line, then the tag on a line to itself.
const detailsCloser = "\n\n</details>"

// cutAtLine keeps at most budget characters of body and then backs up to the
// end of the last whole line, so no construct is left half written ahead of
// the notice. No budget keeps nothing: truncateComment's loop relies on a
// shorter budget always giving a shorter head.
func cutAtLine(body string, budget int) string {
	if budget <= 0 {
		return ""
	}
	// Walk rune starts until the budget is spent; range-over-string is the
	// boundary-safe iteration, so the cut can never split a rune.
	cut := len(body)
	runes := 0
	for i := range body {
		if runes == budget {
			cut = i
			break
		}
		runes++
	}
	head := body[:cut]
	if nl := strings.LastIndexByte(head, '\n'); nl > 0 {
		head = head[:nl]
	}
	return head
}

// openDetails counts the <details> blocks head opens and does not close. It
// reads whole lines, the only form internal/preview writes the tags in: a
// subject or a notes line that mentions one is commit text, escaped or inside
// a code span, and never sits on a line of its own.
func openDetails(head string) int {
	depth := 0
	for line := range strings.SplitSeq(head, "\n") {
		switch line {
		case "<details>":
			depth++
		case "</details>":
			depth--
		}
	}
	return max(depth, 0)
}
