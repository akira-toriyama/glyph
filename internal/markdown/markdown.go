// Package markdown holds the escaping rules for author-supplied text that
// glyph embeds into GitHub-rendered Markdown (release-notes lines, the
// pr-verdict table). It is pure — no I/O, no globals — and deliberately tiny:
// each function neutralizes one specific GitHub rendering behavior that turns
// honest commit text into something it never meant to be.
//
// Everything here is a MODEL of a renderer no unit test can call. Every rule
// below was measured against GitHub's own renderer (`gh api -X POST /markdown`,
// mode=gfm) on 2026-07-21; the probes, their observed output and the date sit
// in markdown_test.go. Re-run them before changing a rule — the one time a rule
// was changed from reasoning alone, the reasoning was wrong (an at-sign written
// as the entity &#64; renders back to "@" and GitHub linked it anyway, because
// mentions are added by a POST-PROCESSOR over the rendered document, long after
// CommonMark has decoded the entity).
//
// One editing rule for the comments in this file: an example holding two
// backticks in a row goes in an INDENTED block, like the ones below. gofmt
// reformats doc-comment prose, and a doubled backtick there is rewritten into a
// typographic quote — which turns an example of the exact bug this package is
// about into nonsense, silently, on somebody else's commit. (This paragraph is
// itself gofmt-safe only because it spells the pair out in words.)
package markdown

import (
	"regexp"
	"strings"
)

// username is GitHub's shape for the name half of a mention: alphanumerics and
// hyphens, never ENDING on a hyphen. The tail rule is not cosmetic — the match
// consumes the character in FRONT of the at-sign, so a name that swallowed its
// trailing hyphen also swallowed the delimiter the NEXT mention needed and left
// that one unescaped. The fuzz target caught it on "@0-@0".
const username = `[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?`

// mention matches a token GitHub would link as a user mention.
//
// The guard group is the character in front of the at-sign: a mention starts
// the string or follows something that is neither a word character nor a
// backtick. Both exclusions are GitHub's, measured:
//
//   - a word character means an email or a pinned ref (dev@example.com,
//     actions/checkout@v5), which GitHub does not link;
//   - a BACKTICK glued to the at-sign suppresses the mention too ("x `@octocat
//     y" renders no link). That is not a fact about code spans: the backtick is
//     plain literal text there, and the mention filter still refuses it.
//
// The backtick exclusion is load-bearing twice over. Every stretch this pattern
// is run over is PROSE (code spans are skipped whole), so every backtick it can
// see is literal text — exactly the shape GitHub refuses. And it is what makes
// EscapeMentions a fixed point: the escaper's own fence is a backtick glued to
// the at-sign, so a second pass matches nothing of what the first pass wrote.
// The one backtick that does NOT suppress is a code span's closing delimiter
// ("`code`@octocat" IS a live mention — after rendering, the at-sign starts a
// fresh text node with nothing in front of it), and that backtick lies outside
// the prose stretch, so this pattern never sees it.
//
// GitHub is stricter still on the RIGHT of the name — it refuses a mention
// followed by a literal backtick or a hyphen — and glyph deliberately does not
// model that half: over-escaping there costs a code span nobody asked for,
// under-escaping costs a notification. Only the left rule is forced.
//
// The name may be a CHAIN of at-tokens ("@user@octocat"). GitHub does not link
// the second token in the raw text (a word character precedes it), but wrapping
// only the first would END A TEXT NODE right in front of it and promote it to a
// live mention: "`@user`@octocat" links @octocat. Swallowing the chain into one
// code span is what keeps the transformation from CREATING a mention.
//
// The at-sign sits BETWEEN the two groups rather than inside one: the escaper
// rebuilds the token from group 2 and needs to know where the at-sign starts.
var mention = regexp.MustCompile("(^|[^A-Za-z0-9_`])@(" + username + "(?:@" + username + ")*)")

