package markdown

import "strings"

// This file neutralizes author text BEFORE it is assembled into a rendered line;
// markdown.go then fences the mentions in the assembled line. The split is the
// whole design, and it rests on one theorem:
//
//	glyph does not have to predict which inline construct wins. It REMOVES the
//	competitors. Once every '<', every '[' and every extended-autolink trigger
//	in prose carries a backslash, the only construct left that can consume a
//	backtick is a code span — and codeSpans is a backtick-only model, so it
//	becomes exact BY CONSTRUCTION.
//
// The pass itself is escapeProseLine, at the foot of this file: it runs over the
// ASSEMBLED line, because a code span pairs across fields (t-9np1).
//
// That is why the known limitation codeSpans used to carry (an autolink or a
// raw HTML tag swallowing a backtick, so glyph believed a PHANTOM span and left
// the mention inside it raw) is closed here rather than there. Teaching
// codeSpans the autolink and raw-HTML grammars would have been eleven chances
// to be wrong in the under-escaping direction — and wrong is a notification —
// while a scanner that models those two constructs is STILL incomplete: a link
// destination eats a backtick too. Measured 2026-07-21:
//
//	see [a](b`c) @octocat `d` end                      LIVE MENTION
//
// cmark reaches the ']', parses "(b`c)" as a link destination and consumes the
// backtick, so GitHub leaves the at-sign in prose. Killing the construct closes
// that door and every door like it, including ones nobody has found yet.
//
// THE ONE DESIGN RULE, and a later "let me make this more precise" pass must not
// violate it: every inexactness falls on the OVER-escaping side. A backslash in
// front of a byte that was inert anyway is INVISIBLE (measured below); a
// construct glyph failed to recognize is live. So none of the rules here tests a
// grammar. They test a byte.

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
// Flattening also has to happen before anything else looks at the text, because
// it is what DECIDES the inline context: to an escaper a blank line ends the
// paragraph and backticks on either side of it cannot pair, while in the
// flattened line they can. Line's Text and Prose flatten before they escape for
// exactly this reason, with a measured leak behind the rule (it was
// preview.escapeCell's, from the era when the order was each caller's to get
// right).
//
// It deliberately does not live inside escapeMentions: deleting a byte would
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
// WHAT IT REPAIRS, not merely what it prevents. Raw HTML in a subject is not a
// theoretical injection; it is silently eating real release notes today.
// GitHub's sanitizer gives a raw tag one of three fates, measured 2026-07-21:
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
// FOUR RULES. Each is the bluntest form that works, and none of them parses:
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
// Author code spans are skipped whole: a backslash is LITERAL inside a code
// span, so escaping there would print as visible noise, and nothing inside one
// can escape it anyway (measured: in "a `<i title=\"x\">` b" the code span wins
// and the tag is inert inside it).
//
// WHY ONE PASS IS ENOUGH, although the span map is computed on the ORIGINAL
// string and may hold a phantom span. Two facts:
//
//   - codeSpans(escapeProseLine(s, …)) == codeSpans(s), modulo offsets. Every inserted
//     byte is a backslash placed in front of a '<', '[', ':', '.' or '&' —
//     never in front of a backtick — so backtick runs keep their lengths and
//     their order, paragraphSpans' backslash case skips exactly the bytes its
//     default case would have copied, and no inserted byte is space, tab or CR,
//     so a blank line stays blank.
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
// caller has already checked is an ampersand): "&#" + up to 7 decimal digits,
// "&#x" + up to 6 hex digits, or "&" + a name of up to 32 alphanumerics, each
// closed by a semicolon.
//
// The shape is tested rather than the NAME, so an unknown name is escaped too.
// That is the over-escaping direction and it is invisible: GitHub renders an
// unrecognized reference as its own literal text either way.
func entityAt(p string, i int) bool {
	rest := p[i+1:]
	switch {
	case strings.HasPrefix(rest, "#x"), strings.HasPrefix(rest, "#X"):
		return closedBy(rest[2:], 6, isHexDigit)
	case strings.HasPrefix(rest, "#"):
		return closedBy(rest[1:], 7, isDigit)
	case rest != "" && isASCIILetter(rest[0]):
		return closedBy(rest[1:], 31, isAlphanumeric)
	}
	return false
}

