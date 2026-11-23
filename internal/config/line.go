package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// varRE is the $xxx placeholder grammar of note.line. RE2 group names share
// it, which is what makes "a placeholder is a group name" hold — so the
// grammar lives beside the patterns whose groups it reads, not in the
// renderer that consumes it.
var varRE = regexp.MustCompile(`\$[a-z_][a-z0-9_]*`)

// spanOpen marks an optional span. `[` is outside varRE's name alphabet, so
// `$[` never collides with a placeholder; a bare `[` stays literal too, which
// is what leaves the template's own Markdown links alone.
const spanOpen = "$["

// Built-in placeholder names. glyph binds these whatever the patterns capture,
// and they outrank a pattern group of the same name — which is what lets a
// template rely on them resolving in every repository. The renderer keys its
// map off these constants so the set validated at load and the set bound at
// render cannot drift apart.
const (
	BuiltinPR        = "pr"
	BuiltinAuthor    = "author"
	BuiltinHash      = "hash"
	BuiltinCoauthors = "coauthors"
)

// lineBuiltins is the same set as a list, for validation and for messages.
var lineBuiltins = []string{BuiltinPR, BuiltinAuthor, BuiltinHash, BuiltinCoauthors}

// FallbackGroup is the one name the notes' raw-line fallback binds for a
// commit no pattern claims (DESIGN §3, the ratified bot fallback): its first
// line, whatever the file's patterns call their groups. Where no pattern
// captures it, it completes a template and never carries one (see
// validateLineNames). The renderer keys the fallback off this constant for
// the same reason it keys the built-ins: the name validated at load and the
// name bound at render cannot drift apart.
const FallbackGroup = "subject"

// LinePart is one piece of a note.line template. Text is the literal bytes
// when Placeholder is false, and the $name without its '$' when it is true.
type LinePart struct {
	Text        string
	Placeholder bool
}

// LineSpan is a run of parts that renders as a unit. Optional marks a
// `$[ … ]` span: it renders only when EVERY placeholder inside it resolves
// non-empty, so the punctuation written to carry a placeholder — the parens
// around $pr — leaves with the placeholder instead of rendering around
// nothing. Literal text between spans is itself a span with Optional false,
// so a renderer walks one list rather than two shapes.
type LineSpan struct {
	Parts    []LinePart
	Optional bool
}

// listMarkers are the openings a note.line template must have: CommonMark's
// bullet-list markers with the space that makes them one. The renderer fences
// each rendered line's would-be mentions against that line alone
// (markdown.Line), which holds only while every line is its own inline
// context — a list item is; lines no marker separates are one paragraph (or,
// with a `|` in them, a table), where a backtick run from the line above pairs
// with the fence's and a `@name` goes live. Measured against GitHub's
// /markdown (gfm) 2026-11-04 on three rendered lines — a subject ending in a
// three-backtick run, one holding a double backtick and @octocat inside the
// fence's three, and another ending in three — which rendered
// `<a class="user-mention">@octocat</a>` without the marker and
// `<code>@octocat</code>` with it (TestLoadRefusesANoteLineWithoutAListMarker;
// DESIGN §3 quotes the lines). An ordered-list
// marker is not offered: `1. ` on every line renumbers in GitHub, not in the
// file, and the shipped templates open with `- `.
var listMarkers = []string{"- ", "* ", "+ "}

// checkLineOpensAList refuses a non-empty template that opens with anything
// but a listMarkers entry. Whitespace before the marker is refused too: an
// indented marker is a list item only under a block that already opened one.
func checkLineOpensAList(template string) error {
	if template == "" {
		return nil
	}
	for _, m := range listMarkers {
		if strings.HasPrefix(template, m) {
			return nil
		}
	}
	return fmt.Errorf("%q does not open with a list-item marker (\"- \", \"* \" or \"+ \"): each rendered line is fenced against @mentions on its own, and GitHub joins lines no marker separates into one paragraph, where a backtick run from a neighbouring line pairs with the fence and a @name inside it goes live — open the template with \"- \"", template)
}

