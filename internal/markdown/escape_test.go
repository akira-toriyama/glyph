package markdown

import (
	"regexp"
	"strings"
	"testing"
)

// WHAT GITHUB ACTUALLY DOES WITH THE NEUTRALIZED FORMS — measured, not reasoned.
// Same probe as the table at the top of markdown_test.go. Observed 2026-07-21.
//
// A backslash-escaped '<' renders as the literal text and the backslash does not
// print. Byte-identical to the entity forms, in all three containers glyph ships
// into (nine probes, one result):
//
//	before \<i title="x"\> after      -> before &lt;i title="x"&gt; after
//	before &lt;i title="x"&gt; after  -> before &lt;i title="x"&gt; after
//	| \<i title="x"> |                -> <td>&lt;i title="x"&gt;</td>
//	inside a <details> block          -> same, and the block stays closed
//	before <i title="x"> after        -> LIVE <i>, and it leaks OUT of the
//	                                     paragraph and past </details>
//
// The three fates of a raw tag. The third is why this is a REPAIR and not only a
// guard — left raw, the author's own words are destroyed:
//
//	BEGIN <i> END / <kbd> / <img src=x>    LIVE (attributes filtered, tag kept)
//	BEGIN <script> END / <title> / <style> escaped by GFM's tagfilter (8 names)
//	BEGIN <notatag> END                    -> "BEGIN  END"   DELETED
//	BEGIN </details> END                   -> "BEGIN  END"   DELETED
//	BEGIN <!-- c --> END                   -> "BEGIN  END"   DELETED
//	BEGIN <!DOCTYPE html> END              -> "BEGIN  END"   DELETED
//
// Real subjects from this fleet, raw and escaped:
//
//	ThemedChip — MUI <Chip> + <kbd> compact token
//	  raw      "MUI  + " and a <kbd> leaking past the </li>
//	  escaped  "MUI &lt;Chip&gt; + &lt;kbd&gt;"
//	unwrap Optional<Any> before NSNull check
//	  raw      "unwrap Optional before NSNull check"
//	  escaped  "unwrap Optional&lt;Any&gt; before NSNull check"
//	user-defined themes via [overlay.theme.<name>]
//	  raw      "[overlay.theme.]"
//	  escaped  "[overlay.theme.&lt;name&gt;]"
//
// ESCAPING '<' IS NOT ENOUGH, and this is what forces the scheme rule. GFM's
// extended autolink needs no angle bracket at all:
//
//	go to \<http://a/x> now      -> &lt;<a href="http://a/x%3E">http://a/x&gt;</a>
//	                                STILL A LINK; the '>' is eaten into the href
//	go to http\://a/x now        -> plain text, no link, backslash not printed
//	go to www\.a.com now         -> plain text, no link
//	go to \http://a/x now        -> STILL A LINK  ← the backslash must go before
//	                                the ':', never in front of the token
//
// THE FOUR LIVE MENTIONS THIS CLOSES, each measured raw and through the whole
// pipeline (flatten -> escapeMarkup -> escapeMentions) inside a real notes line:
//
//	fix <http://a/`x> so @octocat can `see` it        LIVE -> inert
//	fix http://a/`x so @octocat can `see` it          LIVE -> inert
//	see [a](b`c) @octocat `d` end                     LIVE -> inert
//	callers to &#64;v1                                LIVE -> inert
//
// The first three are one bug: a construct that outranks a backtick consumes it,
// so a backtick-only scan believes a PHANTOM code span and leaves the mention
// inside it raw. Kill the construct and the phantom becomes a REAL span —
// measured, the at-sign then lands inside <code> and GitHub links nobody:
//
//	fix \<http\://a/`x> so @octocat can `see` it
//	  -> fix &lt;http://a/<code>x&gt; so @octocat can </code>see` it
//
// The fourth is a different mechanism with the same result: a character
// reference resolves AFTER inline parsing, so no fence can reach it — there is
// no at-sign in the source for the mention pattern to find. Escaping the '&' is
// the only thing that works.
//
// AND WHAT MUST NOT CHANGE — the approved policy is that a subject stays
// readable. Measured byte-identical raw and escaped:
//
//	keep `code spans` and *emphasis* working
//	  -> keep <code>code spans</code> and <em>emphasis</em> working

