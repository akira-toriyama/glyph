package markdown

import (
	"regexp"
	"strings"
)

// Line assembles one GitHub-rendered Markdown line from glyph's own markup and
// author-supplied fields, applying this package's escaping pipeline in the only
// order that is safe: flatten each field as it arrives, then — over the
// assembled whole, in String — the prose escape and the mention fence, one pass
// each. BOTH whole-line passes are load-bearing and for the same reason; the
// escape pass was per field until t-9np1 measured a live link out of the seam
// between two of them. It is the package's ONLY exported surface, and that is the
// point (ratified 2026-07-22, t-3f4s): the order used to be a contract written
// in doc comments on two call sites, and a third caller handing raw author text
// to the mention fence would have resurrected the phantom-span hole SILENTLY —
// codeSpans is a backtick-only model, exact only after escapeProseLine has removed
// the competing constructs (escape.go's header holds the theorem, and the
// measured live mentions that killed the alternatives). A doc comment cannot
// stop that caller. An unexported function can, and now does — the surface
// golden in testdata/exported-surface.golden.txt is what keeps it that way.
//
// The zero value is ready to use. Append in render order:
//
//	var l markdown.Line
//	l.Raw("- ")        // glyph's own markup, trusted byte-for-byte
//	l.Text(scope)      // a plain-text FIELD: flattened, every construct disarmed
//	l.Prose(subject)   // author PROSE: disarmed in String, still readable
//	body := l.String() // prose escape, then mention fence — over the whole line
//
// Why the fence comes last and sees everything: mention-safety is a property of
// the rendered INLINE CONTEXT and of no field in it. The fence must be longer
// than every backtick run it will share a context with, and the fields share
// one — sizing the subject's fence against the subject alone let a backtick
// carried by the SCOPE steal it, and the assembled line was a live mention
// (measured against GitHub 2026-07-21; the incident is written out at
// escapeText's doc in escape.go). The order is forced from the other side too: the fence
// models the FINAL string, so no neutralization may follow it — a later pass
// would rewrite the string the fence was sized and placed against, and an added
// backslash in front of the fence swallows it outright.
//
// Flattening sits inside Text and Prose, and first, for the same reason facing
// the other way: it is what DECIDES the inline context. To the escaper a blank
// line ends the paragraph and backticks on either side of it cannot pair — but
// the flattened line is ONE context, where they can. When the order was each
// caller's to get right, preview escaped first and flattened after, which sized
// the fence against a context the caller then destroyed, and this cell was a
// live mention from a subject the escaper had already declared safe (measured
// against GitHub 2026-07-21):
//
//	| a ` b  c `@octocat d` | 🐛 `:bug:` | patch |
type Line struct {
	// parts are the fragments in render order. They are held rather than
	// concatenated because the prose escape, like the fence, is a property of
	// the assembled line and cannot be decided a field at a time — see
	// String.
	parts []part
}

// partKind says how String must treat a fragment's bytes.
type partKind int

const (
	partRaw     partKind = iota // glyph's own markup, byte-for-byte
	partFinal                   // author bytes already fully disarmed (Text)
	partProse                   // author prose, flattened but NOT yet escaped
	partMention                 // a handle, live and exempt from the fence
)

type part struct {
	kind partKind
	s    string
}

// handle is the username shape anchored to the WHOLE value: Mention trusts a
// name only when nothing but a single GitHub-linkable token is there.
var handle = regexp.MustCompile(`^(?:` + username + `)$`)

// Raw appends s byte-for-byte. It is for glyph's OWN markup — a list bullet, an
// emoji, the bold around a scope, a short SHA — and never for author-supplied
// bytes. That trust classification is the one thing this type cannot check; what
// it does guarantee is that even a misclassified byte stays out of the mention
// hole, because String's fence walks the whole assembled line, Raw stretches
// included.
func (l *Line) Raw(s string) {
	l.parts = append(l.parts, part{partRaw, s})
}

// Text appends an author-supplied PLAIN-TEXT field — data, not prose, like the
// commit scope. The field is flattened to one line and every escapable byte is
// disarmed (escapeText): a data field is exactly the statement that no grammar
// applies to it, so nothing in it may become markup.
func (l *Line) Text(s string) {
	l.parts = append(l.parts, part{partFinal, escapeText(flatten(s))})
}

// Prose appends an author-supplied PROSE field — text the author meant to be
// read, like the commit subject. The field is flattened to one line here; the
// neutralizing happens in String, against the assembled line's code spans and
// not this field's, because a backtick run in one field pairs with a run in
// another (t-9np1). The author's code spans, emphasis and strikethrough keep
// rendering; the constructs that can inject structure, point somewhere the
// author never wrote or steal a code-span delimiter are disarmed.
func (l *Line) Prose(s string) {
	l.parts = append(l.parts, part{partProse, flatten(s)})
}

