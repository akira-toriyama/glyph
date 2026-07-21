package markdown

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
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
// guard — the author's own words are destroyed today:
//
//	BEGIN <i> END / <kbd> / <img src=x>    LIVE (attributes filtered, tag kept)
//	BEGIN <script> END / <title> / <style> escaped by GFM's tagfilter (8 names)
//	BEGIN <notatag> END                    -> "BEGIN  END"   DELETED
//	BEGIN </details> END                   -> "BEGIN  END"   DELETED
//	BEGIN <!-- c --> END                   -> "BEGIN  END"   DELETED
//	BEGIN <!DOCTYPE html> END              -> "BEGIN  END"   DELETED
//
// Real subjects already shipped by this fleet, before and after:
//
//	ThemedChip — MUI <Chip> + <kbd> compact token
//	  before  "MUI  + " and a <kbd> leaking past the </li>
//	  after   "MUI &lt;Chip&gt; + &lt;kbd&gt;"
//	unwrap Optional<Any> before NSNull check
//	  before  "unwrap Optional before NSNull check"
//	  after   "unwrap Optional&lt;Any&gt; before NSNull check"
//	user-defined themes via [overlay.theme.<name>]
//	  before  "[overlay.theme.]"
//	  after   "[overlay.theme.&lt;name&gt;]"
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
// THE FOUR LIVE MENTIONS THIS CLOSES, each measured before and after the whole
// pipeline (Flatten -> EscapeMarkup -> EscapeMentions) inside a real notes line:
//
//	fix <http://a/`x> so @octocat can `see` it        LIVE -> inert
//	fix http://a/`x so @octocat can `see` it          LIVE -> inert
//	see [a](b`c) @octocat `d` end                     LIVE -> inert
//	callers to &#64;v1                                LIVE -> inert
//
// The first three are one bug: a construct that outranks a backtick consumes it,
// so glyph believed a PHANTOM code span and left the mention inside it raw. Kill
// the construct and the phantom becomes a REAL span — measured, the at-sign now
// lands inside <code> and GitHub links nobody:
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
// readable. Measured byte-identical before and after:
//
//	keep `code spans` and *emphasis* working
//	  -> keep <code>code spans</code> and <em>emphasis</em> working
// ─────────────────────────────────────────────────────────────────────────────

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
			if got := Flatten(c.in); got != c.want {
				t.Errorf("Flatten(%q) = %q, want %q", c.in, got, c.want)
			}
			if strings.ContainsAny(Flatten(c.in), "\r\n") {
				t.Errorf("Flatten(%q) left a line terminator behind", c.in)
			}
		})
	}
}

// TestEscapeMarkup pins the neutralization contract: the constructs that can
// inject structure, point somewhere the author never wrote, delete the author's
// words, or steal a code-span delimiter come out escaped — and everything else,
// most of all the author's code spans and emphasis, passes through untouched.
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
			got := EscapeMarkup(c.in)
			if got != c.want {
				t.Errorf("EscapeMarkup(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			// Additive: the author's bytes survive in order. The notes must
			// never silently drop what a commit said — which is the very thing
			// GitHub's sanitizer does today.
			if !isSubsequence(c.in, got) {
				t.Errorf("EscapeMarkup(%q) = %q dropped or rewrote a byte", c.in, got)
			}
			// A fixed point: one pass is enough, and the second changes nothing.
			if again := EscapeMarkup(got); again != got {
				t.Errorf("EscapeMarkup is not a fixed point on %q:\n first %q\nsecond %q", c.in, got, again)
			}
		})
	}
}

// TestEscapeMarkupLeavesTheCodeSpanMapAlone is the load-bearing half of the
// argument that ONE pass suffices. EscapeMarkup skips the code spans of its
// INPUT, so if escaping could move the span map, the map it skipped by would be
// the wrong one and a construct could survive inside a span that no longer
// exists. It cannot: every inserted byte is a backslash placed in front of a
// '<', '[', ':', '.' or '&' — never in front of a backtick — so backtick runs
// keep their lengths and their order.
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
	} {
		t.Run(in, func(t *testing.T) {
			before, after := codeSpans(in), codeSpans(EscapeMarkup(in))
			if len(before) != len(after) {
				t.Fatalf("EscapeMarkup changed the number of code spans in %q: %d -> %d", in, len(before), len(after))
			}
			out := EscapeMarkup(in)
			for i := range before {
				b, a := in[before[i][0]:before[i][1]], out[after[i][0]:after[i][1]]
				if b != a {
					t.Errorf("code span %d moved: %q -> %q", i, b, a)
				}
			}
		})
	}
}

