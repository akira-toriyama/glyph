package notes

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/markdown"
)

// SigilCommit is the slice of a commit the v2 notes read. Nothing is parsed
// here either: the pattern file decides what the message means, and Pull is
// whatever the walk resolved (0 = none).
type SigilCommit struct {
	SHA     string
	Pull    int
	Author  string
	Message string
}

// SigilSection is one rendered v2 group: the section's own title (the user's
// text, rendered as they wrote it) and its fully rendered lines in commit
// order.
type SigilSection struct {
	Title string
	Lines []string
}

// varRE is the $xxx placeholder grammar of the line template. RE2 group
// names share it, which is what makes "a placeholder is a group name" hold.
var varRE = regexp.MustCompile(`\$[a-z_][a-z0-9_]*`)

// GroupSigils maps commits onto the config's [[note.sections]], in the
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
func GroupSigils(commits []SigilCommit, cfg *config.Config) ([]SigilSection, error) {
	type judged struct {
		c       SigilCommit
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
			j.level = string(bump.SigilLevel(m.Sigil))
		} else {
			// The unmatched fallback the design names "use the raw first
			// line": the commit renders through the same template with
			// $subject bound to its first line and no other groups.
			first, _, _ := strings.Cut(c.Message, "\n")
			groups = map[string]string{"subject": first}
		}
		j.line = renderLine(cfg.Note.Line, c, groups)
		js = append(js, j)
	}

	sections := make([]SigilSection, 0, len(cfg.Note.Sections))
	for _, s := range cfg.Note.Sections {
		out := SigilSection{Title: s.Title}
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
// running over the assembled line (the same pipeline v1 lines go through —
// a subject must not be able to page someone from a release body).
// The built-ins $pr / $author / $hash are reserved: they win over a pattern
// group of the same name. A placeholder that is neither built-in nor a group
// of the winning pattern renders empty.
func renderLine(template string, c SigilCommit, groups map[string]string) string {
	builtins := map[string]string{
		"pr":     "",
		"author": c.Author,
		"hash":   shortSHA(c.SHA),
	}
	if c.Pull > 0 {
		builtins["pr"] = "#" + strconv.Itoa(c.Pull)
	}

	var l markdown.Line
	rest := template
	for {
		loc := varRE.FindStringIndex(rest)
		if loc == nil {
			l.Raw(rest)
			break
		}
		l.Raw(rest[:loc[0]])
		name := rest[loc[0]+1 : loc[1]]
		if v, ok := builtins[name]; ok {
			l.Prose(v)
		} else {
			l.Prose(groups[name])
		}
		rest = rest[loc[1]:]
	}
	return l.String()
}

// RenderSigils draws v2 sections as Markdown, mirroring the v1 body shape:
// `## title` headings in section order, lines under each, no version line
// (the release title carries the version). No sections render to the empty
// string; the no-release verdict is the caller's to make.
func RenderSigils(sections []SigilSection) string {
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