// Mention appends the author credit the caller MEANS to page (ratified
// 2026-08-17; by identity since t-39fy): login is the GitHub account that
// authored the commit as GitHub established it — the API's author.login or
// a noreply address — and name is git's free-text %an. The template's
// literal at-sign plus the LOGIN renders as a live @mention instead of being
// fenced: the one deliberate hole in the fence, gated twice. By identity: an
// empty login is the statement that nobody established one, and the name is
// never promoted to stand in for it, because a name is not a login and no
// shape tells them apart — "Saleh" is one handle-shaped token and is
// github.com/larrasket, so @Saleh paged a stranger (golang/tools, measured
// 2026-09-13). And by shape, still: a login goes live only when it is exactly
// one GitHub-handle-shaped token (alphanumerics and interior hyphens), so
// "dependabot[bot]" and an empty resolve cannot open the hole either.
//
// A credit that cannot go live is written as prose, by NAME, and the
// at-sign the caller wrote directly before it is dropped: "@Robert Pająk"
// is not a mention of anyone, and fencing it left `@Robert` in code font
// with the surname outside (opentelemetry-go, measured 2026-09-13). The
// at-sign is the caller's template's ("@$author"), so a template without one
// renders the bare name and mentions nobody — same as before.
func (l *Line) Mention(login, name string) {
	if !handle.MatchString(login) {
		if n := len(l.parts) - 1; n >= 0 && l.parts[n].kind == partRaw {
			l.parts[n].s = strings.TrimSuffix(l.parts[n].s, "@")
		}
		l.Prose(name)
		return
	}
	l.parts = append(l.parts, part{partMention, login})
}

// String assembles the line and runs the two whole-line passes, in order: the
// prose escape, then the mention fence.
//
// Both are properties of the assembled line, and for the same reason. A code
// span is what makes escapeProseLine leave author bytes alone, and CommonMark
// pairs backtick RUNS across the whole inline context — which is the line, not
// the field. Escaping each field against its own spans therefore asked the
// wrong question, and answered it in the under-escaping direction that
// escape.go's ONE DESIGN RULE forbids: a subject ending in an unclosed backtick
// re-paired with the first backtick of the author name, glyph believed the
// bytes between them were inside a code span, and `[CLICK ME](https://evil)`
// written across that seam reached a published release body LIVE — on the
// shipped preset, whose `- $subject$[ ($pr)] @$author` puts two prose fields in
// one context, and with `glyph lint` exiting 0 on the commit (t-9np1, measured
// at e41051d). The fence pass had already been moved here for the mirror-image
// bug; this is the same move for the escape pass.
//
// The mention exemption is unchanged: ranges Mention wrote stay live. A token
// that only PARTLY overlaps such a range (template glue extending the name, a
// chain swallowing it) is fenced whole — the exemption never widens. Calling
// String again returns the same bytes: both passes are fixed points and the
// parts are not consumed.
func (l *Line) String() string {
	// The span scan must see the bytes GitHub will see, so Text is already
	// escaped (its backticks carry a backslash and open nothing) and Prose is
	// flattened but raw. Prose is the only kind escaping may still rewrite.
	var pre strings.Builder
	var owner []partKind
	for _, p := range l.parts {
		pre.WriteString(p.s)
		// By BYTE, not by rune: `for range p.s` walks runes, and one multi-byte
		// glue (an em dash in a note.line) then shifts every ownership answer
		// after it — the fuzzer caught exactly that, and a prose '<' read as
		// Raw is a live '<'.
		for i := 0; i < len(p.s); i++ {
			owner = append(owner, p.kind)
		}
	}
	assembled := pre.String()

	// One scan, over the whole line.
	inSpan := make([]bool, len(assembled))
	for _, sp := range codeSpans(assembled) {
		for i := sp[0]; i < sp[1] && i < len(assembled); i++ {
			inSpan[i] = true
		}
	}
	prose := func(lo, hi int) bool {
		for i := max(lo, 0); i < min(hi, len(owner)); i++ {
			if owner[i] == partProse {
				return true
			}
		}
		return false
	}

	escaped, pos := escapeProseLine(assembled, inSpan, prose)

	// Mention's exemptions, re-found in the escaped string through pos. A
	// handle carries none of the bytes escapeProseLine rewrites, so its own
	// bytes are untouched; only its OFFSET moves.
	var live [][2]int
	at := 0
	for _, p := range l.parts {
		if p.kind == partMention {
			live = append(live, [2]int{pos[at], pos[at+len(p.s)]})
		}
		at += len(p.s)
	}

	return escapeMentionsSkipping(escaped, live)
}
