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
	BuiltinPR        = "pr"
	BuiltinAuthor    = "author"
	BuiltinHash      = "hash"
	BuiltinCoauthors = "coauthors"
)

// LineBuiltins is the same set as a list, for validation and for messages.
var LineBuiltins = []string{BuiltinPR, BuiltinAuthor, BuiltinHash, BuiltinCoauthors}

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

// validateLineNames refuses a $placeholder that no rendered line can fill:
// not a built-in, not a declared trailer, and captured by no pattern whose
// groups a commit binds — and $subject that only the fallback binds, unless
// the template cites a pattern group beside it.
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
// because every line a pattern claims would render without its text.
// Measured (t-f2cb, DESIGN §3): a skip pattern's $branch loaded and rendered
// empty at exit 0; a file whose subject group is `title` was refused
// `- $title$subject`, the one spelling that renders a bot line's text; and
// with $subject legal unconditionally, the gemoji preset with its group
// renamed to `title` and note.line left alone loaded and printed every
// matched commit's line with its text gone, at exit 0.
func validateLineNames(spans []LineSpan, patterns []Pattern, trailers []NoteTrailer) error {
	groups := boundGroupNames(patterns)
	legal := make(map[string]bool, len(LineBuiltins)+len(groups)+len(trailers))
	for _, b := range LineBuiltins {
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
				if groups[part.Text] && !slices.Contains(LineBuiltins, part.Text) {
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
