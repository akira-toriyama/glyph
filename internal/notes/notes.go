// Package notes turns already-participating commits into section-grouped
// release notes. It is pure — no I/O, no globals — and layers on parser.Commit
// and the gitmoji table: grouping follows each rule's section, a breaking
// commit is hoisted into the Breaking Changes section whatever its gitmoji,
// and a commit whose code carries no section stays out entirely. Inclusion
// tracks the section, not the bump, so a none-bump removal (:fire:/:coffin:/
// :truck:, section Removals) still surfaces. Participation policy (bots, merges,
// autosquash artifacts) is internal/bump's Excluded; reading commits belongs
// to internal/gitsource — neither happens here.
package notes

import (
	"slices"
	"strings"
	"text/template"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
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
// order, one `- <emoji> [**scope:** ]<subject> (<short sha>)` line per entry,
// one blank line between sections, subjects verbatim (own-repo content is
// trusted; GitHub autolinks the bare short SHA).
var tmpl = template.Must(template.New("notes").Funcs(template.FuncMap{
	"short": shortSHA,
}).Parse(`{{- range $i, $s := . -}}
{{- if $i}}
{{end -}}
## {{$s.Title}}

{{range $s.Entries -}}
- {{.Emoji}} {{with .Scope}}**{{.}}:** {{end}}{{.Subject}} ({{short .SHA}})
{{end -}}
{{- end -}}
`))

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
