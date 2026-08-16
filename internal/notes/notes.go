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
	"strconv"
	"strings"
	"text/template"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/markdown"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// BreakingSection is the section title breaking commits are hoisted into.
const BreakingSection = "Breaking Changes"

// Commit is one participating commit plus the citation only the caller knows:
// the merged pull request that landed it, 0 when none is known. The SHA rides
// in the embedded parser.Commit, and a caller that knows the sha is an identity
// no branch holds — the release walk's footprint-less arm, where a squash left
// the pull's listed commits with no landing site of their own — BLANKS it
// rather than flagging it: the notes cite what exists, and a sha that exists on
// no branch is not data with a bad label, it is absence (measured: a published
// body citing shas `git branch -r --contains` answers nothing for, t-xxhj).
// This package renders what it is given and holds no arm knowledge; which arm
// a commit came down is DESIGN §4's fact and stays in the walk.
type Commit struct {
	parser.Commit
	Pull int
}

// Entry is one release-notes line: the rule's emoji plus the commit's own
// scope, subject, and citation — the pull that landed it and/or its SHA.
// Breaking records the orthogonal flag (a `!` or a BREAKING CHANGE footer) — a
// plain :boom: lands here via its section instead.
type Entry struct {
	SHA      string `json:"sha"`
	Pull     int    `json:"pull,omitempty"`
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
func Group(commits []Commit, t *gitmoji.Table) ([]Section, error) {
	byTitle := make(map[string][]Entry)
	for _, c := range commits {
		rule, ok := t.Lookup(c.Token)
		if !ok {
			where := ""
			if c.SHA != "" {
				where = " in commit " + c.SHA
			}
			return nil, core.Lintf("unknown %s %s%s: not in the embedded rules table (see `%s`)", t.Spec().Token, c.Token, where, t.Spec().Rules)
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
			Pull:     c.Pull,
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
// sections. GitHub autolinks both citation halves — the bare short SHA and the
// `#N` pull reference.
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

// entryLine draws one entry — `- <emoji> [**scope:** ]<subject> (<citation>)`
// — from author-supplied fields, in three tiers. The citation is
// `#<pull>, <short sha>` when both are known, either alone otherwise, and
// omitted entirely when the entry carries neither — never an empty `()`.
// The pull is ADDED beside the sha, not put in its place: within one pull the
// sha is what keeps N entries N distinct lines, and it is the *landed*
// identity (glossary) wherever one exists. Only a commit whose sha the caller
// blanked — landed under no identity of its own — cites the pull alone, the
// one address of it that outlives the squash.
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
// has to be longer than every backtick run it will share a context with, and
// these fields share one: escaping them separately sized the subject's fence
// against the subject alone, so a backtick carried by the SCOPE stole it. That
// parses and lints clean today — the legacy token grammar's scope slot is
// [^()]+ — and the assembled line was a live mention:
//
//	commit  :bug: fix(readme`): credit @alice and @bob for the fix
//	line    - 🐛 **readme`:** credit `@alice` and `@bob` for the fix (abc1234)
//	         ^ measured against GitHub 2026-07-21: @alice is LINKED, because the
//	           scope's stray backtick paired with the fence's opening one.
//
// Both orderings are markdown.Line's to enforce now — this function only says
// which policy each field gets (the scope is Text, the subject is Prose), and
// CANNOT run the passes out of order, because the fence is no longer callable:
// Line runs it inside String, last, over every byte (t-3f4s).
//
// The line is assembled here, in Go, rather than field by field in the template
// above, so that both properties hold by construction. A field added to this
// function is covered; a field added to the template would not be, which is why
// there is nothing left in the template to add one to.
func entryLine(e Entry) string {
	var l markdown.Line
	l.Raw("- " + e.Emoji + " ")
	if e.Scope != "" {
		l.Raw("**")
		l.Text(e.Scope)
		l.Raw(":** ")
	}
	l.Prose(e.Subject)
	switch {
	case e.Pull > 0 && e.SHA != "":
		l.Raw(" (#" + strconv.Itoa(e.Pull) + ", " + shortSHA(e.SHA) + ")")
	case e.Pull > 0:
		l.Raw(" (#" + strconv.Itoa(e.Pull) + ")")
	case e.SHA != "":
		l.Raw(" (" + shortSHA(e.SHA) + ")")
	}
	return l.String()
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
