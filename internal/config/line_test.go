package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// render is a stand-in for the notes renderer: it walks the spans the way
// internal/notes does — a placeholder resolves through the table, an optional
// span drops whole when any placeholder in it resolves empty — so this
// package can assert the SHAPE it compiles without importing the renderer
// (which imports this package).
func render(spans []LineSpan, vals map[string]string) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Optional {
			empty := false
			for _, p := range s.Parts {
				if p.Placeholder && vals[p.Text] == "" {
					empty = true
				}
			}
			if empty {
				continue
			}
		}
		for _, p := range s.Parts {
			if p.Placeholder {
				b.WriteString(vals[p.Text])
			} else {
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

// TestParseLineOptionalSpan pins both arms of the shipped preset's template.
// Asserting only the empty arm would pass with the span feature deleted (a
// template that renders nothing optional renders nothing optional); asserting
// only the populated arm would pass with Optional ignored. The pair is the
// assertion.
func TestParseLineOptionalSpan(t *testing.T) {
	spans, err := parseLine("- $subject$[ ($pr)] @$author")
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	withPull := render(spans, map[string]string{"subject": "add the demo feature", "pr": "#61", "author": "akira-toriyama"})
	if withPull != "- add the demo feature (#61) @akira-toriyama" {
		t.Errorf("with a pull the span must render whole, got %q", withPull)
	}
	withoutPull := render(spans, map[string]string{"subject": "add the demo feature", "author": "akira-toriyama"})
	if withoutPull != "- add the demo feature @akira-toriyama" {
		t.Errorf("without a pull the span must take its parens AND its space with it, got %q", withoutPull)
	}
}

// TestParseLineLeavesABareBracketLiteral: only `$[` opens a span. A bare `[`
// is Markdown the author wrote — every link, and the escaped brackets around
// a scope — and compiling it as a span would silently delete the text inside
// it for any commit whose scope is empty.
func TestParseLineLeavesABareBracketLiteral(t *testing.T) {
	spans, err := parseLine(`- \[$scope\] $subject [docs](https://example.invalid)`)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	for _, s := range spans {
		if s.Optional {
			t.Fatalf("a bare [ opened an optional span: %+v", spans)
		}
	}
	got := render(spans, map[string]string{"subject": "fix it"})
	if got != `- \[\] fix it [docs](https://example.invalid)` {
		t.Errorf("literal brackets must survive verbatim, got %q", got)
	}
}

// TestParseLineSpanBracketsNest: a span ends at the first ] that is not
// closing a bracket the span itself opened. Measured with the first ] of any
// kind closing it, this template rendered `- add the demo feature]` for a
// scope-less commit — a stray bracket in every line, from a span that closed
// one character early. Both arms again: the bracketed group renders when the
// scope resolves, and the whole span leaves when it does not.
func TestParseLineSpanBracketsNest(t *testing.T) {
	spans, err := parseLine("- $subject$[ [$scope]]")
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if got, want := render(spans, map[string]string{"subject": "fix the scoped thing", "scope": "cli"}), "- fix the scoped thing [cli]"; got != want {
		t.Errorf("scoped: got %q, want %q", got, want)
	}
	if got, want := render(spans, map[string]string{"subject": "fix the demo crash"}), "- fix the demo crash"; got != want {
		t.Errorf("scope-less: got %q, want %q", got, want)
	}
}

// TestParseLineRefusals: the malformed spans, refused at parse time so they
// fail the config rather than the release that would have rendered them.
func TestParseLineRefusals(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"unterminated", "- $subject$[ ($pr) @$author", "unterminated optional span"},
		{"unpaired bracket inside a span", "- $subject$[ [$pr)] @$author", "unterminated optional span"},
		{"nested", "- $subject$[ ($pr$[ ,$hash]) ] @$author", "nested optional span"},
		{"no placeholder", "- $subject$[ (see the pull)] @$author", "holds no $placeholder"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseLine(c.line)
			if err == nil {
				t.Fatalf("parseLine(%q) succeeded, want an error containing %q", c.line, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want substring %q", err, c.want)
			}
		})
	}
}