// TestFlatten pins the t-bz0r fix: every CommonMark line terminator becomes ONE
// space. A bare CR is the one that surprises — GitHub ends the line there, so an
// unflattened subject closed the notes list item and promoted the text behind it
// to a document-level heading (measured; the probe table above).
func TestFlatten(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a bare CR is a line terminator", "fix the thing\r## Fabricated Section", "fix the thing ## Fabricated Section"},
		{"CRLF collapses to ONE space", "a\r\nb", "a b"},
		{"LF", "a\nb", "a b"},
		{"a CR at the very end", "a\r", "a "},
		{"consecutive terminators each become a space", "a\r\n\r\nb", "a  b"},
		{"a lone CR between CRLFs is not swallowed", "a\r\nb\rc\r\nd", "a b c d"},
		{"nothing to flatten passes through", "plain subject", "plain subject"},
		{"a literal backslash-r is text, not a terminator", `a\rb`, `a\rb`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := flatten(c.in); got != c.want {
				t.Errorf("flatten(%q) = %q, want %q", c.in, got, c.want)
			}
			if strings.ContainsAny(flatten(c.in), "\r\n") {
				t.Errorf("flatten(%q) left a line terminator behind", c.in)
			}
		})
	}
}

// TestEscapeMarkup pins the neutralization contract: the constructs that can
// inject structure, point somewhere the author never wrote, delete the author's
// words, or steal a code-span delimiter come out escaped — and everything else,
// most of all the author's code spans and emphasis, passes through untouched.
// escapeMarkup is the all-prose specialization of escapeProseLine: one string,
// every byte author prose, spans computed from the string itself. Production
// composes lines instead (a code span pairs ACROSS fields, t-9np1), so this
// lives here — it is the shape in which the single-string invariants below are
// stateable, and nothing ships it.
func escapeMarkup(s string) string {
	prose := func(lo, hi int) bool { return true }
	out, _ := escapeProseLine(s, spansBehindOpeners(s, prose), prose)
	return out
}

