// Package gitmoji is glyph's machine source of truth for the gitmoji → semver
// mapping. It embeds the pinned rules table (rules.json) so the binary and its
// rules ship lockstep — no separately-synced config can drift from the code that
// reads it. It holds the table, the bump lattice, and the two renderers
// (CanonicalJSON, Markdown) that back `glyph rules`; it performs no other I/O and
// no classification (that is internal/bump's job, layered on Lookup).
package gitmoji

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// rawRules is the pinned gitmoji → semver table. //go:embed binds it at compile
// time, so the shipped binary is the shipped rules — zero skew.
//
//go:embed rules.json
var rawRules []byte

// CodeCount is the size of the ratified gitmoji set. Growing the spec is a
// deliberate act: bump this and rules.json together so a dropped or added code
// can never slip in silently. Load fails if the embedded table is not this size.
const CodeCount = 75

// Bump is a rung on the semver lattice none < patch < minor < major. It is a
// string so it round-trips through rules.json as a human-readable token.
type Bump string

const (
	BumpNone  Bump = "none"  // internal / non-shipping / meta — never moves the version, excluded from notes
	BumpPatch Bump = "patch" // a shipped, user-observable change
	BumpMinor Bump = "minor" // a new feature (only :sparkles: auto-minors)
	BumpMajor Bump = "major" // a breaking change (only :boom: auto-majors)
)

// Rank projects a Bump onto the lattice ordering (0..3); an unknown bump is -1.
// The max-fold over a PR's commits compares ranks, so the fold is
// order-independent and idempotent.
func (b Bump) Rank() int {
	switch b {
	case BumpNone:
		return 0
	case BumpPatch:
		return 1
	case BumpMinor:
		return 2
	case BumpMajor:
		return 3
	default:
		return -1
	}
}

// Valid reports whether b is one of the four defined rungs.
func (b Bump) Valid() bool { return b.Rank() >= 0 }

// Rule is one gitmoji's entry: the authoritative code / emoji / meaning from the
// gitmoji spec, plus glyph's ratified bump and (for version-moving codes) the
// release-notes section. A none rule carries no section (it never reaches notes).
type Rule struct {
	Code    string `json:"code"`
	Emoji   string `json:"emoji"`
	Meaning string `json:"meaning"`
	Bump    Bump   `json:"bump"`
	Section string `json:"section,omitempty"`
}

// Table is the loaded, validated rules set. Codes is kept in spec order; byCode
// is the O(1) lookup index built at Load and excluded from serialization.
type Table struct {
	Version  string   `json:"version"`
	Sections []string `json:"sections"`
	Codes    []Rule   `json:"codes"`

	byCode map[string]Rule
}

// Lookup returns the rule for a textual gitmoji code (e.g. ":sparkles:") and
// whether it is known. An unknown code is a hard lint error upstream, never a
// silent patch.
func (t *Table) Lookup(code string) (Rule, bool) {
	r, ok := t.byCode[code]
	return r, ok
}

// codeShapeRE is the textual-gitmoji shape a rule code must satisfy: a colon, a
// leading alphanumeric, then alphanumerics / _ + - , and a closing colon. It is
// the same group-1 shape the linter uses to pull a code off a commit subject.
var codeShapeRE = regexp.MustCompile(`^:[a-z0-9][a-z0-9_+-]*:$`)

// Load parses and validates the embedded rules table. It fails if the table is
// structurally invalid or not exactly CodeCount codes — a build/embedding error,
// surfaced at startup rather than as a silent misclassification later.
func Load() (*Table, error) {
	t, err := parseTable(rawRules)
	if err != nil {
		return nil, err
	}
	if len(t.Codes) != CodeCount {
		return nil, fmt.Errorf("gitmoji: embedded table has %d codes, want %d", len(t.Codes), CodeCount)
	}
	return t, nil
}

// parseTable decodes and structurally validates a rules table: a version, a
// non-empty section list, and codes that are each a well-formed, unique textual
// gitmoji with a valid bump and section-rung consistency (a none code carries no
// section; a version-moving code carries one drawn from the section list). It
// does NOT enforce the CodeCount — that is Load's contract for the canonical
// embedded table, kept separate so this validator is reusable and testable.
func parseTable(data []byte) (*Table, error) {
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("gitmoji: rules.json is not valid JSON: %w", err)
	}
	if t.Version == "" {
		return nil, fmt.Errorf("gitmoji: rules.json has no version")
	}
	if len(t.Sections) == 0 {
		return nil, fmt.Errorf("gitmoji: rules.json has no sections")
	}
	sectionSet := make(map[string]bool, len(t.Sections))
	for _, s := range t.Sections {
		if s == "" {
			return nil, fmt.Errorf("gitmoji: rules.json has an empty section name")
		}
		sectionSet[s] = true
	}
	byCode := make(map[string]Rule, len(t.Codes))
	for _, r := range t.Codes {
		if !codeShapeRE.MatchString(r.Code) {
			return nil, fmt.Errorf("gitmoji: %q is not a well-formed textual gitmoji code", r.Code)
		}
		if _, dup := byCode[r.Code]; dup {
			return nil, fmt.Errorf("gitmoji: duplicate code %q", r.Code)
		}
		if !r.Bump.Valid() {
			return nil, fmt.Errorf("gitmoji: code %q has out-of-enum bump %q", r.Code, r.Bump)
		}
		if r.Emoji == "" {
			return nil, fmt.Errorf("gitmoji: code %q has no emoji", r.Code)
		}
		if r.Meaning == "" {
			return nil, fmt.Errorf("gitmoji: code %q has no meaning", r.Code)
		}
		if r.Bump == BumpNone {
			if r.Section != "" {
				return nil, fmt.Errorf("gitmoji: none code %q must carry no section, has %q", r.Code, r.Section)
			}
		} else {
			if r.Section == "" {
				return nil, fmt.Errorf("gitmoji: version-moving code %q (%s) must carry a section", r.Code, r.Bump)
			}
			if !sectionSet[r.Section] {
				return nil, fmt.Errorf("gitmoji: code %q references unknown section %q", r.Code, r.Section)
			}
		}
		byCode[r.Code] = r
	}
	t.byCode = byCode
	return &t, nil
}

// CanonicalJSON re-emits the table as deterministic JSON (2-space indent, HTML
// escaping off, no trailing newline) — the form rules.json is stored in, so
// `glyph rules --json` reproduces the embedded source verbatim.
func (t *Table) CanonicalJSON() ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(t); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Markdown renders the table as a stable Markdown document: the human- and
// docs-facing view of the embedded rules, and the drift-guard golden for
// `glyph rules --md`. none codes show "—" for their (absent) section.
func (t *Table) Markdown() string {
	var b strings.Builder
	b.WriteString("# gitmoji → semver\n\n")
	b.WriteString("<!-- Generated by `glyph rules --md`; do not edit by hand. -->\n\n")
	fmt.Fprintf(&b, "Spec: **%s** · %d codes · bump lattice `none < patch < minor < major`.\n\n",
		t.Version, len(t.Codes))

	b.WriteString("## Codes\n\n")
	b.WriteString("| Emoji | Code | Meaning | Bump | Section |\n")
	b.WriteString("|-------|------|---------|------|---------|\n")
	for _, r := range t.Codes {
		section := r.Section
		if section == "" {
			section = "—"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n",
			r.Emoji, r.Code, mdCell(r.Meaning), r.Bump, section)
	}

	b.WriteString("\n## Notes sections (render order)\n\n")
	for i, s := range t.Sections {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	return b.String()
}

// mdCell escapes the one character that would break a Markdown table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