// TestLoadRefusesANoteLineWithoutAListMarker: the renderer fences each
// line's would-be mentions against that line alone (markdown.Line), which
// holds only while every rendered line is its own inline context. A template
// opening with no list marker renders a section's lines as one paragraph, and
// there a backtick run on one line pairs with the fence on the next: with
// `line = '$subject$[ ($pr)]'`, three subjects — one ending in a run of three
// backticks, one holding a double backtick and @octocat (fenced in three), one
// ending in three again — rendered through GitHub's /markdown (gfm) with the
// middle line's text in a code span and `<a class="user-mention">@octocat</a>`
// live beside it; with `- ` in front of each, as `<code>@octocat</code>`
// (measured 2026-11-04, t-ngw8; DESIGN §3 quotes the lines). An empty
// template renders no line and loads;
// the three CommonMark bullet markers load; an indented marker does not.
func TestLoadRefusesANoteLineWithoutAListMarker(t *testing.T) {
	const grammar = "schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	for _, line := range []string{"$subject$[ ($pr)]", "$subject", " - $subject", "-$subject", "1. $subject", "## $subject", "@$author $subject"} {
		t.Run(line, func(t *testing.T) {
			_, err := Load([]byte(grammar + "[note]\nline = '" + line + "'\n"))
			if err == nil {
				t.Fatalf("Load accepted note.line %q, which renders a section's lines as one paragraph", line)
			}
			for _, want := range []string{"note.line: ", "does not open with a list-item marker", `open the template with "- "`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want substring %q", err, want)
				}
			}
		})
	}
	for _, line := range []string{"", "- $subject$[ ($pr)]", "* $subject", "+ $subject"} {
		t.Run("loads: "+line, func(t *testing.T) {
			if _, err := Load([]byte(grammar + "[note]\nline = '" + line + "'\n")); err != nil {
				t.Errorf("Load refused note.line %q: %v", line, err)
			}
		})
	}
}

// TestLoadRefusesAnHTMLBlockAtABlockStart: GitHub opens an HTML block where a
// "<" stands at a block start, and no backslash escape works inside one, so a
// template that puts one there renders the value it wraps raw (t-3bwg).
// Measured 2026-11-09 through `glyph notes --range` on the gemoji preset and
// POST /markdown (gfm): the subject `:bug:~ callers to &#64;octocat now`
// rendered a live @octocat behind each of the first seventeen templates, the
// fifteenth to the seventeenth for a commit with no pull, and behind the last
// two through markdown.Line, under the login "1234" (a real account) and under
// an empty author name. `- x <span>$subject</span>` rendered the same subject as
// its own text, and `- 1.<div>` rendered the tag inline; both load, as does a
// "<" the template escaped.
func TestLoadRefusesAnHTMLBlockAtABlockStart(t *testing.T) {
	const grammar = "schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	for _, c := range []struct{ line, at string }{
		{"- <div>$subject</div>", "line 1 column 3"},
		{"* <div>$subject</div>", "line 1 column 3"},
		{"+ <div>$subject</div>", "line 1 column 3"},
		{"- x\n<span>\n$subject", "line 2 column 1"},
		{"- x\r<div>\r$subject", "line 2 column 1"},
		{"- <!-- -->$subject", "line 1 column 3"},
		{"- <?x ?>$subject", "line 1 column 3"},
		{"- <!X>$subject", "line 1 column 3"},
		{"- > <div>$subject</div>", "line 1 column 5"},
		{"- >\t<div>$subject</div>", "line 1 column 5"},
		{"- 1. <div>$subject</div>", "line 1 column 6"},
		{"- 1) <div>$subject</div>", "line 1 column 6"},
		{"- 10. <div>$subject</div>", "line 1 column 7"},
		{"- x\n  <div>\n  $subject", "line 2 column 3"},
		{"- $[($pr) ]<div>$subject</div>", "line 1 column 12"},
		{"- $pr<div>$subject</div>", "line 1 column 6"},
		{"- $[— $pr ]<div>$subject</div>", "line 1 column 12"},
		{"- $author. <div>$subject</div>", "line 1 column 12"},
		{"- @$author <div>$subject</div>", "line 1 column 12"},
	} {
		t.Run(c.line, func(t *testing.T) {
			_, err := Load([]byte(grammar + "[note]\nline = " + strconv.Quote(c.line) + "\n"))
			if err == nil {
				t.Fatalf("Load accepted note.line %q, which opens an HTML block a value renders raw in", c.line)
			}
			for _, want := range []string{"note.line: " + strconv.Quote(c.line), `puts "<" at a block start, ` + c.at + ": ", "GitHub opens an HTML block"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want substring %q", err, want)
				}
			}
		})
	}
	t.Run("the head of the template itself", func(t *testing.T) {
		const line = "<div>$subject</div>"
		spans, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine: %v", err)
		}
		if err := checkLineOpensNoHTMLBlock(line, spans); err == nil || !strings.Contains(err.Error(), "line 1 column 1") {
			t.Errorf("checkLineOpensNoHTMLBlock(%q) = %v, want the refusal at line 1 column 1", line, err)
		}
		if _, err := Load([]byte(grammar + "[note]\nline = '" + line + "'\n")); err == nil {
			t.Errorf("Load accepted note.line %q", line)
		}
	})
	for _, line := range []string{"- x <span>$subject</span>", "- 1.<div>$subject</div>", `- \<div>$subject</div>`, "- $subject$[ ($pr)] @$author$[ with $coauthors]"} {
		t.Run("loads: "+line, func(t *testing.T) {
			if _, err := Load([]byte(grammar + "[note]\nline = '" + line + "'\n")); err != nil {
				t.Errorf("Load refused note.line %q: %v", line, err)
			}
		})
	}
}