func TestEscapeMarkup(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// Raw HTML. No grammar test — every prose '<' is escaped, so a shape
		// glyph would have had to recognize cannot be one it got wrong.
		{"an open tag", `add a tracker <img src="x">`, `add a tracker \<img src="x">`},
		{"a closing tag breaking out of a container", "speed up </details> the loop", `speed up \</details> the loop`},
		{"a tag GitHub deletes today", "MUI <Chip> compact token", `MUI \<Chip> compact token`},
		{"an HTML comment", "close a line-initial <!-- before the block", `close a line-initial \<!-- before the block`},
		{"a declaration", "reject <!DOCTYPE html> input", `reject \<!DOCTYPE html> input`},
		{"a '<' that forms no construct at all is escaped anyway", "docs < runtime", `docs \< runtime`},
		{"a generic-looking type", "unwrap Optional<Any> before NSNull", `unwrap Optional\<Any> before NSNull`},

		// Angle autolinks: the '<' rule already kills them.
		{"a URI autolink", "see <http://a/x> now", `see \<http\://a/x> now`},
		{"an email autolink", "see <a@b.c> now", `see \<a@b.c> now`},

		// Extended (bracket-less) autolinks — the half a '<' rule cannot reach.
		{"a bare http URL", "go to http://a/x now", `go to http\://a/x now`},
		{"a bare https URL", "go to https://a/x now", `go to https\://a/x now`},
		{"ftp too", "go to ftp://a/x now", `go to ftp\://a/x now`},
		{"schemes are case-insensitive at GitHub", "go to HTTP://EVIL.COM now", `go to HTTP\://EVIL.COM now`},
		{"www with no scheme", "go to www.a.com now", `go to www\.a.com now`},
		{"www is case-insensitive too", "go to WWW.a.com now", `go to WWW\.a.com now`},
		{"a colon that opens no scheme is left alone", "fix: the thing", "fix: the thing"},
		{"a dot that follows no www is left alone", "bump v1.2.3 to v1.2.4", "bump v1.2.3 to v1.2.4"},
		{"over-escaping on the safe side: a scheme suffix GitHub would not link", "xhttp://a", `xhttp\://a`},

		// Link, image and footnote openers. A link DESTINATION consumes a
		// backtick, which is t-dwra wearing brackets.
		{"an injected link", "see [x](http://evil) now", `see \[x](http\://evil) now`},
		{"an image", "render ![]() images from glossary.md", `render !\[]() images from glossary.md`},
		{"a link destination stealing a backtick", "see [a](b`c) @octocat `d` end", "see \\[a](b`c) @octocat `d` end"},
		{"a TOML table renders identically escaped", "warn on unknown [options] keys", `warn on unknown \[options] keys`},

		// Character references — the one rule aimed at a construct that cannot
		// steal a backtick. It closes a mention hole no fence can reach.
		{"a decimal character reference", "callers to &#64;v1", `callers to \&#64;v1`},
		{"a hex character reference", "callers to &#x40;v1", `callers to \&#x40;v1`},
		{"a named character reference", "A &amp; B", `A \&amp; B`},
		{"an unknown name is escaped too (shape, not name)", "see &notareal; here", `see \&notareal; here`},
		{"a bare ampersand is not a reference", "A & B", "A & B"},
		{"an unterminated reference is not one", "A &amp B", "A &amp B"},
		// Nor is the width tested. GitHub decodes a numeric reference of up to eight
		// digits in either radix and leaves nine as its own text (POST /markdown,
		// 2026-11-04: "&#00000064;octocat", "&#x00000040;octocat" and
		// "&#X00000040;octocat" render a live @octocat; "&#000000064;octocat" and
		// "&#x000000040;octocat" render no mention). The escape owes nothing to that
		// limit: a reference of any width is escaped, and the ones GitHub would not
		// have decoded render the same with the backslash as without it.
		{"an 8-digit decimal reference", "cc &#00000064;octocat", `cc \&#00000064;octocat`},
		{"an 8-digit hex reference", "cc &#x00000040;octocat", `cc \&#x00000040;octocat`},
		{"an 8-digit hex reference, capital X", "cc &#X00000040;octocat", `cc \&#X00000040;octocat`},
		{"a 9-digit decimal reference GitHub leaves alone", "cc &#000000064;octocat", `cc \&#000000064;octocat`},
		{"a 9-digit hex reference GitHub leaves alone", "cc &#x000000040;octocat", `cc \&#x000000040;octocat`},
		{"the longest name GitHub knows", "x &CounterClockwiseContourIntegral; y", `x \&CounterClockwiseContourIntegral; y`},
		{"a name longer than any GitHub knows", "x &" + strings.Repeat("a", 56) + "; y", `x \&` + strings.Repeat("a", 56) + "; y"},

		// A block opener at a block start (rule 5). This table holds the rule's
		// single-string invariants; TestProseAtABlockStartOpensNoBlock its shapes.
		{"a heading", "## x", `\## x`},
		{"a block quote", "> x", `\> x`},
		{"a list marker", "- x", `\- x`},
		{"an ordered list marker takes the backslash on its terminator", "1. x", `1\. x`},
		{"a backtick fence", "``` x", "\\``` x"},
		{"a tilde fence", "~~~ x", `\~~~ x`},
		{"a thematic break", "---", `\---`},
		{"an indent is walked past, and only a Line drops it", "    ## x", `    \## x`},
		{"each line of the string starts blocks of its own", "a\r\r# b\n> c", "a\r\r\\# b\n\\> c"},
		{"an opener is not one mid-line", "a ## b - c > d 1. e", "a ## b - c > d 1. e"},
		{"an authored escape already disarms it", `\## x`, `\## x`},

		// What must survive. This is the approved policy.
		{"a code span passes through byte-for-byte", "handle `nil` table", "handle `nil` table"},
		{"emphasis is left working", "keep *bold* and _italic_ verbatim", "keep *bold* and _italic_ verbatim"},
		{"a construct INSIDE a code span is not escaped", "run `<img src=x>` safely", "run `<img src=x>` safely"},
		{"a URL inside a code span is not escaped", "run `http://a/x` safely", "run `http://a/x` safely"},
		{"CJK and pipes are untouched", "align the 日本語 label | keep it", "align the 日本語 label | keep it"},
		{"a lone backtick is not a span, so prose around it is escaped", "a lone ` and <b> here", "a lone ` and \\<b> here"},
		{"an at-sign is left for the mention pass", "callers to @v1", "callers to @v1"},

		// The author's own escapes.
		{"an authored escape is preserved, not doubled", `already \<escaped> here`, `already \<escaped> here`},
		{"a trailing lone backslash", `ends with a backslash \`, `ends with a backslash \`},
		{"an escaped backslash still guards the next byte", `a \\ b`, `a \\ b`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := escapeMarkup(c.in)
			if got != c.want {
				t.Errorf("escapeMarkup(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			// Additive: the author's bytes survive in order. The notes must
			// never silently drop what a commit said — which is the very thing
			// GitHub's sanitizer does today.
			if !isSubsequence(c.in, got) {
				t.Errorf("escapeMarkup(%q) = %q dropped or rewrote a byte", c.in, got)
			}
			// A fixed point: one pass is enough, and the second changes nothing.
			if again := escapeMarkup(got); again != got {
				t.Errorf("escapeMarkup is not a fixed point on %q:\n first %q\nsecond %q", c.in, got, again)
			}
		})
	}
}

// TestEscapeMarkupLeavesTheCodeSpanMapAlone is the load-bearing half of the
// argument that ONE pass suffices. escapeMarkup skips the code spans of its
// INPUT, so if escaping could move the span map, the map it skipped by would be
// the wrong one and a construct could survive inside a span that no longer
// exists. On one line it cannot: every inserted byte is a backslash placed in
// front of a '<', '[', ':', '.', '&' or a block opener, and the one opener that
// is a backtick is a fence's run with no backtick behind it on the line, which
// pairs with nothing either way (escape.go, WHY ONE PASS IS ENOUGH;
// spansBehindOpeners has the one shape that needs more lines to differ).
func TestEscapeMarkupLeavesTheCodeSpanMapAlone(t *testing.T) {
	for _, in := range []string{
		"fix <http://a/`x> so @octocat can `see` it",
		"a `b` <http://x/`y> c",
		"<i title=\"`\"> b `c` d",
		"a ` b @octocat c `` d",
		"`` `a` `x @octocat y`",
		"see [a](b`c) @octocat `d` end",
		"http://a/`x and `y` and www.b/`z",
		`\<a> ` + "`b` <c>",
		"``` no partner",
		"```` no partner",
		"```a``` b",
		"``` a ` b",
		"``` a ``` b `c`",
		"## a `b`",
		"1. a `b`",
		"> a `b`",
	} {
		t.Run(in, func(t *testing.T) {
			before, after := codeSpans(in), codeSpans(escapeMarkup(in))
			if len(before) != len(after) {
				t.Fatalf("escapeMarkup changed the number of code spans in %q: %d -> %d", in, len(before), len(after))
			}
			out := escapeMarkup(in)
			for i := range before {
				b, a := in[before[i][0]:before[i][1]], out[after[i][0]:after[i][1]]
				if b != a {
					t.Errorf("code span %d moved: %q -> %q", i, b, a)
				}
			}
		})
	}
}

