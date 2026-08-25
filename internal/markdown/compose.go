package markdown

import (
	"regexp"
	"strings"
)

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
	// live holds the byte ranges Mention wrote: name bytes the fence pass must
	// leave alone. They are guaranteed handle-shaped (Mention falls back to
	// Prose otherwise), so they carry no backtick and no at-sign — which is what
	// keeps String's whole-line fence sizing and code-span model exact even
	// though these bytes are skipped when fencing.
	live [][2]int
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

// Mention appends a name the caller MEANS to page — the release-notes author
// credit (ratified 2026-08-17): the template's literal at-sign plus this value
// renders as a live @mention instead of being fenced. It is the one deliberate
// hole in the fence, and it is gated by shape, not by trust in the caller: the
// value goes live only when it is exactly one GitHub-handle-shaped token
// (alphanumerics and interior hyphens). Anything else — a git author name with
// a space, "dependabot[bot]", an empty resolve — falls back to Prose and stays
// fenced, because the value here is git's free-text %an, and "@Akira Toriyama"
// would page the stranger @Akira (the t-hykw incident, from the other
// direction). The safety property the fence exists for is thus preserved:
// no author-controlled FREE TEXT can page anyone; going live requires the
// whole field to be nothing but the handle.
//
// The at-sign itself is not written here on purpose. It belongs to the
// caller's template ("@$author"), so a template without one renders the bare
// name and mentions nobody — same as today.
func (l *Line) Mention(s string) {
	if !handle.MatchString(s) {
		l.Prose(s)
		return
	}
	start := l.b.Len()
	l.b.WriteString(s)
	l.live = append(l.live, [2]int{start, l.b.Len()})
}

// String returns the assembled line with every would-be @mention fenced — the
// LAST pass, over the WHOLE line, its fence sized against every backtick run
// any fragment contributed — except inside the ranges Mention wrote, which are
// the one deliberate exemption and are handle-shaped by construction. A token
// that only PARTLY overlaps such a range (template glue extending the name,
// a chain swallowing it) is fenced whole: the exemption never widens. Calling
// String again returns the same bytes: the fence is a fixed point and the
// builder is not consumed.
func (l *Line) String() string {
	return escapeMentionsSkipping(l.b.String(), l.live)
}