// TestLoadRefusesAConstructOpenAtAPlaceholder: a template whose literal text
// leaves an HTML tag, a link's brackets, a link destination, a bare URL or a
// character reference open where a placeholder begins does not load, and the
// refusal names the construct, the placeholder and where the construct stands.
// Each loaded before, and the value completed what the template opened
// (measured 2026-11-15 through `glyph notes --range` and POST /markdown, t-gwsr;
// checkLineLeavesNothingOpen has the table): `- x <$subject` with the subject
// "i>live</i> y" rendered a live <i> open into the next item,
// `- [$subject](https://example.com)` with "click](https://evil.example) y" a
// link to evil.example, `- [x]$subject` with "(https://evil.example) y" the
// same link, `- [x]($subject)` and `- https://$subject` a link to the host the
// subject named, and `- &#64;$subject` with "octocat now" a live @octocat.
func TestLoadRefusesAConstructOpenAtAPlaceholder(t *testing.T) {
	const grammar = "schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	tag := func(name, at string) string {
		return "leaves an HTML tag open where $" + name + ` begins, its "<" at ` + at + ": "
	}
	open := func(name, at string) string {
		return `leaves a "[" open where $` + name + " begins, " + at + ": "
	}
	hidden := func(name, at, what, whatAt string) string {
		return `leaves a "[" open where $` + name + " begins, " + at + ", with a " + what + " inside its text at " + whatAt +
			`: glyph does not read a code span or an HTML tag inside link text, so it cannot tell which "]" closes the text at GitHub`
	}
	closed := func(name, at string) string {
		return "puts $" + name + ` right behind the "]" at ` + at + `, which closes a "[": `
	}
	dest := func(name, at string) string {
		return "leaves a link destination open where $" + name + ` begins, its "(" at ` + at + ": "
	}
	definition := func(name, at string) string {
		return "leaves a link reference definition open where $" + name + ` begins, its ":" at ` + at + ": "
	}
	url := func(name, trigger, at string) string {
		return "leaves a bare URL open where $" + name + " begins, the " + strconv.Quote(trigger) + " that starts its link at " + at + ": "
	}
	urlAdvice := func(name, trigger, at, example, escaped string) string {
		return url(name, trigger, at) + "GitHub links a bare URL up to the next blank, so the value extends the link the template began — " + example +
			` — and a backtick the link takes in leaves a @mention behind it live; put a blank before the placeholder, or write "\` + escaped + `" for its "` + escaped + `" where the URL is text`
	}
	ref := func(name, at string) string {
		return "puts $" + name + " behind the character reference at " + at + " with no blank between them: "
	}
	for _, c := range []struct{ name, line, want string }{
		{"the value names the tag", "- x <$subject", tag("subject", "line 1 column 5")},
		{"the value is an attribute", "- x <i $subject>", tag("subject", "line 1 column 5")},
		{"the value is an attribute's value", `- x <a href="$subject">k</a>`, tag("subject", "line 1 column 5")},
		{"a closing tag", "- x </$subject", tag("subject", "line 1 column 5")},
		{"a > inside double quotes closes no tag", `- x <a title=">" $subject>k</a>`, tag("subject", "line 1 column 5")},
		{"a > inside single quotes closes no tag", "- x <a title='>' $subject>k</a>", tag("subject", "line 1 column 5")},
		{"the open tag behind a closed one", "- x <b>y</b> <i $subject", tag("subject", "line 1 column 14")},
		{"a tag on the template's second line", "- x\n  y <i $subject", tag("subject", "line 2 column 5")},
		{"a tag closed only inside a dropped span", "- x <b$[>$pr] $subject", tag("subject", "line 1 column 5")},
		{"a placeholder inside an optional span", "- x <$[i$pr]i $subject>", tag("pr", "line 1 column 5")},
		{"a comment", "- x <!-- $subject", tag("subject", "line 1 column 5")},
		{"a processing instruction", "- x <?x $subject", tag("subject", "line 1 column 5")},
		{"a comment and a >", "- x <!-- a > $subject", tag("subject", "line 1 column 5")},
		{"a comment the template closed", "- x <!-- c --> $subject", tag("subject", "line 1 column 5")},
		{"a processing instruction the template closed", "- x <? a ?> $subject", tag("subject", "line 1 column 5")},
		{"a CDATA section and a >", "- x <![CDATA[ a ]] > $subject", tag("subject", "line 1 column 5")},
		{"a declaration the template closed", "- x <!DOCTYPE a> $subject", tag("subject", "line 1 column 5")},
		{"a second < inside a quoted value", "- x <a '<i x='> $subject", tag("subject", "line 1 column 5")},
		{"a comment opened inside a tag", "- x <a <!-- > $subject", tag("subject", "line 1 column 5")},

		{"the value is the link text", "- [$subject](https://example.com)", open("subject", "line 1 column 3")},
		{"an image", "- ![$subject](https://example.com/a.png)", open("subject", "line 1 column 4")},
		{"the outer bracket behind a closed inner one", "- [a [b] $subject", open("subject", "line 1 column 3")},
		{"a bracket inside an optional span", "- $subject$[ [$pr]]", open("pr", "line 1 column 14")},
		{"a backslash that is itself escaped", `- \\[$subject]`, open("subject", "line 1 column 5")},
		{"a backslash the value before the bracket uses up", `- x \$hash[$subject]`, open("subject", "line 1 column 11")},
		{"the value right behind the closing bracket", "- [x]$subject", closed("subject", "line 1 column 5")},
		{"behind the closing bracket once a span drops", "- [x]$[ ($pr)]$subject", closed("subject", "line 1 column 5")},
		{"behind a reference link's second pair", "- [x][y]$subject", closed("subject", "line 1 column 8")},
		{"a ] a code span hides, then the value in the destination", "- [a `]` b](/$subject)", hidden("subject", "line 1 column 3", "backtick", "line 1 column 6")},
		{"a ] a tag's attribute hides, then the value in the destination", `- [a <b title="]"> c](/$subject)`, hidden("subject", "line 1 column 3", `"<"`, "line 1 column 6")},
		{"a ] a code span hides, then the value as the destination", "- [a `]` b]($subject)", hidden("subject", "line 1 column 3", "backtick", "line 1 column 6")},
		{"a ] a code span hides in an image", "- ![a `]` b]($subject)", hidden("subject", "line 1 column 4", "backtick", "line 1 column 7")},
		{"a ] an angle autolink hides", "- [a <https://x.example/]> c]($subject)", hidden("subject", "line 1 column 3", `"<"`, "line 1 column 6")},
		{"a ] a code span hides, then the value in the text", "- [a `]` $subject](https://example.com)", hidden("subject", "line 1 column 3", "backtick", "line 1 column 6")},
		{"a ] a tag's attribute hides, then the value in the text", `- [a <b title="]"> $subject](https://example.com)`, hidden("subject", "line 1 column 3", `"<"`, "line 1 column 6")},

		{"the value is the destination", "- [x]($subject)", dest("subject", "line 1 column 6")},
		{"the value ends the destination", "- [x](https://example.com/$subject)", dest("subject", "line 1 column 6")},
		{"a hash in the destination", "- [x](https://example.com/$hash)", dest("hash", "line 1 column 6")},
		{"the value is the title", `- [x](https://example.com "$subject")`, dest("subject", "line 1 column 6")},
		{"an image's destination", "- ![x]($subject)", dest("subject", "line 1 column 7")},
		{"a destination closed only inside a dropped span", "- [x](a$[)$pr] $subject", dest("subject", "line 1 column 6")},
		{"an escaped ) closes no destination", `- [x](a\) $subject`, dest("subject", "line 1 column 6")},
		{"a double quote before the )", `- [x](a"b "c) $subject`, dest("subject", "line 1 column 6")},
		{"a single quote before the )", "- [x](a 'c) $subject", dest("subject", "line 1 column 6")},
		{"a ( before the )", "- [x](a(b) $subject", dest("subject", "line 1 column 6")},
		{"a < before the )", "- [x](<1) $subject", dest("subject", "line 1 column 6")},
		{"a second link inside a title", `- [a](x "[c](d) $subject`, dest("subject", "line 1 column 6")},

		{"the value is a definition's destination", "- [x]: $subject", definition("subject", "line 1 column 6")},
		{"a definition on the template's third line", "- see [x]\n\n  [x]: $subject", definition("subject", "line 3 column 6")},
		{"the value is a definition's title", `- [x]: /url "$subject"`, definition("subject", "line 1 column 6")},
		{"a footnote definition", "- [^1]: $subject", definition("subject", "line 1 column 7")},

		{"the value is the URL's host", "- https://$subject", urlAdvice("subject", "://", "line 1 column 8", `"evil.example/x" behind "https://" links to evil.example`, ":")},
		{"http", "- http://$subject", url("subject", "://", "line 1 column 7")},
		{"a scheme in capitals", "- HTTPS://$subject", url("subject", "://", "line 1 column 8")},
		{"any scheme", "- ftp://$subject", url("subject", "://", "line 1 column 6")},
		{"the value extends the host", "- see https://example.com$subject", url("subject", "://", "line 1 column 12")},
		{"the value extends the path", "- see https://example.com/$subject", url("subject", "://", "line 1 column 12")},
		{"www", "- www.$subject", urlAdvice("subject", "www.", "line 1 column 3", `"evil.example/x" behind "www." links to www.evil.example`, ".")},
		{"www in capitals", "- WWW.$subject", url("subject", "www.", "line 1 column 3")},
		{"the value extends a www URL", "- see www.example.com/$subject", url("subject", "www.", "line 1 column 7")},
		{"a URL in parentheses", "- x (https://a.example)$subject", url("subject", "://", "line 1 column 11")},
		{"a URL behind brackets that are no link", "- [a [b](u) c](https://a.example)$subject", url("subject", "://", "line 1 column 21")},
		{"a :// around a dropped span", "- https:$[$pr]//$subject", url("subject", "://", "line 1 column 8")},
		{"a www. around a dropped span", "- ww$[$pr]w.$subject", url("subject", "www.", "line 1 column 3")},

		{"a decimal reference", "- &#64;$subject", ref("subject", "line 1 column 3")},
		{"a decimal reference with leading zeros", "- &#0064;$subject", ref("subject", "line 1 column 3")},
		{"a hex reference", "- &#x40;$subject", ref("subject", "line 1 column 3")},
		{"a hex reference, capital X", "- &#X4a;$subject", ref("subject", "line 1 column 3")},
		{"a named reference", "- &commat;$subject", ref("subject", "line 1 column 3")},
		{"a named reference with a digit", "- &frac12;$subject", ref("subject", "line 1 column 3")},
		{"the template's text between them", "- &#64;oct$subject", ref("subject", "line 1 column 3")},
		{"the later of two references", "- x &amp;&#64;$subject", ref("subject", "line 1 column 10")},
		{"a blank only inside a dropped span", "- x &#64;$[ ($pr) ]$subject", ref("subject", "line 1 column 5")},
		{"a reference around an empty placeholder", "- &$pr#64;$subject", ref("subject", "line 1 column 3")},
		{"a reference around a dropped span", "- &$[$pr]#64;$subject", ref("subject", "line 1 column 3")},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load([]byte(grammar + "[note]\nline = " + strconv.Quote(c.line) + "\n"))
			if err == nil {
				t.Fatalf("Load accepted note.line %q, which leaves a construct open for a value to complete", c.line)
			}
			if want := "note.line: " + strconv.Quote(c.line) + " " + c.want; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want substring %q", err, want)
			}
		})
	}
}