// checkLineOpensNoHTMLBlock refuses a template whose literal text puts "<" where
// a rendered line can start a block: the head of each of its lines, and behind
// the list-item and quote markers a line opens with — rule 5's block start
// (markdown.blockOpening). GitHub opens an HTML block there, of every kind (a
// tag, "<!--", "<?", "<!X", a lone tag on a line of its own), and no backslash
// escape works inside one, so a value the block takes in renders raw: behind
// `- <div>$subject</div>` the prose escape's `\&#64;octocat` rendered a printed
// backslash and a live @octocat, and its `\<i>` a printed backslash and a live
// tag (measured 2026-11-09 through `glyph notes --range` and POST /markdown;
// TestLoadRefusesAnHTMLBlockAtABlockStart lists the shapes).
//
// The test is a byte, not the seven start conditions of an HTML block: every
// "<" at a block start is refused, `- <span>$subject</span>`, which GitHub
// renders inline, included. That is the over-refusing side, and it costs the
// shipped templates nothing. What renders before the "<" is read bluntly too,
// because a commit decides it and the loader sees none: a placeholder as any
// value at all, an optional span as rendered or dropped, and an at-sign as
// absent. A rendered line reaches each: an empty value or a dropped span leaves
// the "<" where it stood (`- $[($pr) ]<div>` with no pull renders `- <div>`); a
// live login is no prose, so rule 5 leaves the "1234" (a real account) that
// `- $author. <div>` writes standing as an ordered-list marker; and Line.Mention
// drops the at-sign before a credit that cannot page, so `- @$author <div>`
// under an empty author name writes "-  <div>".
func checkLineOpensNoHTMLBlock(template string, spans []LineSpan) error {
	// at walks template in step with the spans, which hold every byte parseLine
	// read: a span's "$[" and "]", a placeholder's "$" and its name, the literals.
	states, at := blockStart, 0
	for _, span := range spans {
		dropped := states
		if span.Optional {
			at += len(spanOpen)
		}
		for _, p := range span.Parts {
			if p.Placeholder {
				if states != 0 {
					states = anyBlockState
				}
				at += len("$") + len(p.Text)
				continue
			}
			for i := 0; i < len(p.Text); i++ {
				if p.Text[i] == '<' && states&blockStart != 0 {
					line, col := linePosition(template, at)
					return fmt.Errorf("%q puts \"<\" at a block start, line %d column %d: GitHub opens an HTML block where a line, a list item or a block quote starts, and no backslash escape works inside one, so a value there renders raw — its \"<i>\" a live tag, its \"&#64;\" a live @mention — write text of the template's own before the \"<\" on its line (blanks, list-item or quote markers, at-signs, placeholders and text inside an optional $[ … ] span do not count: each can leave the block start where it was)", template, line, col)
				}
				states = stepBlockState(states, p.Text[i])
				at++
			}
		}
		if span.Optional {
			states |= dropped
			at += len("]")
		}
	}
	return nil
}

// The states of a rendered line read up to some byte, in rule 5's walk past the
// containers the line opens with: a set of them is what the line can be in, which
// is how a value no commit has supplied yet is carried.
const (
	blockStart    = 1 << iota // nothing yet but blanks and whole markers: a block starts here
	bulletMark                // behind "-", "+" or "*": a blank makes it a list item
	ordinalDigits             // inside an ordered-list marker's digits
	ordinalMark               // behind their "." or ")": a blank makes it a list item
	anyBlockState = blockStart | bulletMark | ordinalDigits | ordinalMark
)

// stepBlockState is the states a line can be in after c, from any of states. A
// line ending starts the next line at a block start, whatever came before it, and
// an at-sign is read as absent (checkLineOpensNoHTMLBlock says why).
func stepBlockState(states int, c byte) int {
	switch c {
	case '\n', '\r':
		return blockStart
	case '@':
		return states
	}
	blank, digit := c == ' ' || c == '\t', c >= '0' && c <= '9'
	next := 0
	if states&blockStart != 0 {
		switch {
		case blank, c == '>':
			next |= blockStart
		case c == '-', c == '+', c == '*':
			next |= bulletMark
		case digit:
			next |= ordinalDigits
		}
	}
	if states&(bulletMark|ordinalMark) != 0 && blank {
		next |= blockStart
	}
	if states&ordinalDigits != 0 {
		switch {
		case digit:
			next |= ordinalDigits
		case c == '.', c == ')':
			next |= ordinalMark
		}
	}
	return next
}

