package markdown

import "strings"

// This file neutralizes author text; markdown.go then fences the mentions in
// the assembled line. The split is the whole design, and it rests on one
// theorem:
//
//	glyph does not have to predict which inline construct wins. It REMOVES the
//	competitors. Once every '<', every '[' and every extended-autolink trigger
//	in prose carries a backslash, the only construct left that can consume a
//	backtick is a code span — and codeSpans is a backtick-only model, so it
//	becomes exact BY CONSTRUCTION.
//
// The pass itself is escapeProseLine, at the foot of this file: it runs over the
// ASSEMBLED line, for the reason Line's doc gives (t-9np1).
//
// codeSpans' doc holds the four measured constructs that swallow a backtick —
// a link destination among them — and why that scan is not taught their
// grammars.
//
// THE ONE DESIGN RULE, and a later "let me make this more precise" pass must not
// violate it: every inexactness falls on the OVER-escaping side. A backslash in
// front of a byte that was inert anyway is INVISIBLE (measured below); a
// construct glyph failed to recognize is live. So none of the rules here tests a
// grammar. They test a byte, or a shape blunter than the grammar it stands for.
//
// ONE POLICY for every author field: a commit scope is prose like the subject
// beside it. DESIGN §2 holds that ruling, the <i title="x"> incident behind it,
// and why the plain-text route is deleted, not revived.

// flatten replaces every CommonMark line terminator in s with a single space:
// "\r\n", "\n" and a bare "\r" each become one space.
//
// A bare CR is a line terminator at GitHub, which is not obvious and is the
// whole bug (t-bz0r). A subject carrying one ENDS the notes list item, and
// whatever follows is parsed as a fresh block — so a commit could fabricate a
// release-notes section. Measured 2026-07-21, on bytes written in binary mode:
//
//	in   "- 🐛 **parser:** fix the thing\r## Fabricated Section (8e0abc1)"
//	out  <li>🐛 <strong>parser:</strong> fix the thing</li>
//	     <h2>Fabricated Section (8e0abc1)</h2>      ← a REAL section
//
//	in   "- 🐛 **parser:** fix the thing ## Fabricated Section (8e0abc1)"
//	out  <li>🐛 <strong>parser:</strong> fix the thing ## Fabricated Section …</li>
//
// A CR reaches a subject because bump.FirstLine trims only a TRAILING "\r":
// an interior one survives untouched.
//
// The order matters — CRLF first, so a CRLF yields ONE space rather than two.
//
// Line.Prose flattens each field as it arrives, before anything else looks at
// the text: flattening is what decides the inline context the later passes
// model (Line's doc has the order and the measured leak behind it).
//
// It deliberately does not live inside escapeMentionsSkipping: deleting a byte would
// break that function's no-rewriting invariant, which its fuzz oracle enforces.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}