// EscapeMentions neutralizes would-be @mentions in s by wrapping each one in a
// backtick fence: "callers to @v1" becomes "callers to `@v1`", which GitHub
// renders as the code token @v1 and links to nobody. Left raw, such a token in
// a release body silently lists a stranger under the release's Contributors,
// and in a PR comment it NOTIFIES them (incident t-hykw).
//
// Wrapping is the ONLY neutralization GitHub honors. Rewriting the at-sign as
// an entity (&#64;, &#x40;, &commat;) or backslash-escaping it does nothing at
// all: mentions are attached by a post-processor walking the RENDERED document,
// where every one of those has already decoded back to a literal at-sign. A
// code span survives that post-processor because <code> is one of the ancestors
// it refuses to touch — and, as a second and independent line, because the
// filter also refuses an at-sign with a backtick glued to it, which stays true
// of glyph's output even in the shapes where GitHub declines to form the span.
//
// THE FENCE IS AS LONG AS THE INPUT DEMANDS. A single inserted backtick is
// Markdown syntax that pairs with the author's: a subject holding a stray
// backtick — "accept a lone ` in the subject, as @octocat asked" — had the
// escaper's opening backtick swallowed by the author's, which fused a run of
// the author's words into a code span and pushed the mention back out into
// prose (t-fbg3). CommonMark pairs a run of N backticks only with a LATER run
// of EXACTLY N, so a fence of (longest run in the input + 1) cannot pair with
// anything the author wrote: every authored run is shorter, and a shorter run
// can never close a longer one nor be taken out of the middle of it. Two or
// more fenced mentions pair in order — open, close, open, close.
//
// A fence is never allowed to TOUCH a character that would consume it, and a
// single space is written between them where it would. Two of those exist: a
// backtick the author wrote, because two adjacent runs fuse into one longer run
// and the pairing collapses (this can only happen at the edge of a code span,
// since a mention with a LITERAL backtick in front of it is not a mention at
// all); and an escaping backslash, which swallows the opening fence outright.
// That space is the one character this function adds to the author's sentence,
// and it buys a clean render — the fused form below is what GitHub prints with
// its backticks showing, because a run of three has nothing to pair with:
//
//	"`code`@octocat"  ->  "`code` ``@octocat``"   and not  "`code```@octocat``"
//
// Text inside a code span the author wrote passes through byte-for-byte: it is
// already mention-inert, and a fence inserted inside it would print as visible
// backtick noise.
//
// Not every at-sign is a mention: an email (dev@example.com), a pinned ref
// (actions/checkout@v5) and a lone at-sign ("meet @ noon") stay raw, exactly as
// GitHub itself treats them.
func EscapeMentions(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	fence := strings.Repeat("`", longestBacktickRun(s)+1)
	var b strings.Builder
	last := 0
	for _, span := range codeSpans(s) {
		fenceMentions(&b, s, last, span[0], fence)
		b.WriteString(s[span[0]:span[1]])
		last = span[1]
	}
	fenceMentions(&b, s, last, len(s), fence)
	return b.String()
}

// fenceMentions copies s[from:to] — a stretch GitHub renders as prose — into b,
// wrapping every would-be mention in fence.
//
// It indexes the FULL string, not the stretch, to decide whether a separating
// space is needed: the neighbor that can fuse with the fence is the code-span
// delimiter just outside the stretch, which a pattern run over the stretch
// alone would report as "start of text" and miss.
func fenceMentions(b *strings.Builder, s string, from, to int, fence string) {
	prose := s[from:to]
	last := 0
	for _, m := range mention.FindAllStringSubmatchIndex(prose, -1) {
		// Group 1 ends on the at-sign (it is empty at the start of the
		// stretch); group 2 ends on the last character of the name.
		at, end := m[3], m[5]
		b.WriteString(prose[last:at])
		if isBacktick(s, from+at-1) || escapesTheNextByte(s, from+at) {
			b.WriteByte(' ')
		}
		b.WriteString(fence)
		b.WriteString(prose[at:end])
		b.WriteString(fence)
		if isBacktick(s, from+end) {
			b.WriteByte(' ')
		}
		last = end
	}
	b.WriteString(prose[last:])
}

// isBacktick reports whether s[i] is a backtick, tolerating an index off either
// end (the character before the first mention, the character after the last).
func isBacktick(s string, i int) bool {
	return i >= 0 && i < len(s) && s[i] == '`'
}

