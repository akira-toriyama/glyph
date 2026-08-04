package markdown

import "strings"

// Line assembles one GitHub-rendered Markdown line from glyph's own markup and
// author-supplied fields, applying this package's escaping pipeline in the only
// order that is safe: per field first, then the mention fence once, over the
// assembled whole. It is the package's ONLY exported surface, and that is the
// point (ratified 2026-07-22, t-3f4s): the order used to be a contract written
// in doc comments on two call sites, and a third caller handing raw author text
// to the mention fence would have resurrected the phantom-span hole SILENTLY —
// codeSpans is a backtick-only model, exact only after escapeMarkup has removed
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
//	l.Prose(subject)   // author PROSE: flattened, disarmed, still readable
//	body := l.String() // the mention fence, ONCE, over the assembled line
//
// Why the fence comes last and sees everything: mention-safety is a property of
// the rendered INLINE CONTEXT and of no field in it. The fence must be longer
// than every backtick run it will share a context with, and the fields share
// one — sizing the subject's fence against the subject alone let a backtick
// carried by the SCOPE steal it, and the assembled line was a live mention
// (measured against GitHub 2026-07-21; the incident is written out at
// notes.entryLine). The order is forced from the other side too: the fence
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
	b strings.Builder
}

// Raw appends s byte-for-byte. It is for glyph's OWN markup — a list bullet, an
// emoji, the bold around a scope, a short SHA — and never for author-supplied
// bytes. That trust classification is the one thing this type cannot check; what
// it does guarantee is that even a misclassified byte stays out of the mention
// hole, because String's fence walks the whole assembled line, Raw stretches
// included.
func (l *Line) Raw(s string) {
	l.b.WriteString(s)
}

// Text appends an author-supplied PLAIN-TEXT field — data, not prose, like the
// commit scope. The field is flattened to one line and every escapable byte is
// disarmed (escapeText): a data field is exactly the statement that no grammar
// applies to it, so nothing in it may become markup.
func (l *Line) Text(s string) {
	l.b.WriteString(escapeText(flatten(s)))
}

// Prose appends an author-supplied PROSE field — text the author meant to be
// read, like the commit subject. The field is flattened to one line and
// neutralized (escapeMarkup): the author's code spans, emphasis and
// strikethrough keep rendering, while the constructs that can inject structure,
// point somewhere the author never wrote or steal a code-span delimiter are
// disarmed.
func (l *Line) Prose(s string) {
	l.b.WriteString(escapeMarkup(flatten(s)))
}

// String returns the assembled line with every would-be @mention fenced — the
// LAST pass, over the WHOLE line, its fence sized against every backtick run
// any fragment contributed. Calling it again returns the same bytes: the fence
// is a fixed point and the builder is not consumed.
func (l *Line) String() string {
	return escapeMentions(l.b.String())
}
