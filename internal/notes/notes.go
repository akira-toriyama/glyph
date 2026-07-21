// Package notes turns already-participating commits into section-grouped
// release notes. It is pure — no I/O, no globals — and layers on parser.Commit
// and the gitmoji table: grouping follows each rule's section, a breaking
// commit is hoisted into the Breaking Changes section whatever its gitmoji,
// and a commit whose code carries no section stays out entirely. Inclusion
// tracks the section, not the bump, so a none-bump removal (:fire:/:coffin:/
// :truck:, section Removals) still surfaces. Participation policy (bots, merges,
// autosquash artifacts) is internal/bump's ExcludedFromClassification; reading
// commits belongs to internal/gitsource — neither happens here.
package notes

import (
	"slices"
	"strings"
	"text/template"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/markdown"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// BreakingSection is the section title breaking commits are hoisted into.
const BreakingSection = "Breaking Changes"

// Entry is one release-notes line: the rule's emoji plus the commit's own
// scope, subject, and SHA. Breaking records the orthogonal flag (a `!` or a
// BREAKING CHANGE footer) — a plain :boom: lands here via its section instead.
type Entry struct {
	SHA      string `json:"sha"`
	Code     string `json:"code"`
	Emoji    string `json:"emoji"`
	Scope    string `json:"scope,omitempty"`
	Subject  string `json:"subject"`
	Breaking bool   `json:"breaking"`
}

// Section is one rendered group: a title from the table's section list and its
// entries in commit order (oldest first).
type Section struct {
	Title   string  `json:"title"`
	Entries []Entry `json:"entries"`
}

// Group maps commits onto the table's sections in the table's render order.
// A breaking commit hoists into BreakingSection whatever its code's rung or
// home section; a non-breaking commit whose code carries no section is dropped
// (kept in history, excluded from notes) — so a none-bump removal still lands in
// its Removals section; an unknown code is a hard lint error, mirroring
// bump.Classify — never a silent skip. Sections without entries are omitted,
// so a sectionless input returns an empty list.
func Group(commits []parser.Commit, t *gitmoji.Table) ([]Section, error) {
	byTitle := make(map[string][]Entry)
	for _, c := range commits {
		rule, ok := t.Lookup(c.Gitmoji)
		if !ok {
			where := ""
			if c.SHA != "" {
				where = " in commit " + c.SHA
			}
			return nil, core.Lintf("unknown gitmoji %s%s: not in the embedded rules table (see `glyph rules`)", c.Gitmoji, where)
		}
		var title string
		switch {
		case c.Breaking:
			title = BreakingSection
		case rule.Section == "":
			continue
		default:
			title = rule.Section
		}
		byTitle[title] = append(byTitle[title], Entry{
			SHA:      c.SHA,
			Code:     rule.Code,
			Emoji:    rule.Emoji,
			Scope:    c.Scope,
			Subject:  c.Subject,
			Breaking: c.Breaking,
		})
	}

	// The render order is the table's section list. The embedded table carries
	// BreakingSection (test-pinned); a hypothetical table without it still must
	// not drop hoisted entries, so it is prepended as a guard.
	titles := t.Sections
	if !slices.Contains(titles, BreakingSection) {
		titles = append([]string{BreakingSection}, titles...)
	}
	var sections []Section
	for _, title := range titles {
		if entries := byTitle[title]; len(entries) > 0 {
			sections = append(sections, Section{Title: title, Entries: entries})
		}
	}
	return sections, nil
}

// tmpl is the Markdown shape of the notes: `## <section>` headings in group
// order, one entry line each (drawn by entryLine), one blank line between
// sections. GitHub autolinks the bare short SHA.
//
// Nothing author-supplied reaches this template: every such byte comes in
// through entryLine, which is where the escaping is. "Own-repo content is
// trusted as Markdown" is what this comment used to say, and it was wrong in
// both directions — a subject's raw HTML broke the notes AND the sanitizer
// deleted the author's own words (t-j0c6).
var tmpl = template.Must(template.New("notes").Funcs(template.FuncMap{
	"line": entryLine,
}).Parse(`{{- range $i, $s := . -}}
{{- if $i}}
{{end -}}
## {{$s.Title}}

{{range $s.Entries -}}
{{line .}}
{{end -}}
{{- end -}}
`))

// entryLine draws one entry — `- <emoji> [**scope:** ]<subject> (<short sha>)`
// — from author-supplied fields, in three tiers.
//
// PER FIELD, before assembly: flatten, then neutralize according to what the
// field IS. The scope is data (a subsystem name) so it is escaped as plain text;
// the subject is prose the author meant to be read, so its code spans and
// emphasis survive and only the constructs that can inject structure, fetch a
// remote resource or delete the author's own words are disarmed. Both policies
// live in internal/markdown, which carries the measurements.
//
// Per FIELD because the two policies differ, and BEFORE assembly because the
// neutralizers must see author bytes only — run over the finished line they
// would escape glyph's own "**" and "- " markup into a different problem.
//
// ONCE, OVER THE ASSEMBLED LINE: the mention fence. Mention-safety is a property
// of the rendered INLINE CONTEXT and not of any field that lands in it. A fence
// has to be longer than every backtick run it will share a context with (see
// markdown.EscapeMentions), and these fields share one: escaping them
// separately sized the subject's fence against the subject alone, so a backtick
// carried by the SCOPE stole it. That parses and lints clean today — the legacy
// token grammar's scope slot is [^()]+ — and the assembled line was a live
// mention:
//
//	commit  :bug: fix(readme`): credit @alice and @bob for the fix
//	line    - 🐛 **readme`:** credit `@alice` and `@bob` for the fix (abc1234)
//	         ^ measured against GitHub 2026-07-21: @alice is LINKED, because the
//	           scope's stray backtick paired with the fence's opening one.
//
// So the fence is the LAST thing that happens to the line, and every byte of the
// line has passed through it. The order of the two is forced the other way too:
// the fence models the FINAL string, so neutralization cannot follow it — that
// would size a fence and choose spans against a construct set the next step then
// destroys.
//
// The line is assembled here, in Go, rather than field by field in the template
// above, so that both properties hold by construction. A field added to this
// function is covered; a field added to the template would not be, which is why
// there is nothing left in the template to add one to.
func entryLine(e Entry) string {
	var b strings.Builder
	b.WriteString("- " + e.Emoji + " ")
	if e.Scope != "" {
		b.WriteString("**" + markdown.EscapeText(markdown.Flatten(e.Scope)) + ":** ")
	}
	b.WriteString(markdown.EscapeMarkup(markdown.Flatten(e.Subject)))
	b.WriteString(" (" + shortSHA(e.SHA) + ")")
	return markdown.EscapeMentions(b.String())
}

// Render draws sections as Markdown — the body a release publishes, headed by
// no version line (the release title carries the version). No sections render
// to the empty string; the no-release verdict is the caller's to make.
func Render(sections []Section) (string, error) {
	var b strings.Builder
	if err := tmpl.Execute(&b, sections); err != nil {
		return "", core.APIf("rendering notes: %v", err)
	}
	return b.String(), nil
}

// shortSHA abbreviates a full SHA to the conventional seven characters; an
// already-short value passes through untouched.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