// checkLineLeavesNothingOpen refuses a template whose literal text leaves an
// inline construct open where a placeholder begins, for the value to complete.
// The prose escape puts its backslash on the byte that opens a construct, and
// only on a value's byte (markdown.escapeProseLine), so an opener the template
// wrote stays live and the value lands inside it, where no escape of the value
// closes the hole. Five constructs, each measured 2026-11-15 through `glyph
// notes --range` and POST /markdown (gfm):
//
//	template                                 value                            GitHub renders
//	`- x <$subject`                          i>live</i> y                     a live <i>, open into the next item
//	`- x <i $subject>`                       title=a                          <i title="a">
//	`- x <a title=">" $subject>k</a>`        href=//evil.example              <a href="//evil.example">
//	`- x <!-- a > $subject`                  ` --> @octocat `                 a live @octocat: the comment ends at the value's "-->" and takes its backtick in
//	`- x <a '<i x='> $subject`               ` '> @octocat `                  a live @octocat: GitHub reads the second "<" as the tag
//	`- [$subject](https://example.com)`      click](https://evil.example) y   a link to evil.example ("https\://" loses its backslash inside a destination)
//	`- [x]$subject`                          (https://evil.example) y         a link to evil.example
//	`- [x]$subject`                          : /url                           a link reference definition: "[x]" links to /url
//	`- [x]($subject)`                        https://evil.example             a link to evil.example
//	`- [x](https://example.com/$subject)`    x`) @octocat `                   a live @octocat: the destination takes the backtick in
//	`- [x](https://example.com "$subject")`  ` ") @octocat `                  a live @octocat, the backtick in the title
//	`- [x](a"b "c) $subject`                 ` ") @octocat `                  a live @octocat: the quote behind the blank opens a title, the first one does not
//	`- [x](a(b) $subject`                    "` ") @octocat `                 a live @octocat: the first ")" balances the "(" and ends nothing
//	`- [x]: $subject`                        https://evil.example             a definition: the item renders empty
//	`- https://$subject`                     evil.example/x                   a link to evil.example (so "http://", "HTTPS://", "www.")
//	`- see https://example.com$subject`      .evil.example/x                  a link to example.com.evil.example
//	`- see https://example.com/$subject`     ` @octocat `                     a live @octocat: the URL takes the backtick in
//	`- &#64;$subject`                        octocat now                      a live @octocat (so "&commat;", "&#x40;", "&#0064;")
//	`- &#64;oct$subject`                     ocat now                         a live @octocat
//	`- &$pr#64;$subject`                     octocat now, no pull             a live @octocat
//
// What loads, measured the same way with the same values: a tag the template
// closed (`- x <b>$subject</b>`, `- x <b>y</b> $subject`: the value is text), a
// "<" no tag can start at (`- x < $subject`, `- x <1$subject`), brackets closed
// with something of the template's own behind them (`- [x] $subject`), a link
// or a bare URL the template wrote whole with a blank behind it
// (`- [x](https://example.com) $subject`, `- see https://example.com/ $subject`),
// a reference with a blank behind it (`- &#64; $subject` renders "@ octocat"),
// an escaped opener (`- x \<$subject`, `- \[$subject]`, `- https\://$subject`,
// `- www\.$subject`, `- \&#64;$subject`), and the head of a reference or of a
// bare URL (`- &$subject`, `- &#64$subject`, `- https:$subject`,
// `- www$subject`), which rules 4 and 2 escape on the assembled line once a
// value completes it.
//
// The walk never ends a construct before GitHub does. Each test is blunter
// than the grammar it stands for, on the refusing side, and where two readings
// of the template's bytes can end a construct at different bytes the walk
// stops ending it at all:
//
//   - A tag is open from a "<" that a letter or "/" follows (the "<" rule 5
//     reads as an opener), or that the placeholder itself follows, to the next
//     ">" outside a quoted attribute value. Whether the bytes between are a
//     valid tag is not asked. A second "<" inside it leaves two candidates for
//     the tag, each with its own quotes, so nothing closes it from there; and
//     nothing closes "<!" or "<?" (a comment, a declaration, CDATA, a
//     processing instruction), which GitHub ends at "-->", ">", "]]>" or "?>"
//     by which of the four it is.
//   - Link text is open while a "[" has no "]" of its own, and the "]" that
//     closes one refuses a placeholder right behind it. Images, footnotes and
//     reference links are the same brackets. A backtick or a "<" no backslash
//     escapes inside open link text leaves the text open: no later "]" closes
//     it. What hides a "]" from GitHub there — a code span, a tag, a comment,
//     an angle autolink (`- [a `]` b]($subject)` links where the subject
//     points) — starts with one of those two bytes, so the walk reads none of
//     them and still ends no link text early.
//   - A link destination is open from a "(" right behind such a "]" to the
//     next ")" no backslash escapes. A "(", a "<" or a quote before that ")"
//     is where GitHub's grammar forks (balanced parentheses, a bracketed
//     address, a title, each legal only in its place), so nothing closes the
//     destination from there. A ":" right behind such a "]" opens a link
//     reference definition, which nothing closes: where a definition ends is
//     a block's question.
//   - A bare URL is open from "://" or "www." to the next blank: no scheme is
//     asked for and none excluded, "www." is read in either case and wherever
//     it stands, and nothing but a blank ends the URL — GitHub's own end, a
//     "<" or trimmed punctuation, moves with what follows it.
//   - A character reference is any that has the shape (markdown.entityAt's:
//     decimal, hex or named, closed by ";"), whatever it decodes to, and it
//     reaches the placeholder across every byte but a blank: a table of the
//     references that decode to an at-sign would owe this boundary to
//     GitHub's.
//
// An address is no bare URL here (`- contact local@$subject`,
// `- mailto:$subject`): a value writes a whole address under every template
// and GitHub links it (markdown.escapeProseLine, "LEFT ALIVE"), `- contact
// local$subject` renders the same link from "@evil.example" and text before a
// placeholder cannot be refused, and an address takes no backtick in.
//
// A backslash makes the opener behind it text, as it does at GitHub; inside an
// open tag it does not, as in HTML. Nothing else of Markdown is read: a
// template's own code span or line ending closes nothing here, and inside link
// text a code span's backtick keeps the text open (above).
//
// A commit decides what stands before a placeholder, and the loader sees none
// (checkLineOpensNoHTMLBlock): an optional span is read as rendered and as
// dropped, and an earlier placeholder as a value and as empty, which is how
// `- &$pr#64;$subject` writes "&#64;" for a commit with no pull. So the walk
// carries the set of states the rendered line can be in.
func checkLineLeavesNothingOpen(template string, spans []LineSpan) error {
	states, at := []inlineState{{}}, 0
	for _, span := range spans {
		dropped := states
		if span.Optional {
			at += len(spanOpen)
		}
		for _, p := range span.Parts {
			if p.Placeholder {
				next := make([]inlineState, 0, 2*len(states))
				for _, st := range states {
					if err := st.refusal(template, p.Text); err != nil {
						return err
					}
					next = addInlineState(addInlineState(next, st), st.behindAValue())
				}
				states = next
				at += len("$") + len(p.Text)
				continue
			}
			for i := 0; i < len(p.Text); i++ {
				next := make([]inlineState, 0, len(states))
				for _, st := range states {
					next = addInlineState(next, st.step(p.Text[i], at))
				}
				states = next
				at++
			}
		}
		if span.Optional {
			merged := slices.Clone(states)
			for _, st := range dropped {
				merged = addInlineState(merged, st)
			}
			states = merged
			at += len("]")
		}
	}
	return nil
}

