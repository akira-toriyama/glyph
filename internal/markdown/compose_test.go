package markdown

import (
	"strings"
	"testing"
)

// TestLineSealsTheEscapeOrder pins the property Line exists for:
// flattening runs inside Prose as each field arrives, and the prose escape and
// then the mention fence run LAST, inside String, over the whole assembled
// line — including Raw stretches and sized against backticks that other
// fragments contributed. The mutation ledger names this test: a String that
// returns its bytes unfenced turns every release-notes line and preview cell
// into a potential live @mention, which is a notification to a stranger
// (incident t-hykw), not a rendering nit.
func TestLineSealsTheEscapeOrder(t *testing.T) {
	t.Run("fence runs last and over the assembled line", func(t *testing.T) {
		// The measured incident shape (markdown_test.go's probe table,
		// 2026-07-21): the scope carries a backtick, the subject carries the
		// mentions, and only a fence sized over the ASSEMBLED line beats the
		// run the scope smuggled into the shared inline context.
		var l Line
		l.Raw("- 🐛 ")
		l.Raw("**")
		l.Prose("readme`")
		l.Raw(":** ")
		l.Prose("credit @alice and @bob for the fix")
		l.Raw(" (abc1234)")
		got := l.String()
		// The scope is prose like every author field (DESIGN §2), so its
		// backtick reaches the line live and counts for fence sizing: the
		// fence is two, and both mentions are fenced, not just the subject's.
		want := "- 🐛 **readme`:** credit ``@alice`` and ``@bob`` for the fix (abc1234)"
		if got != want {
			t.Fatalf("composed line:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("the fence covers Raw stretches too", func(t *testing.T) {
		var l Line
		l.Raw("cc @octocat")
		if got, want := l.String(), "cc `@octocat`"; got != want {
			t.Fatalf("got %q, want %q — a Raw stretch left out of the fence pass is a mention hole", got, want)
		}
	})

	t.Run("Prose fields are flattened and disarmed over the line", func(t *testing.T) {
		var l Line
		l.Prose("a\nb<")
		l.Raw(" ")
		l.Prose("keep `code` kill\n<i>")
		got := l.String()
		// Both fields: flattened, the author's span survives, every raw-HTML
		// opener outside it does not.
		want := `a b\< keep ` + "`code`" + ` kill \<i>`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("Mention is the one hole, and only for a whole handle", func(t *testing.T) {
		var l Line
		l.Raw("- ")
		l.Prose("thank @someone for the report")
		l.Raw(" @")
		l.Mention("akira-toriyama", "akira-toriyama")
		got := l.String()
		// The subject's stranger is fenced; the credited author is live.
		want := "- thank `@someone` for the report @akira-toriyama"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("Mention goes live on the login alone, and a credit that cannot page drops its at-sign", func(t *testing.T) {
		// login is what GitHub established; name is git's free-text %an. Only
		// a whole handle-shaped LOGIN goes live — never the name, whatever its
		// shape (t-39fy: "Saleh" is github.com/larrasket) — and a credit
		// written as prose takes the template's at-sign with it.
		for _, c := range []struct{ login, name, want string }{
			{"larrasket", "Saleh", "by @larrasket"},
			{"", "Saleh", "by Saleh"}, // handle-shaped NAME, no identity: a stranger is not paged
			{"", "Akira Toriyama", "by Akira Toriyama"},
			{"", "Robert Pająk", "by Robert Pająk"},
			{"dependabot[bot]", "dependabot[bot]", `by dependabot\[bot]`},
			{"-lead-hyphen", "-lead-hyphen", "by -lead-hyphen"}, // no handle starts on a hyphen, at GitHub or here
			{"", "", "by "},
			{"", "ping @bob", "by ping `@bob`"}, // a name carrying its own mention is still fenced as prose
		} {
			var l Line
			l.Raw("by @")
			l.Mention(c.login, c.name)
			if got := l.String(); got != c.want {
				t.Fatalf("Mention(%q, %q): got %q, want %q", c.login, c.name, got, c.want)
			}
		}
		// The at-sign is dropped only from a Raw part directly before the
		// credit: a PROSE at-sign is author bytes, kept — and the token it
		// then assembles with the plain name is a would-be mention the fence
		// catches, so the stranger is still not paged.
		var l Line
		l.Prose("cc @")
		l.Mention("", "Saleh")
		if got, want := l.String(), "cc `@Saleh`"; got != want {
			t.Fatalf("a prose at-sign is not the template's: got %q, want %q", got, want)
		}
	})

	t.Run("prose glued onto a Mention re-arms the fence", func(t *testing.T) {
		// Template text extending the name assembles a token nobody vouched
		// for ("@akiras"); the partial overlap must fence it whole rather than
		// let the exemption widen.
		var l Line
		l.Raw("cc @")
		l.Mention("akira", "akira")
		l.Raw("s")
		if got, want := l.String(), "cc `@akiras`"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("a Mention does not blind the fence around it", func(t *testing.T) {
		// The exempt bytes still count for sizing and context: a backtick
		// elsewhere on the line must still grow the fence on the stranger.
		var l Line
		l.Prose("x`")
		l.Raw(" @")
		l.Mention("akira", "akira")
		l.Raw(" ")
		l.Prose("ping @bob")
		got := l.String()
		if !strings.Contains(got, "``@bob``") {
			t.Fatalf("fence not sized over the whole line: %q", got)
		}
		if !strings.Contains(got, "@akira ") {
			t.Fatalf("the live mention lost its exemption: %q", got)
		}
	})

	t.Run("String repeats", func(t *testing.T) {
		var l Line
		l.Prose("thanks @octocat")
		first := l.String()
		if second := l.String(); second != first {
			t.Fatalf("String is not repeatable: first %q, then %q", first, second)
		}
		if !strings.Contains(first, "`@octocat`") {
			t.Fatalf("mention not fenced: %q", first)
		}
	})
}

// TestProseAtABlockStartOpensNoBlock pins rule 5 (escape.go) over every container
// a template can open with: "- ## x" is a heading inside the item at GitHub
// whichever bullet stands in front of it, so the template's bullet protects
// nothing. Measured 2026-11-04 through `glyph notes --range` on the gemoji preset
// with an exclude_authors commit, then POST /markdown on each line: raw, each shape
// below opens the block blockOpenerAt's table names; escaped, it renders as its own
// text in the item, with the credit behind it. The empty prefix is a template with
// no bullet, and preview's table cell, which goes through the same Line and renders
// the same either way (measured raw and escaped in a cell).
func TestProseAtABlockStartOpensNoBlock(t *testing.T) {
	prefixes := []string{"- ", "* ", "+ ", "1. ", "1) ", "> ", "- > ", "> - ", "- 1. ", "   - ", "-   ", "-\t", ""}
	line := func(prefix, value string, credited bool) string {
		var l Line
		l.Raw(prefix)
		l.Prose(value)
		if credited {
			l.Raw(" @")
			l.Mention("", "dependabot[bot]")
		}
		got := l.String()
		if again := l.String(); again != got {
			t.Fatalf("String is not repeatable on %q: %q, then %q", value, got, again)
		}
		return got
	}
	const credit = ` dependabot\[bot]`

	t.Run("an opener that does not need the end of the line", func(t *testing.T) {
		for _, c := range []struct{ value, want string }{
			{"# x", `\# x`},
			{"## Breaking Changes", `\## Breaking Changes`},
			{"###### x", `\###### x`},
			{"####### x", `\####### x`}, // no heading at GitHub: over-escaped, renders the same
			{"#", `\#`},
			{"#\tx", "\\#\tx"},
			{"> x", `\> x`},
			{">x", `\>x`},
			{">", `\>`},
			{"> [!WARNING] x", `\> \[!WARNING] x`},
			{"1. x", `1\. x`},
			{"1) x", `1\) x`},
			{"2. x", `2\. x`},
			{"123456789. x", `123456789\. x`},
			{"1234567890. x", `1234567890\. x`}, // no list at GitHub: over-escaped
			{"1.", `1\.`},
			{"1)", `1\)`},
			{"1.\tx", "1\\.\tx"},
			{"- x", `\- x`},
			{"* x", `\* x`},
			{"+ x", `\+ x`},
			{"-", `\-`},
			{"*", `\*`},
			{"+", `\+`},
			{"-\tx", "\\-\tx"},
			{"*\tx", "\\*\tx"},
			{"+\tx", "\\+\tx"},
			{"- - x", `\- - x`},
			{"- - -", `\- - -`},
			{"* * *", `\* * *`},
			{"``` x", "\\``` x"},
			{"```", "\\```"},
			{"```x", "\\```x"},
			{"````", "\\````"},
			{"~~~ x", `\~~~ x`},
			{"~~~", `\~~~`},
			{"~~~x", `\~~~x`},
			{"~~~~", `\~~~~`},
			{"~~~x~~~", `\~~~x~~~`},
		} {
			for _, p := range prefixes {
				if got, want := line(p, c.value, false), p+c.want; got != want {
					t.Errorf("Raw(%q) + Prose(%q):\n got %q\nwant %q", p, c.value, got, want)
				}
				if got, want := line(p, c.value, true), p+c.want+credit; got != want {
					t.Errorf("Raw(%q) + Prose(%q) + credit:\n got %q\nwant %q", p, c.value, got, want)
				}
			}
		}
	})

	t.Run("an opener the rest of the line disarms", func(t *testing.T) {
		// Only markers and blanks to the end of the line make these one, so a
		// credit behind the value leaves a paragraph that needs no backslash.
		for _, c := range []struct{ value, want string }{
			{"---", `\---`},
			{"--", `\--`},
			{"-- -", `\-- -`},
			{"***", `\***`},
			{"** *", `\** *`},
			{"**", `\**`}, // no break at GitHub: over-escaped, renders the same
			{"___", `\___`},
			{"_ _ _", `\_ _ _`},
			{"__ _", `\__ _`},
			{"__", `\__`},
			{"_", `\_`},
			{"===", `\===`},
			{"=", `\=`},
			{"= =", `\= =`},
		} {
			for _, p := range prefixes {
				if got, want := line(p, c.value, false), p+c.want; got != want {
					t.Errorf("Raw(%q) + Prose(%q):\n got %q\nwant %q", p, c.value, got, want)
				}
				if got, want := line(p, c.value, true), p+c.value+credit; got != want {
					t.Errorf("Raw(%q) + Prose(%q) + credit:\n got %q\nwant %q", p, c.value, got, want)
				}
			}
		}
	})

	t.Run("an indent is dropped, where no backslash can reach it", func(t *testing.T) {
		for _, c := range []struct{ value, want string }{
			{" x", "x"},
			{"   x", "x"},
			{"    x", "x"},
			{"     x", "x"},
			{"\tx", "x"},
			{"\t\tx", "x"},
			{"  \t  x", "x"},
			{"   ## x", `\## x`},
			{"    ## x", `\## x`},
			{"\t- x", `\- x`},
			{"\r## x", `\## x`}, // flattened to a space first, then dropped
			{"x    y", "x    y"},
		} {
			for _, p := range prefixes {
				if got, want := line(p, c.value, true), p+c.want+credit; got != want {
					t.Errorf("Raw(%q) + Prose(%q):\n got %q\nwant %q", p, c.value, got, want)
				}
			}
		}
	})

	t.Run("what opens no block stays byte for byte", func(t *testing.T) {
		for _, value := range []string{
			// The approved policy: inline syntax at the head of a value keeps working.
			"*x*", "**x**", "*** x ***", "_x_", "__x__", "~~x~~", "~x~", "`a`", "``a``",
			"```a```", "```a``` b", "``` a ` b",
			// Bytes a marker would need a blank behind.
			"#64 fix", "#x", "-x", "--flag", "+1 x", "1.2.3 released", "1.x", "=x",
			"|a|b|", "plain text",
			// A matched subject under the presets opens with its gitmoji.
			":bug:~ ## x", ":bug:~ > x", ":bug:~ 1. x", ":bug:~ - x", ":bug:~     x",
			":bug:~ ``` x", ":bug:~ ~~~ x", ":bug:~ ---", ":bug:(a-b)~ *x* _y_ `z`",
		} {
			for _, p := range prefixes {
				if got, want := line(p, value, false), p+value; got != want {
					t.Errorf("Raw(%q) + Prose(%q):\n got %q\nwant %q", p, value, got, want)
				}
				if got, want := line(p, value, true), p+value+credit; got != want {
					t.Errorf("Raw(%q) + Prose(%q) + credit:\n got %q\nwant %q", p, value, got, want)
				}
			}
		}
	})

	t.Run("the mention fence still runs over a disarmed line", func(t *testing.T) {
		// Disarming the opener makes the rest of the line a live paragraph:
		// "\~~~@octocat" links @octocat (measured), where "~~~@octocat" read the
		// handle as a fence's info string.
		for _, c := range []struct{ value, want string }{
			{"@octocat", "`@octocat`"},
			{"# @octocat", "\\# `@octocat`"},
			{"> @octocat", "\\> `@octocat`"},
			{">@octocat", "\\>`@octocat`"},
			{"- @octocat", "\\- `@octocat`"},
			{"1. @octocat", "1\\. `@octocat`"},
			{"~~~ @octocat", "\\~~~ `@octocat`"},
			{"~~~@octocat", "\\~~~`@octocat`"},
			{"``` @octocat", "\\``` ````@octocat````"},
			{"```@octocat", "\\```@octocat"}, // a backtick glued to the at-sign shields it
		} {
			if got, want := line("- ", c.value, true), "- "+c.want+credit; got != want {
				t.Errorf("Prose(%q):\n got %q\nwant %q", c.value, got, want)
			}
		}
	})
}

// TestBlockOpenerOwnership: a construct the template wrote in full is the
// template's, a value behind one is at a block start only if that construct can
// hold a block, and a construct a value touches is escaped whichever byte the rule
// fires on (escape.go, as for the inline rules).
func TestBlockOpenerOwnership(t *testing.T) {
	type field struct {
		kind partKind
		s    string
	}
	raw := func(s string) field { return field{partRaw, s} }
	prose := func(s string) field { return field{partProse, s} }
	for _, c := range []struct {
		name   string
		fields []field
		want   string
	}{
		{"the template's own heading holds inline text", []field{raw("## "), prose("## x")}, "## ## x"},
		{"the template's own break", []field{raw("---")}, "---"},
		{"the template's own nested lists", []field{raw("- - "), prose("x")}, "- - x"},
		{"a value behind the template's text is inline", []field{raw("- x "), prose("## y")}, "- x ## y"},
		{"a value behind a checkbox is inline", []field{raw("- [ ] "), prose("## y")}, "- [ ] ## y"},
		{"a value behind the template's bold is inline", []field{raw("- **"), prose("a"), raw(":** "), prose("## y")}, "- **a:** ## y"},
		{"a second value is inline", []field{raw("- "), prose("x "), prose("## y")}, "- x ## y"},
		{"a second value keeps its indent", []field{raw("- "), prose("x"), prose("    y")}, "- x    y"},
		{"an empty value leaves the next at the block start", []field{raw("- "), prose(""), prose("## y")}, `- \## y`},
		{"a blank value leaves the next at the block start", []field{raw("- "), prose("  "), prose("    ## y")}, `- \## y`},
		{"a break straddling two values", []field{prose("--"), prose("-")}, `\---`},
		{"a break a value starts and the template ends", []field{raw("- "), prose("-"), raw("--")}, `- \---`},
		{"a break the template starts and a value ends", []field{raw("- -"), prose("--")}, `- \---`},
		{"a marker a value's digit and the template's dot make", []field{raw("- "), prose("1"), raw(". x")}, `- 1\. x`},
		{"a fence the template starts and a value ends", []field{raw("``"), prose("` x")}, "\\``` x"},
		{"a line terminator in the template starts a block of its own", []field{raw("- a\n  "), prose("## x")}, "- a\n  \\## x"},
		{"a bare CR does too", []field{raw("- a\r- "), prose("> x")}, "- a\r- \\> x"},
		{"and the indent goes there as well", []field{raw("- a\n"), prose("    x")}, "- a\nx"},
		{"block structure outranks a span the scan sees across a line ending", []field{raw("- `ping\n"), prose("## x"), raw("` y")}, "- `ping\n\\## x` y"},
		// Two fences a template's line ending separates pair as one span in the
		// bare bytes. Each takes its backslash, the span is gone, and the tag
		// between them is prose: left out of rule 1 it is live raw HTML at GitHub.
		{"a fence's backslash dissolves the span it would pair into", []field{raw("- "), prose("``` <i>"), raw("\n  "), prose("```")}, "- \\``` \\<i>\n  \\```"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var l Line
			for _, f := range c.fields {
				if f.kind == partRaw {
					l.Raw(f.s)
				} else {
					l.Prose(f.s)
				}
			}
			if got := l.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}

	t.Run("an author credit written as prose lands where the template put it", func(t *testing.T) {
		for _, c := range []struct{ login, name, want string }{
			{"", "# Foo", `- \# Foo`},
			{"", "  Foo", "- Foo"},
			{"octocat", "# Foo", "- @octocat"},
		} {
			var l Line
			l.Raw("- @")
			l.Mention(c.login, c.name)
			if got := l.String(); got != c.want {
				t.Errorf("Mention(%q, %q): got %q, want %q", c.login, c.name, got, c.want)
			}
		}
	})
}

// TestRawLineEndingsEndTheParagraphTheFenceReads pins paragraphBreak at Line,
// where a note.line literal reaches it. A blank line spelled in CRs ends the
// paragraph at GitHub, the backticks either side pair with nothing, and an unfenced
// mention behind them is live: measured at 2f9428a through `glyph notes` on the
// first two (`lint` and `notes` both exit 0). The last two must keep their span: one
// line ending is no paragraph break, and GitHub renders the handle as code there.
func TestRawLineEndingsEndTheParagraphTheFenceReads(t *testing.T) {
	for _, c := range []struct{ glue, want string }{
		{"\r\r", "- `ping\r\r:bug:~ cc ``@octocat`` now` x"},
		{"\n\r", "- `ping\n\r:bug:~ cc ``@octocat`` now` x"},
		{"\r\n\r\n", "- `ping\r\n\r\n:bug:~ cc ``@octocat`` now` x"},
		{"\r", "- `ping\r:bug:~ cc @octocat now` x"},
		{"\r\n", "- `ping\r\n:bug:~ cc @octocat now` x"},
	} {
		var l Line
		l.Raw("- `ping" + c.glue)
		l.Prose(":bug:~ cc @octocat now")
		l.Raw("` x")
		if got := l.String(); got != c.want {
			t.Errorf("glue %q:\n got %q\nwant %q", c.glue, got, c.want)
		}
	}
}
