package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// varRE is the $xxx placeholder grammar of note.line. RE2 group names share
// it, which is what makes "a placeholder is a group name" hold — so the
// grammar lives beside the patterns whose groups it reads, not in the
// renderer that consumes it.
var varRE = regexp.MustCompile(`\$[a-z_][a-z0-9_]*`)

// spanOpen marks an optional span. `$[` was literal under varRE before this
// existed (`[` is outside the name alphabet), so every template already in
// the fleet parses unchanged; a bare `[` stays literal too, which is what
// leaves the `- [$scope] $subject` idiom and Markdown links alone.
const spanOpen = "$["

// Built-in placeholder names. glyph binds these whatever the patterns capture,
// and they outrank a pattern group of the same name — which is what lets a
// template rely on them resolving in every repository. The renderer keys its
// map off these constants so the set validated at load and the set bound at
// render cannot drift apart.
const (
	BuiltinPR     = "pr"
	BuiltinAuthor = "author"
	BuiltinHash   = "hash"
)

// LineBuiltins is the same set as a list, for validation and for messages.
var LineBuiltins = []string{BuiltinPR, BuiltinAuthor, BuiltinHash}

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

// ParseLine compiles a note.line template into spans, rejecting rather than
// repairing three malformed shapes: an unterminated `$[`, a nested one, and a
// span holding no placeholder. Callers parse at LOAD time so a bad template
// fails the config — the same exit every other config error takes — rather
// than the release that would have rendered it.
//
// An empty template is not an error: [note] is optional, and a config with no
// line renders no lines.
func ParseLine(template string) ([]LineSpan, error) {
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
// Brackets inside a span NEST — a Markdown link and the `[$scope]` idiom both
// live inside one — so the closing bracket is the first at depth zero, not
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

// validateLineNames refuses a $placeholder that nothing can ever fill: not a
// built-in, and captured by none of the file's patterns.
//
// ParseLine already refuses a span with no placeholder — "it says optional and
// means always". This is the mirror, and the span made it worse: a name nothing
// binds resolves empty for every commit, so inside a span it drops the span on
// every line and takes the punctuation with it, leaving a release body quietly
// missing a column rather than visibly wrong. Measured before this check
// existed: `$[ ($pull)]` — the built-in is `$pr` — rendered every line with no
// citation and no parens, at exit 0, while the same name OUTSIDE a span at
// least rendered a visible empty `()`. Optional punctuation is only safe to
// offer if a typo inside it cannot pass for a deliberate omission.
//
// The legal set is the UNION over patterns, not the intersection: which pattern
// wins is a property of each commit, so a name any pattern captures is a name
// the template may cite.
func validateLineNames(spans []LineSpan, patterns []Pattern) error {
	legal := make(map[string]bool, len(LineBuiltins))
	for _, b := range LineBuiltins {
		legal[b] = true
	}
	for _, p := range patterns {
		for _, name := range p.re.SubexpNames() {
			if name != "" {
				legal[name] = true
			}
		}
	}

	known := slices.Sorted(maps.Keys(legal))
	for _, span := range spans {
		for _, part := range span.Parts {
			if part.Placeholder && !legal[part.Text] {
				return fmt.Errorf("$%s is not a built-in and no pattern captures it, so it resolves empty for every commit; the names this file can bind are: %s", part.Text, strings.Join(known, ", "))
			}
		}
	}
	return nil
}