// escapeProseLine neutralizes, in the PROSE of s, every inline construct that can
// escape its container, point somewhere the author never wrote, or outrank a
// backtick and steal a code-span delimiter. The author's own code spans pass
// through byte-for-byte, and emphasis and strikethrough are left working: a
// commit subject is prose that the author meant to be read, and the approved
// policy (2026-07-21) is to keep it readable while disarming it.
//
// It only ADDS backslashes — never rewrites or drops a byte — so the author's
// text survives as a subsequence of the result, and it is a fixed point.
//
// WHAT IT REPAIRS, not merely what it prevents: left raw, a tag in a subject
// can delete the author's own words from the notes. GitHub's sanitizer gives a
// raw tag one of three fates, measured 2026-07-21:
//
//	<i>, <kbd>, <img src=…>          LIVE — the tag renders, and it LEAKS past
//	                                 the list item and out of a <details> block
//	<script>, <title>, <style>, …    escaped by GFM's tagfilter (8 names only)
//	<Chip>, <Any>, </details>,       DELETED ENTIRELY — no tag, no text, the
//	<!-- c -->, <!DOCTYPE …>         author's words simply vanish
//
// The fleet's own history has 16 subjects carrying a '<', and 14 of them are the
// third kind. Measured against GitHub with real shipped subjects:
//
//	… ThemedChip — MUI <Chip> + <kbd> compact token …
//	  -> "MUI  + " — <Chip> deleted, <kbd> live and leaking past the </li>
//	… user-defined themes via [overlay.theme.<name>] …
//	  -> "[overlay.theme.]"
//	… unwrap Optional<Any> before NSNull check …
//	  -> "unwrap Optional before NSNull check"
//
// A backslash-escaped '<' renders as the literal text "<" — byte-identical to
// an inert one — in a paragraph, a table cell and inside a <details> block (nine
// probes, one result). So escaping repairs those 14 and regresses none.
//
// FIVE RULES. Each is the bluntest form that works, and none of them parses:
//
//  1. '<' — escaped unconditionally, with no grammar test at all. The
//     raw-HTML/autolink grammar has at least eleven traps (a 32-character scheme
//     cap, DEL legal inside a URI, case-insensitive CDATA, "<a >" legal while
//     "< a>" is not, "</a >" legal while "</a href=x>" is not, tag names taking
//     hyphens but not underscores, constructs spanning a line ending, cmark's
//     declaration form diverging from CommonMark 0.31.2). Implementing it is
//     eleven chances to under-escape. Not implementing it costs nothing.
//
//  2. The EXTENDED autolink — the ':' of "http://", "https://", "ftp://" and the
//     '.' of "www." are escaped. This is the part a '<'-only fix cannot reach,
//     and it is forced. Measured, with no angle bracket anywhere in the input:
//
//     fix http://a/`x so @octocat can `see` it          LIVE MENTION
//
//     The bare URL is autolinked, eats the backtick, and the mention behind it
//     stays in prose. Measured too: the backslash must sit before the ':' or the
//     '.', not before the token — "\http://evil.com" still links, while
//     "http\://a.com" and "www\.a.com" render with the backslash consumed and no
//     link at all. And escaping only '<' is measurably not enough: GFM
//     relinkifies "\<http://a/x>" and swallows the '>' into the href as %3E.
//
//     Blunter than cmark in three places, each over-escaping invisibly: no
//     rewind-and-compare on the scheme (so "xhttp://a" is escaped although
//     GitHub would not link it), no preceding-character guard on "www.", and no
//     domain, trailing-punctuation or paren-balancing rules. None can matter —
//     this decides only that a link never STARTS, never where one ends.
//
//  3. '[' — escaped unconditionally. A link destination consumes a backtick (see
//     the file header), so this is the same bug as the autolink one wearing
//     brackets, and it is what makes the theorem above true rather than nearly
//     true. It closes the arbitrary-link symptom for free, and images and
//     footnotes with it. Escaping the '[' alone suffices: a ']' with no live
//     opener does nothing. Measured cost on the fleet: 114 subjects carry a '['
//     — TOML tables and milestone tags, "[overlay.theme.<name>]", "[M4-η] (#72)"
//     — and every one renders identically escaped. Exactly one subject in 9,548
//     carries a "](", and it is an empty "![]()" that renders as nothing today.
//
//  4. An entity-SHAPED '&' — escaped, because a character reference resolves
//     AFTER inline parsing and the mention filter then walks the rendered
//     document. Measured: "callers to &#64;v1" is a LIVE MENTION, and no fence
//     can prevent it because there is no at-sign in the source for the mention
//     pattern to find. "\&#64;v1" renders as the literal text. This is the one
//     rule aimed at a construct that cannot steal a backtick; it closes a
//     documented mention hole instead. Zero subjects in the fleet's 9,548 carry
//     an entity-shaped ampersand, so the rendering cost is measured at zero.
//
//  5. A BLOCK OPENER at a block start — escaped. Block structure is decided
//     before any inline rule above runs, and the start of a list item is a
//     block start as much as the start of a line is, so a template's opening
//     "- " protects nothing: "- ## Breaking Changes" renders a heading inside
//     the item. It reads more of the line than the other rules do, because four
//     of the bytes it fires on are inline syntax the approved policy keeps
//     working (a leading emphasis run stays emphasis, a leading code span stays
//     code). blockOpenerAt holds the shapes and their measurements; blockOpening
//     says where a block starts. An indent is the one opener a backslash cannot
//     reach: Line.Prose drops it.
//
// Author code spans are skipped whole: a backslash is LITERAL inside a code
// span, so escaping there would print as visible noise, and nothing inside one
// can escape it anyway (measured: in "a `<i title=\"x\">` b" the code span wins
// and the tag is inert inside it).
//
// WHY ONE PASS IS ENOUGH, although the span map is computed before rules 1–4 have
// run and may hold a phantom span. Two facts:
//
//   - The map holds for the finished line. It is read with rule 5's backslashes
//     already in place (spansBehindOpeners), and every other inserted byte is a
//     backslash placed in front of a '<', '[', ':', '.' or '&' — never in front
//     of a backtick — so backtick runs keep their lengths and their order,
//     paragraphSpans' backslash case skips exactly the bytes its default case
//     would have copied, and no inserted byte is space, tab or CR, so a blank
//     line stays blank.
//   - After the pass, codeSpans agrees with GitHub. Enumerate the GFM inline
//     constructs that could consume a backtick and diverge from a backtick-only
//     model: raw HTML, both angle autolinks, the extended autolinks, links,
//     images and footnotes are all dead by rules 1–3; backslash escapes and
//     code spans (including cmark's memo) are what paragraphSpans already
//     models; emphasis and strikethrough consume no backtick; an entity
//     reference admits no backtick between its '&' and its ';'.
//
// One construct is deliberately LEFT ALIVE: the bare-email, mailto: and xmpp:
// autolinks. Measured, no additive escape kills them — neither "a\@b.com" nor
// "a@b\.com" — because that pass runs over already-parsed text nodes; only a
// code span does, which the policy forbids in a subject. They need no killing:
// their grammars admit neither a backtick nor a '<', so they can neither steal a
// delimiter nor form a phantom span, and the href is the address the author
// typed, so they cannot point anywhere the author did not write.