// TestLoadAcceptsAConstructClosedBeforeAPlaceholder holds
// checkLineLeavesNothingOpen to what it measured: the refusal is for a
// construct open at a placeholder, and a template that closed its construct,
// escaped its opener, or wrote the opener behind the placeholder keeps loading.
// The heads of a reference and of a bare URL load because rules 4 and 2 escape
// one a value completes (markdown's TestAReferenceAValueCompletesIsEscaped,
// TestAReferenceALoginCompletesIsEscaped), and an address because a value
// writes a whole one under every template (markdown.escapeProseLine, "LEFT
// ALIVE").
func TestLoadAcceptsAConstructClosedBeforeAPlaceholder(t *testing.T) {
	const grammar = "schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	for _, c := range []struct{ name, line string }{
		{"a tag the template closed", "- x <b>y</b> $subject"},
		{"a value between a tag and its closer", "- x <b>$subject</b>"},
		{"a > inside quotes, then the tag's own", `- x <span title="a>b" lang='c>d'>$subject</span>`},
		{"a < and a blank", "- x < $subject"},
		{"a < and a digit", "- x <1$subject"},
		{"an escaped <", `- x \<$subject`},

		{"brackets closed, then a blank", "- [x] $subject"},
		{"brackets closed, then a tab", "- [x]\t$subject"},
		{"a link the template wrote whole", "- [x](https://example.com) $subject"},
		{"an image the template wrote whole", "- ![x](https://example.com/a.png) $subject"},
		{"a destination behind the last placeholder", `- $subject [docs](https://example.com "Docs")`},
		{"a ( behind a ] that closes nothing", "- x]($subject)"},
		{"brackets, a blank and a (", "- [x] ($subject)"},
		{"a : behind a ] that closes nothing", "- x]: $subject"},
		{"brackets, a blank and a :", "- [x] : $subject"},
		{"an escaped bracket and a :", `- \[x]: $subject`},

		{"a bare URL and a blank", "- see https://example.com/ $subject"},
		{"a www URL and a blank", "- see www.example.com $subject"},
		{"a bare URL and a line ending", "- see https://example.com/\n  $subject"},
		{"an escaped ://", `- https\://$subject`},
		{"an escaped www.", `- www\.$subject`},
		{"a URL's head: a scheme", "- http$subject"},
		{"a URL's head: a scheme and a :", "- https:$subject"},
		{"a URL's head: a scheme and :/", "- https:/$subject"},
		{"a URL's head: ww", "- ww$subject"},
		{"a URL's head: www", "- www$subject"},
		{"a bare URL behind the last placeholder", "- $subject https://example.com/"},
		{"an address's local part", "- contact local@$subject"},
		{"a mailto:", "- mailto:$subject"},
		{"a mention behind a blank", "- $subject @$author"},
		{"a ] that closes nothing", "- x]$subject"},
		{"an escaped [", `- \[$subject]`},
		{"escaped brackets inside an optional span", `- $subject$[ \[$pr\]]`},
		{"a backtick inside link text behind the last placeholder", "- $subject [a `]` b](https://example.com)"},
		{"a tag inside link text behind the last placeholder", "- $subject [a <b>c</b>](https://example.com)"},
		{"an escaped backtick inside closed link text", "- [a \\` b](https://example.com) $subject"},
		{"an escaped < inside closed link text", `- [a \< b](https://example.com) $subject`},

		{"a reference and a blank", "- &amp; $subject"},
		{"an at-sign reference and a blank", "- &#64; $subject"},
		{"a reference and a tab", "- &#64;\t$subject"},
		{"a bare ampersand", "- R&D $subject"},
		{"an ampersand and a blank", "- x & $subject"},
		{"an escaped reference", `- \&#64;$subject`},
		{"a reference's head: &", "- &$subject"},
		{"a reference's head: &#", "- &#$subject"},
		{"a reference's head: &#x", "- &#x$subject"},
		{"a reference's head: digits", "- &#64$subject"},
		{"a reference's head: a name", "- &comm$subject"},
		{"a name with no semicolon", "- &amp $subject"},

		{"an open tag behind the last placeholder", "- x $subject <i"},
		{"an open bracket behind the last placeholder", "- x $subject [y"},
		{"a link's closer behind the placeholder", "- x $subject](https://example.com)"},
		{"a reference behind the last placeholder", "- x $subject &#64;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load([]byte(grammar + "[note]\nline = " + strconv.Quote(c.line) + "\n")); err != nil {
				t.Errorf("Load refused note.line %q: %v", c.line, err)
			}
		})
	}
	for _, name := range []string{"gemoji", "conventional"} {
		t.Run("the "+name+" preset", func(t *testing.T) {
			preset, ok := Preset(name)
			if !ok {
				t.Fatalf("no %s preset", name)
			}
			cfg, err := Load(preset)
			if err != nil {
				t.Fatalf("Load refused the shipped preset: %v", err)
			}
			if len(cfg.Note.Spans) == 0 {
				t.Errorf("the %s preset compiled no note.line, so its template was not judged", name)
			}
		})
	}
}