// escapesTheNextByte reports whether an ODD number of backslashes ends just
// before i, which is what makes the byte at i escaped rather than syntax.
//
// A fence written straight after such a run would be eaten by it — the author's
// backslash escapes the escaper's own backtick — and the span would not form.
// GitHub still declines the mention there (the backtick survives as literal
// text glued to the at-sign), but glyph's next pass reads the wreckage as prose
// and fences it AGAIN, so the transformation stops being a fixed point. The
// fuzz target found it on "\@0 @0". An even run is a run of escaped
// backslashes, literal text that escapes nothing.
//
// A backslash cannot escape the at-sign out of being a mention, incidentally:
// GitHub renders "\@octocat" as a live mention, the backslash having vanished
// in rendering before the mention filter ever looks.
func escapesTheNextByte(s string, i int) bool {
	n := 0
	for i-n > 0 && s[i-n-1] == '\\' {
		n++
	}
	return n%2 == 1
}

// longestBacktickRun returns the length of the longest run of consecutive
// backticks in s, which is what the fence has to beat. Escaped backticks are
// counted like any other: an over-long fence is harmless, while a fence that
// ties an authored run can be closed by it.
func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	return longest
}

// codeSpans reports the byte ranges of s that GitHub renders as inline code
// spans, delimiters included. Everything else is prose, where a mention lives.
//
// Inline parsing is per BLOCK, so the scan runs per paragraph: a code span
// cannot reach across a blank line, and neither can the backtick bookkeeping
// below. Today's callers pass a single line (a commit subject, a scope), so the
// split is unreachable from here — but this scan decides where mentions stay
// live, and a safety boundary should not owe its correctness to a caller's line
// discipline.
//
// KNOWN LIMITATION, accepted and pinned by a test: this scan knows only
// backticks, and CommonMark has other inline constructs that are parsed FIRST
// and can swallow one. A URI autolink or a raw HTML tag carrying a backtick —
// "fix <http://a/`x> so @octocat can `see` it" — leaves the next backtick to
// pair with a later one, and glyph reads the phantom span as already-inert and
// leaves the mention inside it raw. GitHub renders that as a live mention.
// Closing the gap needs a real inline parser (autolinks, raw HTML, entity
// references, link destinations), which is a different program from a commit
// linter; the same blindness in the other direction fences a mention inside an
// autolinked URL, which is cosmetic. Both stay in the box marked "known".
func codeSpans(s string) [][2]int {
	var spans [][2]int
	for from := 0; ; {
		to := paragraphBreak(s, from)
		if to < 0 {
			return append(spans, paragraphSpans(s[from:], from)...)
		}
		spans = append(spans, paragraphSpans(s[from:to], from)...)
		from = to + 1
	}
}

// paragraphSpans finds the code spans of one paragraph, reporting them in the
// coordinates of the whole string (offset is where the paragraph starts).
//
// This is a hand-written scan because the rule cannot be written as a regexp.
// The implementation this replaced tried ("`[^`]*`" — a backtick, non-backticks,
// a backtick) and that pattern is wrong in both directions: it takes an
// UNMATCHED backtick for an opener, and it cannot see a multi-backtick span at
// all. Both directions leaked live mentions — in the line below there is no
// code span at GitHub, because a run of one cannot be closed by a run of two,
// but the pattern claimed one around the mention and glyph left it raw, which
// is a notification:
//
//	a ` b @octocat c `` d
//
// CommonMark pairs backtick RUNS: a run of N consecutive backticks opens a span
// that only a later run of EXACTLY N closes, and a run with no such partner is
// literal text that opens nothing. "The nearest later run of exactly N" is a
// counting problem, not a pattern-matching one.
//
// Backslashes get the asymmetry CommonMark gives them, likewise unexpressible
// as a pattern: in prose "\`" is an escaped, literal backtick that opens no span
// ("\`not code`" renders both backticks visibly), but INSIDE a span backslashes
// carry no meaning, so a "\`" closes it ("`foo\`bar`" is the code "foo\" followed
// by prose). Hence: honor escapes while looking for an opener (here), ignore
// them while looking for the closer (in backtickSearch.closer).
func paragraphSpans(p string, offset int) [][2]int {
	var spans [][2]int
	search := backtickSearch{lastRun: map[int]int{}}
	for i := 0; i < len(p); {
		switch p[i] {
		case '\\':
			// Skip the escaped byte. Safe on UTF-8 input: a continuation byte
			// is >= 0x80, never a backtick or a backslash, so stepping into the
			// middle of a rune can neither fabricate nor hide a delimiter.
			i += 2
		case '`':
			n := backtickRun(p, i)
			end := search.closer(p, i+n, n)
			if end < 0 {
				// No partner: this run is literal text. Resume AFTER it — it is
				// spent, and re-reading its backticks would let a shorter suffix
				// of it open a span CommonMark never grants (a backtick string
				// is maximal by definition).
				i += n
				continue
			}
			spans = append(spans, [2]int{offset + i, offset + end})
			i = end
		default:
			i++
		}
	}
	return spans
}