// foldSuffix reports whether the bytes ending just before i equal w, ignoring
// case — schemes are case-insensitive at GitHub ("HTTP://EVIL.COM" is linked).
func foldSuffix(p string, i int, w string) bool {
	return i >= len(w) && strings.EqualFold(p[i-len(w):i], w)
}

// entityAt reports whether a character reference starts at p[i] (which the
// caller has already checked is an ampersand): "&#" + decimal digits, "&#x" +
// hex digits, or "&" + a letter and alphanumerics, each closed by a semicolon.
//
// The shape is tested rather than the NAME or the WIDTH, so an unknown name and
// a reference too wide for GitHub to decode are escaped too. That is the
// over-escaping direction and it is invisible: GitHub renders an unrecognized
// reference as its own literal text either way. A cap here would owe this
// boundary's correctness to GitHub's limit (TestEscapeMarkup has the widths
// measured).
func entityAt(p string, i int) bool {
	rest := p[i+1:]
	switch {
	case strings.HasPrefix(rest, "#x"), strings.HasPrefix(rest, "#X"):
		return closedBy(rest[2:], isHexDigit)
	case strings.HasPrefix(rest, "#"):
		return closedBy(rest[1:], isDigit)
	case rest != "" && isASCIILetter(rest[0]):
		return closedBy(rest[1:], isAlphanumeric)
	}
	return false
}