// TestLoadFileMalformedLineNamesTheFile: a malformed template is a CONFIG
// failure, and the message has to say which file and which key — the reader
// is a developer whose commit-lint job just went red, holding only stderr.
func TestLoadFileMalformedLineNamesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glyph.toml")
	body := "schema = 1\n" + minimalPatterns + "[note]\nline = '- $subject$[ ($pr) @$author'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatalf("LoadFile accepted an unterminated optional span")
	}
	want := path + ": note.line: "
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want it to start with %q", err, want)
	}
}

// TestLoadRefusesAPlaceholderNothingBinds is the mirror of the no-placeholder
// refusal, and the span is what makes it worth a load error rather than a
// shrug. A name nothing binds resolves empty for every commit, so inside a
// span it drops the span on every line and takes the punctuation with it: the
// release body is quietly missing a column instead of visibly wrong. Measured
// before this check existed — `$[ ($pull)]`, where the built-in is `$pr`,
// rendered every line with no citation and no parens, at exit 0.
//
// Both arms, or neither: a name a pattern DOES capture has to keep loading, or
// the refusal is just a ban on pattern groups.
func TestLoadRefusesAPlaceholderNothingBinds(t *testing.T) {
	// The first pattern captures semver_sigil and subject; scope belongs to the
	// second only, which is what proves the legal set is the UNION.
	const patterns = "[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!]) (?P<subject>.+)'\n" +
		"[[patterns]]\npattern = '^\\((?P<scope>[a-z]+)\\) (?P<semver_sigil>[=~^!]) (?P<subject>.+)'\n"

	t.Run("a name no pattern captures", func(t *testing.T) {
		_, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ ($pull)]'\n"))
		if err == nil {
			t.Fatalf("Load accepted a placeholder no pattern can fill")
		}
		for _, want := range []string{"$pull", "author, coauthors, hash, pr, scope, semver_sigil, subject"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want substring %q", err, want)
			}
		}
	})

	t.Run("a group only the second pattern captures", func(t *testing.T) {
		if _, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ \\[$scope\\]]'\n")); err != nil {
			t.Errorf("Load rejected a group one pattern captures: %v", err)
		}
	})

	t.Run("the built-ins", func(t *testing.T) {
		if _, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ ($pr)] @$author $hash'\n")); err != nil {
			t.Errorf("Load rejected the built-ins: %v", err)
		}
	})
}