// backtickSearch carries the state cmark-gfm — the parser GitHub actually runs
// — keeps while pairing backtick runs within one block, and glyph reproduces it
// because it is OBSERVABLE: it decides whether a span exists, and a span is
// where a mention stops being a mention.
//
// cmark's closer search is memoized so that a document full of stray backticks
// cannot cost O(n²): every run the search walks past is recorded as the last
// known run of ITS length, and a search that reaches the end of the block sets
// exhausted. From then on an opener of length N is refused outright unless a
// run of exactly N is known to sit AFTER it — including the case where no run
// of that length was ever recorded, whose zero position is behind everything.
//
// The consequence is not obvious and is not in the CommonMark spec: once some
// unmatched run has exhausted the search, only the FIRST of two equal-length
// spans is formed. In the first line below GitHub renders a code span "a" and
// then the LITERAL text `b`; in the second it renders the mention as prose and
// links it, while glyph — modelling the spec instead — read the second span as
// already-inert and left the mention alone:
//
//	`` `a` `b`
//	`` `a` `x @octocat y`
//
// Modelling the memo can only cost a span glyph would otherwise trust, which is
// the safe direction: a mention inside a span glyph no longer believes in gets
// fenced, and a fence inside a span GitHub does believe in is visible noise,
// not a notification.
//
// cmark's other bound on this — it refuses to OPEN a span with a run longer
// than 80 backticks, the width of the memo array — is deliberately not modelled.
// Honouring it would make the escaper's own fence unopenable on a subject
// carrying 80 consecutive backticks, and the pass would then grow its fence by
// one on every run instead of standing still. In that shape the neutralization
// still holds by the second line of defence: the fence stays glued to the
// at-sign as literal text, and the mention filter refuses it there.
type backtickSearch struct {
	// lastRun maps a run length to the start of the most recent run of that
	// length any search has walked past.
	lastRun map[int]int
	// exhausted records that some search ran off the end of the block, which is
	// what arms the memo (cmark consults it only then).
	exhausted bool
}

// closer finds the first backtick run of exactly n backticks at or after from
// (from being the position just past the opening run), and returns the index
// just past it, or -1 when the opener has no partner. A run of another length
// is skipped WHOLE: three backticks cannot close one, and neither can any
// single backtick taken out of them.
func (b *backtickSearch) closer(p string, from, n int) int {
	if b.exhausted && b.lastRun[n] <= from {
		return -1
	}
	for i := from; i < len(p); i++ {
		if p[i] != '`' {
			continue
		}
		m := backtickRun(p, i)
		b.lastRun[m] = i
		if m == n {
			return i + m
		}
		i += m - 1 // the loop's i++ then lands just past this run
	}
	b.exhausted = true
	return -1
}

// backtickRun returns the length of the run of backticks starting at i.
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// paragraphBreak returns the index of the newline that opens the first blank
// line at or after from, or -1 when the text runs to its end unbroken. A line
// holding nothing but spaces, tabs and a carriage return counts as blank.
func paragraphBreak(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
			j++
		}
		if j == len(s) || s[j] == '\n' {
			return i
		}
	}
	return -1
}