// TestEscapeText pins the plain-text contract for the commit SCOPE: every ASCII
// punctuation byte comes out escaped, except the two that must not be.
//
// Only the LEGACY token grammar can deliver a scope with anything to escape —
// the canonical slot is lowercase kebab-case — so the interesting cases are all
// legacy ones. Measured across the fleet: 139 legacy scopes are non-kebab, and
// every one of them renders identically escaped.
func TestEscapeText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a canonical scope is untouched", "parser", "parser"},
		{"a hyphen is NOT escaped — it is the one punctuation a kebab scope carries", "grid-1f-4", "grid-1f-4"},
		{"a digit-carrying scope", "v2", "v2"},
		{"a stray backtick, which today steals the mention fence", "readme`", "readme\\`"},
		{"a raw tag in the scope", `<i title="x">`, `\<i title\=\"x\"\>`},
		{"a link in the scope", "[x](http://evil)", `\[x\]\(http\:\/\/evil\)`},
		{"a dotted scope", "a.b", `a\.b`},
		{"a comma-separated legacy scope", "ThemeKit,prism", `ThemeKit\,prism`},
		{"emphasis markers", "a*b_c~d", `a\*b\_c\~d`},
		{"an ampersand", "A&B", `A\&B`},
		{"a backslash escapes itself, so it cannot eat ours", `a\b`, `a\\b`},
		{"an at-sign is left for the mention pass", "@octocat", "@octocat"},
		{"non-ASCII is untouched", "日本語", "日本語"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EscapeText(c.in); got != c.want {
				t.Errorf("EscapeText(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// TestEscapeTextLeavesNoConstruct is the falsifiable statement of what "plain
// text" means, and it is what makes EscapeMarkup(subject) sound in isolation: a
// scope can no longer open a span, a tag or a link that reaches across the
// "**…:** " boundary into the subject beside it.
func TestEscapeTextLeavesNoConstruct(t *testing.T) {
	for _, in := range []string{
		"readme`",
		"a `b` c",
		`<i title="x">`,
		"[x](http://evil)",
		"http://a/x",
		"www.a.com",
		"a``b",
		`\<not escaped>`,
	} {
		t.Run(in, func(t *testing.T) {
			out := EscapeText(in)
			if spans := codeSpans(out); len(spans) != 0 {
				t.Errorf("EscapeText(%q) = %q still forms code span(s) %v", in, out, spans)
			}
			// EscapeMarkup is the independent judge of "does a construct start
			// here": if none does, it has nothing to add.
			if again := EscapeMarkup(out); again != out {
				t.Errorf("EscapeText(%q) = %q still carries a construct — EscapeMarkup would add %d byte(s)", in, out, len(again)-len(out))
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
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := EscapeMarkup(s)
		if !isSubsequence(s, out) {
			t.Fatalf("EscapeMarkup(%q) = %q dropped or rewrote a byte", s, out)
		}
		if again := EscapeMarkup(out); again != out {
			t.Fatalf("EscapeMarkup is not a fixed point on %q: %q -> %q", s, out, again)
		}
		if strings.Contains(s, "`") {
			return // the premise: with no backtick there is no code span
		}
		for i := 0; i < len(out); i++ {
			switch {
			case out[i] == '<', out[i] == '[':
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("EscapeMarkup(%q) = %q left %q live at %d", s, out, out[i], i)
				}
			case out[i] == '&' && entityAt(out, i):
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("EscapeMarkup(%q) = %q left an entity live at %d", s, out, i)
				}
			}
		}
		for _, trigger := range []string{"http://", "https://", "ftp://", "www."} {
			if idx := indexFold(out, trigger); idx >= 0 {
				t.Fatalf("EscapeMarkup(%q) = %q left %q unbroken at %d", s, out, trigger, idx)
			}
		}
	})
}

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