// inlineState is what a rendered line's literal text has left open at some
// byte, as checkLineLeavesNothingOpen reads it. The zero value is nothing
// open; every offset is into the template and is zero while its construct is
// not open, so that equal states compare equal.
type inlineState struct {
	escaped bool // the last byte is a backslash no backslash escaped

	tag   tagState
	tagAt int // the "<" of a tag that is not tagNone

	depth  int // "[" with no "]" of their own yet
	linkAt int // the outermost of them

	hidden   bool // a backtick or a "<" no backslash escaped stood inside open link text: no "]" closes it
	hiddenAt int  // that byte

	closed   bool // the last byte is a "]" that closed a "["
	closedAt int

	dest   destState
	destAt int // the "(" or the ":" behind the "]" of a destination that is not destNone

	slashes uint8  // 1 behind a ":" no backslash escaped, 2 behind ":/": one more "/" writes "://"
	colonAt int    // that ":"
	ws      uint8  // how many "w" the last bytes are, up to three: a "." then writes "www."
	wsAt    [3]int // the last ws of those "w", oldest first
	auto    bool   // inside a bare URL the template began, and no blank since
	autoAt  int    // the ":" of its "://" or the first "w" of its "www."

	ref   refState
	refAt int // the "&" of a reference that is not refNone

	pending   bool // a whole character reference, and no blank since
	pendingAt int  // its "&"
}

type tagState uint8

const (
	tagNone   tagState = iota
	tagLess            // behind "<": the next byte decides
	tagOpen            // inside a tag, outside its quotes
	tagDouble          // inside a double-quoted attribute value
	tagSingle          // inside a single-quoted one
	tagStuck           // "<!", "<?" or a tag that holds a second "<": nothing closes it
)

type destState uint8