// closedBy reports whether s opens with one or more bytes satisfying ok and then
// a semicolon.
func closedBy(s string, ok func(byte) bool) bool {
	n := 0
	for n < len(s) && ok(s[n]) {
		n++
	}
	return n > 0 && n < len(s) && s[n] == ';'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlphanumeric(c byte) bool { return isASCIILetter(c) || isDigit(c) }

// blockOpener is a block construct a line opens at some byte.
type blockOpener struct {
	end       int  // just past the bytes that make it one
	at        int  // the byte rule 5's backslash goes in front of
	container bool // another block can start right behind it: a list item, a block quote
}

// blockOpenerAt reports the block construct a line opens at s[i], with s ending
// where the line ends. The shapes are GitHub's, measured 2026-11-04 by POST
// /markdown (mode gfm) on the bytes `glyph notes --range` writes, a value behind
// the shipped template's "- ":
//
//	line                   renders           shape
//	- ## x                 <li><h2>          "#" to "######", then a space, a tab or the end
//	- > x    - >x          <li><blockquote>  any ">"
//	- 1. x   - 1) x        <li><ol>          up to nine digits, "." or ")", then the same
//	- - x    - * x   - + x <li><ul>          "-", "*" or "+", then the same
//	- ``` x                <li><pre>         three backticks and none behind them on the line
//	- ~~~ x  - ~~~x~~~     <li><pre>         three tildes, whatever follows
//	- ***    - _ _ _       <li><hr>          only "-", "*" or "_" and blanks to the end
//	- ---    - --          <hr>              the same, and it takes the bullet with it
//
// The last two need the end of the line, so a credit behind the value disarms
// them. A setext underline ("=" or "-" alone to the end of the line) needs a
// paragraph on the line above, which a list item's first line does not have: it
// is live only where a template opens with no bullet ("a\n===" renders
// <h1>a</h1>).
//
// What the same measurement leaves alone, and so must not fire: emphasis and
// strikethrough at the head of a value (the inline syntax rule 5 owes its shape
// tests to), a code span there, a backtick run with a backtick behind it on the
// line, and "#64 fix", "#x", "-x", "--flag", "+1 x", "1.2.3 x", "=x". A form feed
// or a vertical tab behind a marker opens nothing either.
//
// Blunter than the grammar wherever the cost is invisible, each escaped form
// measured to render as its own text in a list item, in a table cell and inside a
// <details> block: "#" and the digits have no cap, and a thematic break or an
// underline needs no count ("**", "__", "-" and "=" fire alone). Nothing here
// decides what a block would hold; it decides only that one never starts.
func blockOpenerAt(s string, i int) (blockOpener, bool) {
	c := s[i]
	end := i
	for end < len(s) && s[end] == c {
		end++
	}
	switch {
	case c == '>':
		return blockOpener{end: i + 1, at: i, container: true}, true
	case c == '#' && blankAt(s, end):
		return blockOpener{end: end, at: i}, true
	case c == '~' && end-i >= 3:
		return blockOpener{end: end, at: i}, true
	case c == '`' && end-i >= 3 && !strings.Contains(s[end:], "`"):
		return blockOpener{end: end, at: i}, true
	case (c == '-' || c == '+' || c == '*') && blankAt(s, i+1):
		return blockOpener{end: i + 1, at: i, container: true}, true
	case (c == '-' || c == '*' || c == '_' || c == '=') && strings.Trim(s[i:], string(c)+" \t") == "":
		return blockOpener{end: len(s), at: i}, true
	case isDigit(c):
		end = i
		for end < len(s) && isDigit(s[end]) {
			end++
		}
		if end < len(s) && (s[end] == '.' || s[end] == ')') && blankAt(s, end+1) {
			return blockOpener{end: end + 1, at: end, container: true}, true
		}
	}
	return blockOpener{}, false
}

// blankAt reports whether a marker ending just before i is one: a space, a tab or
// the end of the line follows it.
func blankAt(s string, i int) bool {
	return i >= len(s) || s[i] == ' ' || s[i] == '\t'
}

// blockOpening walks the line s[from:], with s ending where the line ends, past
// the indent and past every container a template wrote in full: a block starts
// behind each one, so "- > " leaves the value at a block start as "- " does.
//
// It returns the byte rule 5 escapes on this line, -1 for none, and whether the
// line holds nothing yet but those containers, which is what Line.Prose reads.
// An opener that touches prose is escaped whichever byte the rule fires on, and
// the line is then a paragraph with no further block start. One wholly the
// template's is left alone (its "## " before a value is the template's heading).
func blockOpening(s string, from int, prose func(lo, hi int) bool) (at int, waiting bool) {
	for i := from; ; {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i == len(s) {
			return -1, true
		}
		op, ok := blockOpenerAt(s, i)
		switch {
		case !ok:
			return -1, false
		case prose(i, op.end):
			return op.at, false
		case !op.container:
			return -1, false
		}
		i = op.end
	}
}

// blockOpeners holds rule 5's bytes for every line of s. A template may carry a
// line terminator of its own, and each line starts blocks of its own.
func blockOpeners(s string, prose func(lo, hi int) bool) map[int]bool {
	opens := map[int]bool{}
	for from := 0; from <= len(s); {
		to := len(s)
		if n := strings.IndexAny(s[from:], "\r\n"); n >= 0 {
			to = from + n
		}
		if at, _ := blockOpening(s[:to], from, prose); at >= 0 {
			opens[at] = true
		}
		from = to + 1
	}
	return opens
}

// awaitsBlock reports whether whatever is written next on s lands at a block
// start: its last line holds nothing yet but a template's containers.
func awaitsBlock(s string, prose func(lo, hi int) bool) bool {
	_, waiting := blockOpening(s, strings.LastIndexAny(s, "\r\n")+1, prose)
	return waiting
}

// spansBehindOpeners marks the bytes of s inside a code span, reading the line as
// it reads with rule 5's backslashes in place: block structure comes first, so
// that is the text GitHub pairs backticks in.
//
// On one line this changes nothing. The only opener that is a backtick is a fence's
// run with none behind it on the line, and that run pairs with nothing either way.
// Across a template's own line ending it does change: these two runs are one span
// in the bare string, and with their backslashes they are literal text, with the
// tag between them prose that rule 1 must reach (a fuzz corpus entry,
// fence-backslash-dissolves-a-span-across-a-line-ending, holds the case):
//
//	``` <i>
//	```
func spansBehindOpeners(s string, prose func(lo, hi int) bool) []bool {
	opens := blockOpeners(s, prose)
	var b strings.Builder
	from := make([]int, 0, len(s)+len(opens))
	for i := 0; i < len(s); i++ {
		if opens[i] {
			b.WriteByte('\\')
			from = append(from, i)
		}
		b.WriteByte(s[i])
		from = append(from, i)
	}
	inSpan := make([]bool, len(s))
	for _, sp := range codeSpans(b.String()) {
		for _, i := range from[sp[0]:sp[1]] {
			inSpan[i] = true
		}
	}
	return inSpan
}

// escapeProseLine applies the five rules above to the ASSEMBLED line s, which
// is the only context in which they are decidable. Detection reads the whole
// string; a backslash is inserted only where the construct it would disarm
// touches author prose, which prose reports for a byte range.
//
// Per field, every one of the rules carries state or context that a field
// boundary truncates (t-9np1, measured on the per-field escaper that shipped):
//
//	prose "…thing \" + prose "<h1>OWNED</h1>"   ->  \\<h1>  — the author's own
//	   trailing backslash escaped the one the next stretch inserted, leaving a
//	   LIVE '<'. One pass reads "\<" as the author's escape and copies the pair.
//	prose "see www" + prose ".evil.example/x"   ->  a live autolink: foldSuffix
//	   looked back only as far as the stretch began.
//	prose "cc &" + prose "#64;octocat"          ->  a live @mention: entityAt
//	   looked ahead only as far as the stretch ended.
//
// A construct that STRADDLES prose and glyph's own markup is escaped too, at
// whichever byte the rule fires on. That can put a backslash in front of a byte
// Raw contributed, which is the over-escaping side the design rule prefers and
// is invisible in the render; a construct lying wholly inside Raw is glyph's
// own and is left alone.
// inSpan comes from spansBehindOpeners. It returns the escaped line and pos, where
// pos[i] is the offset in the result of input byte i (and pos[len(s)] the end) —
// the fence pass needs it to find Mention's exempt ranges again after escaping has
// shifted everything right.
func escapeProseLine(s string, inSpan []bool, prose func(lo, hi int) bool) (string, []int) {
	var b strings.Builder
	b.Grow(len(s))
	pos := make([]int, len(s)+1)
	opens := blockOpeners(s, prose)
	esc := func(lo, hi, at int) {
		if prose(lo, hi) {
			b.WriteByte('\\')
		}
		b.WriteByte(s[at])
	}
	for i := 0; i < len(s); {
		pos[i] = b.Len()
		// Ahead of the span test: block structure outranks inline structure, so a
		// line that would open a block is disarmed even where a span crosses a
		// template's own line ending into it. The cost is a backslash printed in
		// that span, the over-escaping side.
		if opens[i] {
			b.WriteByte('\\')
			b.WriteByte(s[i])
			i++
			continue
		}
		// Inside a code span nothing is escaped: the bytes are inert, and this
		// is the one exemption the package grants. It is exact only because
		// codeSpans models cmark's MAXBACKTICKS bound (markdown.go).
		if inSpan[i] {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch {
		case s[i] == '\\':
			// An already-escaped byte is copied as a pair and skipped — what
			// makes the pass a fixed point, and what keeps an author's own "\<"
			// from becoming "\\<": a literal backslash followed by a LIVE '<'.
			if i+1 < len(s) {
				b.WriteString(s[i : i+2])
				pos[i+1] = b.Len() - 1
				i += 2
			} else {
				b.WriteByte('\\')
				i++
			}
		case s[i] == '<', s[i] == '[':
			esc(i, i+1, i)
			i++
		case s[i] == ':' && strings.HasPrefix(s[i:], "://") &&
			(foldSuffix(s, i, "http") || foldSuffix(s, i, "https") || foldSuffix(s, i, "ftp")):
			esc(i-len("http"), i+3, i)
			i++
		case s[i] == '.' && foldSuffix(s, i, "www"):
			esc(i-len("www"), i+1, i)
			i++
		case s[i] == '&' && entityAt(s, i):
			esc(i, i+1, i)
			i++
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	pos[len(s)] = b.Len()
	return b.String(), pos
}
