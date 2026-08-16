// Package gitmoji is glyph's machine source of truth for the gitmoji → semver
// mapping, and — since the conventional profile (DESIGN §2.2) — the one table
// ENGINE both vocabularies load through. It embeds the pinned gitmoji table
// (rules.json) so the binary and its rules ship lockstep — no
// separately-synced config can drift from the code that reads it — and it
// holds the bump lattice, the shared Table model with its validation
// (ParseTable, parameterized by a Spec), and the two renderers (CanonicalJSON,
// Markdown) that back `glyph rules`. The engine lives HERE rather than in a
// third package because the validation rules — unique tokens, a valid bump, a
// section drawn from the declared list, a section mandatory on every
// version-moving entry — are the parts that must never fork between
// vocabularies; internal/conventional embeds its own data and calls in with
// its own Spec. No other I/O and no classification (that is internal/bump's
// job, layered on Lookup).
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
	BumpNone  Bump = "none"  // internal / non-shipping / meta — never moves the version; excluded from notes unless the code carries a section (removals do)
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

// Rule is one vocabulary entry: the token (a textual gitmoji code, or a
// conventional type — DESIGN §2.2 "the type plays the code's role", which is
// why the field and its JSON key stay `code` for both), the emoji and meaning,
// plus glyph's ratified bump and the release-notes section. The emoji is the
// gitmoji vocabulary's alone — a conventional entry has none, and its Spec
// says so. The section is decoupled from the bump: every version-moving entry
// carries one, and a none entry may carry one too — a removal
// (:fire:/:coffin:/:truck:) surfaces in the notes without moving the version;
// omitting the section keeps a commit out.
type Rule struct {
	Code    string `json:"code"`
	Emoji   string `json:"emoji,omitempty"`
	Meaning string `json:"meaning"`
	Bump    Bump   `json:"bump"`
	Section string `json:"section,omitempty"`
}

// Spec parameterizes the table engine for one vocabulary: what its tokens are
// called and shaped like, whether entries carry emoji, and how the Markdown
// surface titles itself. The two instances live beside their data — the
// gitmoji one below, the conventional one in internal/conventional — so a
// vocabulary and its spec cannot drift apart.
type Spec struct {
	Name    string         // vocabulary name; prefixes every validation error ("gitmoji", "conventional")
	Token   string         // what one entry's token is called in prose and headers ("code", "type")
	TokenRE *regexp.Regexp // the shape every token must satisfy
	Emoji   bool           // every entry must carry an emoji (and the Markdown grows the column)
	Title   string         // the Markdown H1
	Rules   string         // the bare self-printing command (`glyph rules`, plus the profile flag where one is needed); error prose and the Markdown comment derive from it
}

// gitmojiSpec is the gitmoji vocabulary's own Spec — the shape here is the
// same group-1 shape the linter uses to pull a code off a commit subject.
var gitmojiSpec = Spec{
	Name:    "gitmoji",
	Token:   "code",
	TokenRE: regexp.MustCompile(`^:[a-z0-9][a-z0-9_+-]*:$`),
	Emoji:   true,
	Title:   "gitmoji → semver",
	Rules:   "glyph rules",
}

// Table is the loaded, validated rules set of one vocabulary. Codes is kept
// in source order; byCode is the O(1) lookup index built at load; spec records
// which vocabulary this is, for the renderers and error prose, and stays out
// of serialization.
type Table struct {
	Version  string   `json:"version"`
	Sections []string `json:"sections"`
	Codes    []Rule   `json:"codes"`

	byCode map[string]Rule
	spec   Spec
}

// Spec returns the vocabulary spec this table was validated under.
func (t *Table) Spec() Spec { return t.spec }

// Lookup returns the rule for a textual gitmoji code (e.g. ":sparkles:") and
// whether it is known. An unknown code is a hard lint error upstream, never a
// silent patch.
func (t *Table) Lookup(code string) (Rule, bool) {
	r, ok := t.byCode[code]
	return r, ok
}

// Load parses and validates the embedded gitmoji rules table. It fails if the
// table is structurally invalid or not exactly CodeCount codes — a
// build/embedding error, surfaced at startup rather than as a silent
// misclassification later.
func Load() (*Table, error) {
	t, err := ParseTable(rawRules, gitmojiSpec)
	if err != nil {
		return nil, err
	}
	if len(t.Codes) != CodeCount {
		return nil, fmt.Errorf("gitmoji: embedded table has %d codes, want %d", len(t.Codes), CodeCount)
	}
	return t, nil
}