const (
	destNone       destState = iota
	destInline               // behind "](", and nothing but plain bytes since
	destStuck                // behind "](" and a "(", a "<" or a quote: nothing closes it
	destDefinition           // behind "]:": nothing closes it
)

type refState uint8

const (
	refNone    refState = iota
	refAmp              // &
	refHash             // &#
	refHexMark          // &#x
	refDigits           // &#1
	refHex              // &#x1
	refName             // &a
)

func addInlineState(set []inlineState, st inlineState) []inlineState {
	if slices.Contains(set, st) {
		return set
	}
	return append(set, st)
}

// step is the state after the literal byte c, which stands at offset at.
func (s inlineState) step(c byte, at int) inlineState {
	live := !s.escaped
	s.escaped = c == '\\' && live

	if c == '<' && (s.tag == tagOpen || s.tag == tagDouble || s.tag == tagSingle) {
		s.tag = tagStuck
	}
	switch s.tag {
	case tagStuck:
	case tagDouble:
		if c == '"' {
			s.tag = tagOpen
		}
	case tagSingle:
		if c == '\'' {
			s.tag = tagOpen
		}
	case tagOpen:
		switch c {
		case '"':
			s.tag = tagDouble
		case '\'':
			s.tag = tagSingle
		case '>':
			s.tag, s.tagAt = tagNone, 0
		}
	default:
		switch {
		case s.tag == tagLess && (c == '!' || c == '?'):
			s.tag = tagStuck
		case s.tag == tagLess && (isASCIILetter(c) || c == '/'):
			s.tag = tagOpen
		case c == '<' && live:
			s.tag, s.tagAt = tagLess, at
		default:
			s.tag, s.tagAt = tagNone, 0
		}
	}

	switch {
	case s.closed && c == ':' && s.dest == destNone:
		s.dest, s.destAt = destDefinition, at
	case s.closed && c == '(' && s.dest == destNone:
		s.dest, s.destAt = destInline, at
	case s.dest == destInline && c == ')' && live:
		s.dest, s.destAt = destNone, 0
	case s.dest == destInline && (c == '(' || c == '<' || c == '"' || c == '\''):
		s.dest = destStuck
	}

	s.closed, s.closedAt = false, 0
	switch {
	case c == '[' && live:
		if s.depth == 0 {
			s.linkAt = at
		}
		s.depth++
	case c == ']' && live && s.depth > 0 && !s.hidden:
		s.depth--
		if s.depth == 0 {
			s.linkAt = 0
		}
		s.closed, s.closedAt = true, at
	case (c == '`' || c == '<') && live && s.depth > 0 && !s.hidden:
		s.hidden, s.hiddenAt = true, at
	}

	if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
		s.pending, s.pendingAt = false, 0
		s.auto, s.autoAt = false, 0
	}
	slashes, colonAt, ws, wsAt := s.slashes, s.colonAt, s.ws, s.wsAt
	s.slashes, s.colonAt, s.ws, s.wsAt = 0, 0, 0, [3]int{}
	switch {
	case c == ':' && live:
		s.slashes, s.colonAt = 1, at
	case c == '/' && slashes == 1:
		s.slashes, s.colonAt = 2, colonAt
	case c == '/' && slashes == 2 && !s.auto:
		s.auto, s.autoAt = true, colonAt
	case c == 'w' || c == 'W':
		if ws < 3 {
			wsAt[ws] = at
		} else {
			wsAt = [3]int{wsAt[1], wsAt[2], at}
		}
		s.ws, s.wsAt = min(ws+1, 3), wsAt
	case c == '.' && ws == 3 && !s.auto:
		s.auto, s.autoAt = true, wsAt[0]
	}
	digit := c >= '0' && c <= '9'
	hex := digit || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	ref, refAt := s.ref, s.refAt
	s.ref, s.refAt = refNone, 0
	switch {
	case c == '&' && live:
		s.ref, s.refAt = refAmp, at
	case ref == refAmp && c == '#':
		s.ref, s.refAt = refHash, refAt
	case ref == refAmp && isASCIILetter(c):
		s.ref, s.refAt = refName, refAt
	case ref == refHash && (c == 'x' || c == 'X'):
		s.ref, s.refAt = refHexMark, refAt
	case (ref == refHash || ref == refDigits) && digit:
		s.ref, s.refAt = refDigits, refAt
	case (ref == refHexMark || ref == refHex) && hex:
		s.ref, s.refAt = refHex, refAt
	case ref == refName && (isASCIILetter(c) || digit):
		s.ref, s.refAt = refName, refAt
	case (ref == refDigits || ref == refHex || ref == refName) && c == ';':
		s.pending, s.pendingAt = true, refAt
	}
	return s
}