// FuzzEscapeMarkupNeutralizes states the two properties the design rests on over
// arbitrary input, plus the one that actually makes the escaping safe.
//
// It deliberately does NOT try to be a second renderer — that is the mistake the
// mention oracle's own history records. It asserts what can be stated exactly:
//
//  1. ADDITIVE. The input is a subsequence of the output, so a commit's words
//     can never be dropped or rewritten.
//  2. A FIXED POINT. A second pass changes nothing.
//  3. NO SURVIVING TRIGGER IN PROSE. Under the premise that the input holds no
//     backtick — so no code span can exist and every byte is prose — every '<',
//     '[' and entity-shaped '&' in the output is preceded by an odd run of
//     backslashes, and no unbroken "http://", "https://", "ftp://" or "www."
//     survives. The premise is what keeps this honest: inside an author's code
//     span nothing is escaped, by design, so the invariant would be false there.
func FuzzEscapeMarkupNeutralizes(f *testing.F) {
	for _, s := range []string{
		"fix <http://a/`x> so @octocat can `see` it",
		"fix http://a/`x so @octocat can `see` it",
		"see [a](b`c) @octocat `d` end",
		"callers to &#64;v1",
		"speed up </details><h1>OWNED</h1> the loop",
		`already \<escaped> here`,
		"www.a.com and WWW.b.com",
		"a\rb",
		"",
		"<",
		`\`,
		"&#;",
		"cc &#x00000040;octocat",
		"## x",
		"1. x",
		"``` x",
		"    x",
		"---",
		"a\r\r# b",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := escapeMarkup(s)
		if !isSubsequence(s, out) {
			t.Fatalf("escapeMarkup(%q) = %q dropped or rewrote a byte", s, out)
		}
		if again := escapeMarkup(out); again != out {
			t.Fatalf("escapeMarkup is not a fixed point on %q: %q -> %q", s, out, again)
		}
		if strings.Contains(s, "`") {
			return // the premise: with no backtick there is no code span
		}
		for i := 0; i < len(out); i++ {
			switch {
			case out[i] == '<', out[i] == '[':
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("escapeMarkup(%q) = %q left %q live at %d", s, out, out[i], i)
				}
			case out[i] == '&' && entityShape.MatchString(out[i:]):
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("escapeMarkup(%q) = %q left an entity live at %d", s, out, i)
				}
			}
		}
		for _, trigger := range []string{"http://", "https://", "ftp://", "www."} {
			if idx := indexFold(out, trigger); idx >= 0 {
				t.Fatalf("escapeMarkup(%q) = %q left %q unbroken at %d", s, out, trigger, idx)
			}
		}
	})
}

// entityShape is the reference shape the fuzz oracles judge with, anchored at the
// ampersand. It is written out rather than calling entityAt, for the reason
// oddBackslashRunBefore gives: asked through entityAt, a cap on the width caps the
// oracle with it, and the corpus entry hex8-reference-pages-past-a-width-cap
// passes over a live mention.
var entityShape = regexp.MustCompile(`^&(?:#[0-9]+|#[xX][0-9A-Fa-f]+|[A-Za-z][A-Za-z0-9]+);`)

// oddBackslashRunBefore reports whether an ODD number of backslashes ends just
// before i, which is what makes the byte at i escaped rather than syntax. It is
// written out rather than reusing escapesTheNextByte so the invariant does not
// borrow its judgement from the code under test.
func oddBackslashRunBefore(s string, i int) bool {
	n := 0
	for i-n > 0 && s[i-n-1] == '\\' {
		n++
	}
	return n%2 == 1
}

// indexFold is strings.Index, case-insensitively — GitHub links "HTTP://EVIL.COM".
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
