package notes

import (
	"strconv"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/markdown"
)

// Commit is the slice of a commit the notes read. Nothing is parsed
// here either: the pattern file decides what the message means, and Pull is
// whatever the walk resolved (0 = none).
type Commit struct {
	SHA     string
	Pull    int
	Author  string
	Login   string // the author.login GitHub attached to the commit when the API described it, "" otherwise — the one source of a live credit
	Message string // the text every verdict judges (gitsource.RawCommit.Message): what the grammar matches and the fallback's first line
	// Recorded is the message as git recorded it, closing newline included
	// (gitsource.RawCommit.Recorded), and the ONLY text the trailer block is
	// read off: git's reader reads the object's bytes, and the newline decides
	// whether a final `Conflicts:` line is its ignored list or an empty
	// trailer (DESIGN §3). A Commit built with Message alone has no trailers.
	Recorded string
}

// Section is one rendered group: the section's own title (the user's
// text, rendered as they wrote it) and its fully rendered lines in commit
// order.
type Section struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

// Group maps commits onto the config's [[note.sections]], in the
// config's order — order is render order, and a commit lands in EVERY
// section whose filter matches it (a major by dependabot sits in Breaking
// Changes and in Dependencies both; deduplicating would make section order
// silently decide which one wins).
//
// Who lands where follows the two axes: a semver section holds the matched
// commits whose sigil folds to its level, so a commit no pattern matched —
// legal here whenever the author is excluded from the fold — has no level
// and can only appear in author sections. A skip-pattern commit is in no
// section at all: skip drops a commit from lint, bump AND notes, which is
// exactly what separates it from exclude_authors (excluded authors still
// appear wherever note.sections says they do).
//
// exclude_authors is deliberately not consulted: whether a commit appears in
// the notes is note.sections' decision alone.
func Group(commits []Commit, cfg *config.Config) ([]Section, error) {
	type judged struct {
		c       Commit
		matched bool
		level   string // meaningful only when matched
		line    string
	}
	js := make([]judged, 0, len(commits))
	for _, c := range commits {
		m, err := cfg.Match(c.Message)
		if err != nil {
			return nil, core.Lintf("commit %s: %v", c.SHA, err)
		}
		if m.Matched && m.Skip {
			continue
		}
		j := judged{c: c, matched: m.Matched}
		groups := m.Groups
		if m.Matched {
			j.level = string(bump.LevelOf(m.Sigil))
		} else {
			// The unmatched fallback the design names "use the raw first
			// line": the commit renders through the same template with
			// $subject bound to its first line and no other groups.
			groups = map[string]string{config.FallbackGroup: bump.FirstLine(c.Message)}
		}
		j.line = renderLine(cfg.Note.Spans, cfg.Note.Trailers, c, groups)
		js = append(js, j)
	}

	sections := make([]Section, 0, len(cfg.Note.Sections))
	for _, s := range cfg.Note.Sections {
		out := Section{Title: s.Title}
		for _, j := range js {
			switch s.Axis {
			case config.AxisSemver:
				if j.matched && j.level == s.Value {
					out.Lines = append(out.Lines, j.line)
				}
			case config.AxisAuthor:
				if j.c.Author == s.Value {
					out.Lines = append(out.Lines, j.line)
				}
			}
		}
		if len(out.Lines) > 0 {
			sections = append(sections, out)
		}
	}
	return sections, nil
}

// renderLine substitutes the template's $xxx placeholders. Literal template
// text is the user's own markdown and passes through raw; substituted values
// are commit-derived text and are escaped as prose, with the mention fence
// running over the assembled line (markdown.Line's pipeline — a subject
// must not be able to page someone from a release body). The one
// exemption is the built-in $author: markdown.Line.Mention renders the
// API-attached, handle-shaped login live and the name otherwise (DESIGN §3,
// "`note.line` and the optional span.").
// Group-derived values and the other built-ins keep the fence: whether a
// SUBJECT can page someone is not the author's intent to declare.
// The built-ins (config.BuiltinPR and its siblings) and the declared
// trailers share one map, consulted before the winning pattern's groups, so
// both win over a group of the same name. A placeholder that is neither
// renders empty. A scope is prose like any other value: DESIGN §2 argues why
// no group gets a plain-text route.
//
// An optional span drops WITH its literal text when any placeholder inside it
// resolves empty. That is the whole point of the span: $pr is empty for every
// commit under --range and for every direct push under --since-tag, so parens
// written around it would render around nothing (DESIGN §3 has the measured
// line).
func renderLine(spans []config.LineSpan, declared []config.NoteTrailer, c Commit, groups map[string]string) string {
	builtins := map[string]string{
		config.BuiltinPR:        "",
		config.BuiltinAuthor:    c.Author,
		config.BuiltinHash:      shortSHA(c.SHA),
		config.BuiltinCoauthors: strings.Join(coAuthorNames(c.Recorded), ", "),
	}
	if c.Pull > 0 {
		builtins[config.BuiltinPR] = "#" + strconv.Itoa(c.Pull)
	}
	// A declared trailer resolves like a pattern group: absent means empty,
	// which drops the optional span that carries it. Written into the
	// built-ins map, where a trailer named like a built-in would replace it;
	// config.buildTrailers refuses such a name, so the collision never
	// reaches here and the order a template sees is trailer > group.
	for _, t := range declared {
		builtins[t.Name] = trailerValue(c.Recorded, t.Token)
	}
	resolve := func(name string) string {
		if v, ok := builtins[name]; ok {
			return v
		}
		return groups[name]
	}

	var l markdown.Line
	for _, span := range spans {
		if span.Optional && !allResolve(span, resolve) {
			continue
		}
		for _, p := range span.Parts {
			switch {
			case p.Placeholder && p.Text == config.BuiltinAuthor:
				l.Mention(c.Login, c.Author)
			case p.Placeholder:
				l.Prose(resolve(p.Text))
			default:
				l.Raw(p.Text)
			}
		}
	}
	return l.String()
}

// allResolve says whether every placeholder in the span has a value. EVERY,
// not any: a span carries one piece of punctuation around its placeholders,
// so a partly-resolved span renders that punctuation around a hole.
func allResolve(span config.LineSpan, resolve func(string) string) bool {
	for _, p := range span.Parts {
		if p.Placeholder && resolve(p.Text) == "" {
			return false
		}
	}
	return true
}

// shortSHA abbreviates a full SHA to the conventional seven characters; an
// already-short value passes through untouched.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Render draws sections as Markdown:
// `## title` headings in section order, lines under each, no version line
// (the release title carries the version). No sections render to the empty
// string; the no-release verdict is the caller's to make.
func Render(sections []Section) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + s.Title + "\n\n")
		for _, line := range s.Lines {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