// behindAValue is the state after a placeholder that rendered a value, for a
// state refusal let pass: the value's bytes stand between the template's, so a
// backslash before the placeholder escapes none of the template's bytes behind
// it (`- x \$hash[$subject]` leaves its "[" live behind the hash). A
// placeholder that rendered empty leaves the state as it was, a reference's
// head included, and the walk keeps both.
func (s inlineState) behindAValue() inlineState {
	s.escaped = false
	return s
}

// refusal is the load error for a placeholder $name that begins in this state,
// nil when nothing is open.
func (s inlineState) refusal(template, name string) error {
	switch {
	case s.tag == tagStuck:
		line, col := linePosition(template, s.tagAt)
		return fmt.Errorf("%q leaves an HTML tag open where $%s begins, its \"<\" at line %d column %d: GitHub ends a comment, a processing instruction, a declaration or a tag that holds a second \"<\" by a grammar of its own (at \"-->\", \"?>\", \"]]>\", or the \">\" of whichever \"<\" it read as the tag), and the loader takes no \">\" for that end, so the value may stand inside the construct, where no backslash escape works — write it behind the last placeholder, or write \"\\<\" where the \"<\" is text", template, name, line, col)
	case s.tag != tagNone:
		line, col := linePosition(template, s.tagAt)
		return fmt.Errorf("%q leaves an HTML tag open where $%s begins, its \"<\" at line %d column %d: no backslash escape works inside a tag, so the value completes the tag instead of standing in it as text — \"i>\" there renders a live <i>, \"img src=x>\" a live image — close the tag with \">\" before the placeholder, or write \"\\<\" where the \"<\" is text", template, name, line, col)
	case s.hidden:
		line, col := linePosition(template, s.linkAt)
		hiddenLine, hiddenCol := linePosition(template, s.hiddenAt)
		what := "backtick"
		if template[s.hiddenAt] == '<' {
			what = `"<"`
		}
		return fmt.Errorf("%q leaves a \"[\" open where $%s begins, line %d column %d, with a %s inside its text at line %d column %d: glyph does not read a code span or an HTML tag inside link text, so it cannot tell which \"]\" closes the text at GitHub, and the value may stand inside the link — \"https://evil.example\" behind \"[a `]` b](\" links to evil.example — write \"\\%c\" where the %s is text, or move it out of the link text", template, name, line, col, what, hiddenLine, hiddenCol, template[s.hiddenAt], what)
	case s.depth > 0:
		line, col := linePosition(template, s.linkAt)
		return fmt.Errorf("%q leaves a \"[\" open where $%s begins, line %d column %d: the value's \"]\" closes the link text and its \"(\" opens a destination no escape reaches — \"click](https://evil.example)\" there renders a link to evil.example — write \"\\[\" where the bracket is text", template, name, line, col)
	case s.closed:
		line, col := linePosition(template, s.closedAt)
		return fmt.Errorf("%q puts $%s right behind the \"]\" at line %d column %d, which closes a \"[\": a value that opens with \"(\" or \":\" gives the brackets a destination — \"(https://evil.example)\" there renders a link to evil.example — put a blank or text of the template's own between them, or write \"\\[\" where the bracket is text", template, name, line, col)
	case s.dest == destDefinition:
		line, col := linePosition(template, s.destAt)
		return fmt.Errorf("%q leaves a link reference definition open where $%s begins, its \":\" at line %d column %d: \"[x]:\" defines where every \"[x]\" in the body links to, and the value is that address or its title — \"https://evil.example\" there links them to evil.example — write \"\\[\" where the bracket is text", template, name, line, col)
	case s.dest == destStuck:
		line, col := linePosition(template, s.destAt)
		return fmt.Errorf("%q leaves a link destination open where $%s begins, its \"(\" at line %d column %d: GitHub reads a \"(\", a \"<\" or a quote inside a destination by a grammar of its own (nested parentheses, a bracketed address, a title), and the loader takes no \")\" behind one for the destination's end, so the value may stand inside the link — write the destination without them, or write the link behind the last placeholder", template, name, line, col)
	case s.dest == destInline:
		line, col := linePosition(template, s.destAt)
		return fmt.Errorf("%q leaves a link destination open where $%s begins, its \"(\" at line %d column %d: the value is the address or the title of the link, and no backslash escape works there — \"https://evil.example\" behind \"[x](\" links to evil.example, and a backtick the destination takes in leaves a @mention behind it live — close the destination with \")\" before the placeholder", template, name, line, col)
	case s.auto:
		line, col := linePosition(template, s.autoAt)
		trigger, example, escaped := "://", `"evil.example/x" behind "https://" links to evil.example`, ":"
		if template[s.autoAt] != ':' {
			trigger, example, escaped = "www.", `"evil.example/x" behind "www." links to www.evil.example`, "."
		}
		return fmt.Errorf("%q leaves a bare URL open where $%s begins, the %q that starts its link at line %d column %d: GitHub links a bare URL up to the next blank, so the value extends the link the template began — %s — and a backtick the link takes in leaves a @mention behind it live; put a blank before the placeholder, or write \"\\%s\" for its %q where the URL is text", template, name, trigger, line, col, example, escaped, escaped)
	case s.pending:
		line, col := linePosition(template, s.pendingAt)
		return fmt.Errorf("%q puts $%s behind the character reference at line %d column %d with no blank between them: the mention fence reads the line's bytes and GitHub resolves a reference after it has read them, so \"&#64;\" and the value \"octocat\" render a live @octocat — put a blank between them, or write the character itself", template, name, line, col)
	}
	return nil
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// linePosition is the 1-based line and column of byte at in s. A line ends
// where CommonMark's does — "\n", "\r\n" or a lone "\r" — and a column counts
// runes.
func linePosition(s string, at int) (line, col int) {
	line, start := 1, 0
	for i := range at {
		if s[i] == '\n' || (s[i] == '\r' && (i+1 == len(s) || s[i+1] != '\n')) {
			line, start = line+1, i+1
		}
	}
	return line, utf8.RuneCountInString(s[start:at]) + 1
}

// parseLine compiles a note.line template into spans, rejecting rather than
// repairing three malformed shapes: an unterminated `$[`, a nested one, and a
// span holding no placeholder. Callers parse at LOAD time so a bad template
// fails the config — the same exit every other config error takes — rather
// than the release that would have rendered it. Whether the template opens
// with a list marker is checkLineOpensAList's, asked beside it; whether it
// puts "<" at a block start is checkLineOpensNoHTMLBlock's and whether it leaves
// a construct open at a placeholder checkLineLeavesNothingOpen's, asked of the
// spans.
//
// An empty template is not an error: [note] is optional, and a config with no
// line renders no lines.
func parseLine(template string) ([]LineSpan, error) {
	var spans []LineSpan
	var plain []LinePart
	flush := func() {
		if len(plain) > 0 {
			spans = append(spans, LineSpan{Parts: plain})
			plain = nil
		}
	}

	rest := template
	for {
		open := strings.Index(rest, spanOpen)
		if open < 0 {
			plain = append(plain, parseParts(rest)...)
			break
		}
		plain = append(plain, parseParts(rest[:open])...)
		rest = rest[open+len(spanOpen):]

		end, err := spanEnd(rest, template)
		if err != nil {
			return nil, err
		}
		parts := parseParts(rest[:end])
		if !holdsPlaceholder(parts) {
			return nil, fmt.Errorf("optional span %q holds no $placeholder: a span with nothing to resolve renders unconditionally — it says optional and means always", spanOpen+rest[:end]+"]")
		}
		flush()
		spans = append(spans, LineSpan{Parts: parts, Optional: true})
		rest = rest[end+1:]
	}
	flush()
	return spans, nil
}

// spanEnd finds the ] closing a span whose text starts at the head of s.
// Brackets inside a span NEST — a Markdown link lives inside one, and so do
// the escaped brackets of `\[$scope\]` — so the closing bracket is the first at depth zero, not
// the first of any kind. Taking the first of any kind was measured writing
// `- add the demo feature]` from `- $subject$[ [$scope]]`: the stray bracket
// of a span that closed one character early, which is the same class of
// silently-wrong line the span exists to remove. An unpaired [ therefore runs
// off the end and is refused as unterminated rather than closing early.
func spanEnd(s, template string) (int, error) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], spanOpen):
			return 0, fmt.Errorf("nested optional span in %q: a span renders whole or not at all, so an inner one has nothing left to decide", template)
		case s[i] == '[':
			depth++
		case s[i] == ']':
			if depth == 0 {
				return i, nil
			}
			depth--
		}
	}
	return 0, fmt.Errorf("unterminated optional span in %q: every $[ needs a closing ], and a [ inside the span takes one of its own", template)
}