// ParseTable decodes and structurally validates one vocabulary's rules table
// under its spec: a version, a non-empty section list, and entries that are
// each a well-formed, unique token with a valid bump and section consistency
// (a version-moving entry carries a section; a none entry may carry one — a
// removal — or omit it; any section named is drawn from the section list).
// It does NOT enforce a count — that is each vocabulary's Load contract for
// its canonical embedded table (CodeCount here, TypeCount in
// internal/conventional), kept separate so this validator is reusable and
// testable.
func ParseTable(data []byte, spec Spec) (*Table, error) {
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("%s: rules.json is not valid JSON: %w", spec.Name, err)
	}
	if t.Version == "" {
		return nil, fmt.Errorf("%s: rules.json has no version", spec.Name)
	}
	if len(t.Sections) == 0 {
		return nil, fmt.Errorf("%s: rules.json has no sections", spec.Name)
	}
	sectionSet := make(map[string]bool, len(t.Sections))
	for _, s := range t.Sections {
		if s == "" {
			return nil, fmt.Errorf("%s: rules.json has an empty section name", spec.Name)
		}
		sectionSet[s] = true
	}
	byCode := make(map[string]Rule, len(t.Codes))
	for _, r := range t.Codes {
		if !spec.TokenRE.MatchString(r.Code) {
			return nil, fmt.Errorf("%s: %q is not a well-formed %s", spec.Name, r.Code, spec.Token)
		}
		if _, dup := byCode[r.Code]; dup {
			return nil, fmt.Errorf("%s: duplicate %s %q", spec.Name, spec.Token, r.Code)
		}
		if !r.Bump.Valid() {
			return nil, fmt.Errorf("%s: %s %q has out-of-enum bump %q", spec.Name, spec.Token, r.Code, r.Bump)
		}
		// The emoji column belongs to one vocabulary: mandatory where the spec
		// says entries carry one, and refused where it says they do not — a
		// conventional type with an emoji would be data the renderers silently
		// drop, which is how a hand edit hides.
		if spec.Emoji && r.Emoji == "" {
			return nil, fmt.Errorf("%s: %s %q has no emoji", spec.Name, spec.Token, r.Code)
		}
		if !spec.Emoji && r.Emoji != "" {
			return nil, fmt.Errorf("%s: %s %q carries an emoji, which this vocabulary does not have", spec.Name, spec.Token, r.Code)
		}
		if r.Meaning == "" {
			return nil, fmt.Errorf("%s: %s %q has no meaning", spec.Name, spec.Token, r.Code)
		}
		// Notes visibility is decoupled from the bump. A version-moving entry
		// must carry a section (every shipping change is notes-visible); a none
		// entry may carry one — a removal (:fire:/:coffin:/:truck:) surfaces in
		// the notes without moving the version — or omit it to stay out. Any
		// section named must be drawn from the render-order list.
		if r.Bump != BumpNone && r.Section == "" {
			return nil, fmt.Errorf("%s: version-moving %s %q (%s) must carry a section", spec.Name, spec.Token, r.Code, r.Bump)
		}
		if r.Section != "" && !sectionSet[r.Section] {
			return nil, fmt.Errorf("%s: %s %q references unknown section %q", spec.Name, spec.Token, r.Code, r.Section)
		}
		byCode[r.Code] = r
	}
	t.byCode = byCode
	t.spec = spec
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
// docs-facing view of the embedded rules, and the drift-guard golden for the
// spec's own self-printing command. An entry with no section shows "—"; a
// removal none code shows its Removals section like any version mover. The
// emoji column exists only where the vocabulary does (Spec.Emoji) — the
// gitmoji rendering is byte-identical to what it was before the engine was
// parameterized, which its golden pins.
func (t *Table) Markdown() string {
	head := strings.ToUpper(t.spec.Token[:1]) + t.spec.Token[1:]
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", t.spec.Title)
	fmt.Fprintf(&b, "<!-- Generated by `%s --md`; do not edit by hand. -->\n\n", t.spec.Rules)
	fmt.Fprintf(&b, "Spec: **%s** · %d %ss · bump lattice `none < patch < minor < major`.\n\n",
		t.Version, len(t.Codes), t.spec.Token)

	fmt.Fprintf(&b, "## %ss\n\n", head)
	if t.spec.Emoji {
		fmt.Fprintf(&b, "| Emoji | %s | Meaning | Bump | Section |\n", head)
		b.WriteString("|-------|------|---------|------|---------|\n")
	} else {
		fmt.Fprintf(&b, "| %s | Meaning | Bump | Section |\n", head)
		b.WriteString("|------|---------|------|---------|\n")
	}
	for _, r := range t.Codes {
		section := r.Section
		if section == "" {
			section = "—"
		}
		if t.spec.Emoji {
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n",
				r.Emoji, r.Code, mdCell(r.Meaning), r.Bump, section)
		} else {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
				r.Code, mdCell(r.Meaning), r.Bump, section)
		}
	}

	b.WriteString("\n## Notes sections (render order)\n\n")
	for i, s := range t.Sections {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	return b.String()
}

// mdCell escapes the one character that would break a Markdown table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