// TestNoteLineMayCiteTheFallbackSubject: the raw-line fallback binds $subject
// for every commit no pattern claims, whatever the patterns call their
// groups, so a file that names its subject group otherwise may cite it beside
// that group — the one spelling that renders a bot line's text. Measured
// before (2026-09-29): such a file citing $subject was refused as resolving
// empty for every commit, and its `- $title @$author` rendered each bot line
// as `-  dependabot\[bot]`, the text gone, at exit 0.
func TestNoteLineMayCiteTheFallbackSubject(t *testing.T) {
	src := "schema = 1\n[[patterns]]\npattern = '^(?P<title>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n[note]\nline = '- $title$subject'\n"
	if _, err := Load([]byte(src)); err != nil {
		t.Fatalf("Load refused $subject beside $title, which the fallback binds for every unmatched commit: %v", err)
	}
}

// TestNoteLineRefusesTheFallbackSubjectAlone: where no pattern whose groups a
// commit binds captures `subject`, the fallback binds $subject for a commit no
// pattern claims and for no other, so it completes a template and never
// carries one — a template whose only name beyond the built-ins and trailers
// is that $subject renders every line a pattern claims without its text.
// Measured (2026-10-04) with $subject legal unconditionally: the gemoji
// preset with its group renamed to `title` and note.line left alone loaded
// and printed `-  akira` for each matched commit at exit 0, where adfc5e1
// refused it at exit 2; with sections on the semver axis only, the fallback
// never renders, so $subject filled no line at all. The refusal names the
// groups the file can bind, title among them, and not the fallback's name.
func TestNoteLineRefusesTheFallbackSubjectAlone(t *testing.T) {
	preset, ok := Preset("gemoji")
	if !ok {
		t.Fatal("gemoji preset missing")
	}
	renamed := strings.ReplaceAll(string(preset), "?P<subject>", "?P<title>")
	if renamed == string(preset) {
		t.Fatal("the gemoji preset captures no subject group to rename — re-derive this fixture")
	}
	const title = "schema = 1\n[[patterns]]\npattern = '^(?P<title>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	for name, src := range map[string]string{
		"the preset with its group renamed": renamed,
		"semver sections only":              title + "[note]\nline = '- $subject @$author'\n[[note.sections]]\nsemver = 'patch'\ntitle = 'Fixes'\n",
		"a trailer beside it":               title + "[note]\nline = '- $subject$[ — $why]'\n[[note.trailers]]\ntoken = 'Why'\nname = 'why'\n",
		"only a skip pattern captures it":   title + "[[patterns]]\npattern = '^Merge (?P<subject>.+)'\nskip = true\n[note]\nline = '- $subject'\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load([]byte(src))
			if err == nil {
				t.Fatal("Load accepted a template whose only group is the fallback's $subject: every line a pattern claims renders without its text")
			}
			for _, want := range []string{"note.line: $subject is not a built-in", "the names this file can bind are: ", "semver_sigil, title"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, want substring %q", err, want)
				}
			}
			if strings.Contains(err.Error(), ", subject") {
				t.Errorf("refusal = %q lists subject among the names this file binds", err)
			}
		})
	}
	if _, err := Load([]byte(strings.Replace(renamed, "line = '- $subject", "line = '- $title$subject", 1))); err != nil {
		t.Errorf("the renamed preset citing $title beside $subject did not load: %v", err)
	}
}

// TestNoteLineCannotCiteASkipGroup: skip is total — a skipped commit is in no
// section — so a group only a skip pattern captures binds for no rendered
// line, exactly as one only an unlandable pattern captures. Were it to load,
// `$[ (from $branch)] $branch` from a skip pattern would render
// `- :bug:~ fix it ` at exit 0, span dropped and the bare one empty.
func TestNoteLineCannotCiteASkipGroup(t *testing.T) {
	_, err := Load([]byte("schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n" +
		"[[patterns]]\npattern = '^Merge (?P<branch>.+)'\nskip = true\n[note]\nline = '- $subject$[ (from $branch)]'\n"))
	if err == nil || !strings.Contains(err.Error(), "$branch") {
		t.Fatalf("Load = %v, want the note.line refusal naming $branch", err)
	}
}