// parseParts splits span-free template text into its literal runs and its
// $placeholders.
func parseParts(s string) []LinePart {
	var parts []LinePart
	for s != "" {
		loc := varRE.FindStringIndex(s)
		if loc == nil {
			parts = append(parts, LinePart{Text: s})
			break
		}
		if loc[0] > 0 {
			parts = append(parts, LinePart{Text: s[:loc[0]]})
		}
		parts = append(parts, LinePart{Text: s[loc[0]+1 : loc[1]], Placeholder: true})
		s = s[loc[1]:]
	}
	return parts
}

func holdsPlaceholder(parts []LinePart) bool {
	for _, p := range parts {
		if p.Placeholder {
			return true
		}
	}
	return false
}

// validateLineNames refuses a $placeholder that no rendered line can fill:
// not a built-in, not a declared trailer, and captured by no pattern whose
// groups a commit binds — and $subject that only the fallback binds, unless
// the template cites a pattern group beside it.
//
// parseLine already refuses a span with no placeholder — "it says optional and
// means always". This is the mirror, and the span made it worse: a name nothing
// binds resolves empty for every commit, so inside a span it drops the span on
// every line and takes the punctuation with it, leaving a release body quietly
// missing a column rather than visibly wrong (DESIGN §3, "A placeholder no
// commit binds does not load"). Optional punctuation is only safe to offer if
// a typo inside it cannot pass for a deliberate omission.
//
// The legal set is the UNION over patterns, not the intersection: which pattern
// wins is a property of each commit, so a name any pattern captures is a name
// the template may cite. Only the patterns whose groups a commit binds count
// (Pattern.bindsGroups): a skip pattern's commit is in no section, and an
// unlandable one's claim comes back unmatched with no groups, so a name only
// such a pattern captures resolves empty for every rendered line.
//
// The raw-line fallback binds FallbackGroup for a commit no pattern claims,
// whatever the patterns name their groups — and for no other commit. So
// where no pattern binds `subject`, $subject COMPLETES a template, it never
// carries one: legal when the template also cites a group a pattern binds
// (`- $title$subject` — each line fills the one its commit binds), refused
// when it is the template's only name beyond the built-ins and trailers,
// because every line a pattern claims would render without its text
// (DESIGN §3, "The fallback's `$subject` completes a template", has the
// measurements).
func validateLineNames(spans []LineSpan, patterns []Pattern, trailers []NoteTrailer) error {
	groups := boundGroupNames(patterns)
	legal := make(map[string]bool, len(lineBuiltins)+len(groups)+len(trailers))
	for _, b := range lineBuiltins {
		legal[b] = true
	}
	for g := range groups {
		legal[g] = true
	}
	for _, t := range trailers {
		legal[t.Name] = true
	}

	known := slices.Sorted(maps.Keys(legal))
	citesFallback, citesGroup := false, false
	for _, span := range spans {
		for _, part := range span.Parts {
			switch {
			case !part.Placeholder:
			case legal[part.Text]:
				// A built-in outranks a group of its name, so citing one
				// binds no group.
				if groups[part.Text] && !slices.Contains(lineBuiltins, part.Text) {
					citesGroup = true
				}
			case part.Text == FallbackGroup:
				citesFallback = true
			default:
				return fmt.Errorf("$%s is not a built-in and no commit binds it — no pattern whose groups a commit binds (not skip, not unlandable) captures it and no [[note.trailers]] entry declares it — so it resolves empty on every line; the names this file can bind are: %s", part.Text, strings.Join(known, ", "))
			}
		}
	}
	if citesFallback && !citesGroup {
		return fmt.Errorf("$%s is not a built-in and no pattern whose groups a commit binds captures it, so it resolves empty on every line a pattern claims — only the raw-line fallback binds it, for a commit no pattern claims — and the template cites no pattern group beside it to carry those lines' text; the names this file can bind are: %s", FallbackGroup, strings.Join(known, ", "))
	}
	return nil
}

// boundGroupNames is the set of group names some commit can bind: those
// captured by a pattern whose groups a commit binds (Pattern.bindsGroups).
// validateLineNames reads it for the names a template may cite and
// buildTrailers for the names a trailer may not take, so the two read one
// set.
func boundGroupNames(patterns []Pattern) map[string]bool {
	names := make(map[string]bool)
	for i := range patterns {
		p := &patterns[i]
		if !p.bindsGroups() {
			continue
		}
		for _, name := range p.re.SubexpNames() {
			if name != "" {
				names[name] = true
			}
		}
	}
	return names
}