// closedBy reports whether s opens with one or more bytes satisfying ok — at
// most maxRun of them — and then a semicolon.
func closedBy(s string, maxRun int, ok func(byte) bool) bool {
	n := 0
	for n < len(s) && n <= maxRun && ok(s[n]) {
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

// escapable is every ASCII punctuation byte CommonMark lets a backslash escape,
// minus the two escapeText deliberately leaves alone.
const escapable = "!\"#$%&'()*+,./:;<=>?[\\]^_`{|}~"

// escapeText renders s as the literal text it is: a plain-text field, not prose.
// It is what the commit SCOPE gets, because a scope is data — a subsystem name
// glyph prints in bold — and nothing in it should ever become markup.
//
// This is a flat byte loop and deliberately not a construct scanner: "the scope
// is a plain-text field" is exactly the statement that no grammar applies to it.
// Escaping the backslash itself is what keeps the pass self-consistent, so an
// authored backslash cannot eat the escaper's own.
//
// It is NOT idempotent, and a plain-text escaper cannot be. Call it once, at
// render, on the raw field — never on a value that has already been through it.
//
// A scope is whatever bytes a repository's pattern captured — v2 imposes no
// shape on the group, so anything but a parenthesis can arrive here. Measured,
// that is not hypothetical — "…​fix(<i title=\"x\">): …" linted clean and put a
// live tag in a release body; the v1 fleet carried 139 non-kebab scopes and
// every one renders identically escaped.
//
// TWO EXCLUSIONS, both load-bearing:
//
//   - '-' is left alone. It is a marker only at the START of a line (a list
//     bullet, a setext underline, a thematic break) and the shipped line
//     templates never put a scope there — compose writes the "- " bullet as
//     raw markup in front. Escaping it would put a backslash into the raw
//     source of essentially every kebab-case scope in the fleet for zero
//     rendered difference. Excluding it keeps those scopes byte-identical.
//   - '@' is left alone, because escaping it is worse than useless twice over.
//     "\@octocat" is a LIVE mention (the backslash vanishes in rendering before
//     the mention post-processor looks), and the backslash would then trip
//     escapeMentions' escapesTheNextByte, which correctly writes a separating
//     space so its fence is not eaten — printing a visible "\ " for no safety at
//     all. A mention in a scope keeps being handled where it always was: in the
//     single escapeMentions pass over the assembled line.
func escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		// Every escapable byte is < 0x80 and every UTF-8 continuation byte is
		// >= 0x80, so this cannot fire in the middle of a rune.
		if strings.IndexByte(escapable, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escapeProseLine applies escapeProse's rules to the ASSEMBLED line s, which is
// the only context in which they are decidable. Detection reads the whole
// string; a backslash is inserted only where the construct it would disarm
// touches author prose, which prose reports for a byte range.
//
// escapeProse ran per field, and every one of its rules carries state or
// context that a field boundary truncated (t-9np1, measured on the shipped
// escaper):
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
// It returns the escaped line and pos, where pos[i] is the offset in the result
// of input byte i (and pos[len(s)] the end) — the fence pass needs it to find
// Mention's exempt ranges again after escaping has shifted everything right.
func escapeProseLine(s string, inSpan []bool, prose func(lo, hi int) bool) (string, []int) {
	var b strings.Builder
	b.Grow(len(s))
	pos := make([]int, len(s)+1)
	esc := func(lo, hi, at int) {
		if prose(lo, hi) {
			b.WriteByte('\\')
		}
		b.WriteByte(s[at])
	}
	for i := 0; i < len(s); {
		pos[i] = b.Len()
		// Inside a code span nothing is escaped: the bytes are inert, and this
		// is the one exemption the package grants. It is exact only because
		// codeSpans now models cmark's MAXBACKTICKS bound (markdown.go).
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
