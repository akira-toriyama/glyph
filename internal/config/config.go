// Package config loads and validates glyph.toml — the v2 configuration in
// which user-defined RE2 patterns decide the message grammar and the named
// group semver_sigil is the only input to version calculation. The sigil
// alphabet is fixed in the binary (= none / ~ patch / ^ minor / ! major /
// % promote to 1.0.0); everything else — where the sigil sits, what a subject
// looks like, which commits are skipped — belongs to the file, not to glyph.
//
// The package is pure: no I/O beyond LoadFile reading the one path it is
// given, no globals. It is deliberately strict where the house config style
// is lenient: unknown schema, unknown keys and half-specified patterns are
// errors, never clamped or ignored, because this file decides CI verdicts and
// a silently-misread key is a silently-changed verdict — the exact hole the
// v2 design refuses to open (no silent none). There is no Default(): a
// missing or invalid glyph.toml is the caller's fact to act on, not this
// package's to paper over.
//
// Errors are plain errors on purpose, not *core.Error: which exit code a bad
// glyph.toml maps to is the v2 CLI's contract to define at its boundary, and
// every error this package returns is the same class (bad config), so the
// caller classifies by call site, never by string match.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// Schema is the one glyph.toml schema this binary understands. A file that
// declares any other value is rejected whole — interpreting a config written
// for a future schema with today's field meanings would judge commits by
// rules the author never wrote.
const Schema = 1

// SigilGroup is the regex group name every verdict flows through. RE2 group
// names cannot contain '-', which is why it is snake_case.
const SigilGroup = "semver_sigil"

// Sigil is one of the five version signals a commit can carry. The alphabet
// and its meaning are fixed in the binary and are not configurable.
//
// Four of them are relative — they say how far to step from wherever the
// repository already is. SigilPromote is the one absolute signal: it names
// the version v1.0.0 rather than a distance, and it exists because the 0.x
// arithmetic has no other exit. While the major is 0 a '!' folds to a minor
// step (see bump.Version.Next), so a repository still finding its shape can
// break things without claiming a stable 1.0 — which leaves reaching 1.0.0
// as something its author has to say, and '%' is how they say it.
type Sigil rune

const (
	SigilNone    Sigil = '=' // no version movement
	SigilPatch   Sigil = '~'
	SigilMinor   Sigil = '^'
	SigilMajor   Sigil = '!'
	SigilPromote Sigil = '%' // 0.x → 1.0.0; a major step at 1.x and above
)

// ParseSigil maps a captured semver_sigil group (or a pattern's fixed
// semver_sigil key) to its Sigil. Anything but the five one-rune signals is
// an error — a pattern loose enough to capture something else is a config
// bug, not a new kind of sigil.
func ParseSigil(s string) (Sigil, error) {
	switch s {
	case "=":
		return SigilNone, nil
	case "~":
		return SigilPatch, nil
	case "^":
		return SigilMinor, nil
	case "!":
		return SigilMajor, nil
	case "%":
		return SigilPromote, nil
	}
	return 0, fmt.Errorf("invalid semver_sigil %q: the alphabet is [=~^!%%]", s)
}

func (s Sigil) String() string { return string(rune(s)) }

// Config is a validated glyph.toml. Every Pattern inside it is compiled;
// construct one only through Load or LoadFile.
type Config struct {
	Schema         int
	ExcludeAuthors []string
	Commit         Commit
	Patterns       []Pattern
	Note           Note
}

// Commit carries the human-facing template block. glyph never reads it — the
// preset bytes init writes are its one home; it is decoded at all only so the
// strict decoder accepts the section — so no validation applies beyond TOML
// well-formedness.
type Commit struct {
	Style    string
	Template string
}

// Pattern is one compiled [[patterns]] entry. Order is meaning: the first
// pattern in file order whose regex matches the message wins, and nothing
// after it is consulted.
type Pattern struct {
	// Pattern is the RE2 source text, kept for error messages and display.
	Pattern string
	// Fixed is the pattern-level semver_sigil key: the sigil a match yields
	// when the message itself captures none. Nil when the key is absent.
	Fixed *Sigil
	// Skip drops a matching commit from lint, bump and notes entirely — a
	// skipped commit is not a violation (git-cliff's commit_parsers shape).
	Skip bool
	// Warn is the pattern-level warn key: a message the author of the FILE
	// wrote for the author of a COMMIT, emitted wherever this pattern wins a
	// verdict (lint, and the version fold). It exists for patterns that are
	// legal but undesirable — the v1-acceptance window, where a sigil-less
	// subject folds none: without a warning that hole is silent for exactly
	// as long as the pattern lives. Empty means no warning.
	Warn string

	re *regexp.Regexp
}

// Note carries the release-notes block: the per-commit line template and the
// ordered sections. Line is the template as written, kept for display; Spans
// is the same template compiled, and is what the notes renderer walks —
// resolving a placeholder to a commit's value stays the renderer's job, so
// this package never sees a commit.
type Note struct {
	Line        string
	Spans       []LineSpan
	DraftOnNone bool
	Sections    []Section
}

// SectionAxis says which fact of a commit a section filters on. The two axes
// share no namespace on purpose: a section states its axis explicitly or the
// file does not load.
type SectionAxis int

const (
	AxisSemver SectionAxis = iota // Value is one of major / minor / patch / none
	AxisAuthor                    // Value is a literal author to match
)

// Section is one [[note.sections]] entry. Slice order is render order, and a
// commit may land in any number of sections.
type Section struct {
	Axis  SectionAxis
	Value string
	Title string
}

// raw is the decode shape. Pointer scalars distinguish an omitted key from an
// explicit zero — schema = 0 must read as "declared and unsupported", not as
// "missing". It is never exposed: callers only ever see a fully validated
// Config.
type raw struct {
	Schema         *int         `toml:"schema"`
	ExcludeAuthors []string     `toml:"exclude_authors"`
	Commit         rawCommit    `toml:"commit"`
	Patterns       []rawPattern `toml:"patterns"`
	Note           rawNote      `toml:"note"`
}

type rawCommit struct {
	Style    string `toml:"style"`
	Template string `toml:"template"`
}

type rawPattern struct {
	Pattern     *string `toml:"pattern"`
	SemverSigil *string `toml:"semver_sigil"`
	Skip        bool    `toml:"skip"`
	Warn        *string `toml:"warn"`
}

type rawNote struct {
	Line        string       `toml:"line"`
	DraftOnNone bool         `toml:"draft_on_none"`
	Sections    []rawSection `toml:"sections"`
}

type rawSection struct {
	Semver *string `toml:"semver"`
	Author *string `toml:"author"`
	Title  string  `toml:"title"`
}

// LoadFile reads and validates the glyph.toml at path. A missing file is the
// caller's condition to handle (os.IsNotExist on the wrapped error), not a
// default-config case.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the caller's own glyph.toml; reading a named path is this function's contract
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := Load(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Load parses and validates glyph.toml content. It rejects, rather than
// repairs: unknown keys, an unknown or missing schema, an empty
// exclude_authors entry, an uncompilable or sigil-less pattern, a malformed
// note.line, a note.line placeholder nothing can bind, and a section that does
// not state exactly one axis are all load failures.
func Load(data []byte) (*Config, error) {
	var r raw
	if err := strictUnmarshal(data, &r); err != nil {
		return nil, err
	}

	if r.Schema == nil {
		return nil, fmt.Errorf("schema is required (this glyph understands schema = %d)", Schema)
	}
	if *r.Schema != Schema {
		return nil, fmt.Errorf("unsupported schema = %d (this glyph understands schema = %d); refusing to guess what its fields mean", *r.Schema, Schema)
	}

	for i, a := range r.ExcludeAuthors {
		if a == "" {
			return nil, fmt.Errorf("exclude_authors[%d] is empty: the authoring path has no commit yet and judges under an empty author, so an empty entry excludes every message the commit-msg hook is ever handed — the convention gate off, at exit 0, from one stray comma", i)
		}
	}

	if len(r.Patterns) == 0 {
		return nil, fmt.Errorf("at least one [[patterns]] entry is required: without one, no commit can ever match and every range is refused")
	}
	patterns := make([]Pattern, 0, len(r.Patterns))
	for i, rp := range r.Patterns {
		p, err := compilePattern(rp)
		if err != nil {
			return nil, fmt.Errorf("patterns[%d]: %w", i, err)
		}
		patterns = append(patterns, p)
	}

	spans, err := ParseLine(r.Note.Line)
	if err != nil {
		return nil, fmt.Errorf("note.line: %w", err)
	}
	if err := validateLineNames(spans, patterns); err != nil {
		return nil, fmt.Errorf("note.line: %w", err)
	}

	sections := make([]Section, 0, len(r.Note.Sections))
	for i, rs := range r.Note.Sections {
		s, err := buildSection(rs)
		if err != nil {
			return nil, fmt.Errorf("note.sections[%d]: %w", i, err)
		}
		sections = append(sections, s)
	}

	return &Config{
		Schema:         *r.Schema,
		ExcludeAuthors: r.ExcludeAuthors,
		Commit:         Commit(r.Commit),
		Patterns:       patterns,
		Note: Note{
			Line:        r.Note.Line,
			Spans:       spans,
			DraftOnNone: r.Note.DraftOnNone,
			Sections:    sections,
		},
	}, nil
}

func strictUnmarshal(data []byte, r *raw) error {
	d := toml.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(r); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return fmt.Errorf("unknown key (typo, or written for a newer schema?):\n%s", strict.String())
		}
		return fmt.Errorf("parse glyph.toml: %w", err)
	}
	return nil
}

func compilePattern(rp rawPattern) (Pattern, error) {
	if rp.Pattern == nil || *rp.Pattern == "" {
		return Pattern{}, fmt.Errorf("pattern is required and must not be empty (an empty regex matches every message)")
	}
	re, err := regexp.Compile(*rp.Pattern)
	if err != nil {
		return Pattern{}, fmt.Errorf("compile %q: %w (RE2 syntax — lookahead and backreferences are unsupported)", *rp.Pattern, err)
	}

	hasGroup := false
	for _, name := range re.SubexpNames() {
		if name == SigilGroup {
			hasGroup = true
		}
	}

	var fixed *Sigil
	if rp.SemverSigil != nil {
		if rp.Skip {
			return Pattern{}, fmt.Errorf("skip = true and semver_sigil = %q contradict: a skipped commit has no sigil to give", *rp.SemverSigil)
		}
		s, err := ParseSigil(*rp.SemverSigil)
		if err != nil {
			return Pattern{}, err
		}
		fixed = &s
	}

	warn := ""
	if rp.Warn != nil {
		if rp.Skip {
			return Pattern{}, fmt.Errorf("skip = true and warn = %q contradict: a skipped commit is outside every verdict, so its warning would have no reader with anything to fix", *rp.Warn)
		}
		if *rp.Warn == "" {
			return Pattern{}, fmt.Errorf("warn is empty: the warning IS the message shown for every commit this pattern claims — say what is undesirable and what to write instead, or drop the key")
		}
		warn = *rp.Warn
	}

	if !rp.Skip && !hasGroup && fixed == nil {
		return Pattern{}, fmt.Errorf("pattern %q has no (?P<%s>...) group, no semver_sigil key and no skip = true: a match could never yield a verdict", *rp.Pattern, SigilGroup)
	}

	return Pattern{
		Pattern: *rp.Pattern,
		Fixed:   fixed,
		Skip:    rp.Skip,
		Warn:    warn,
		re:      re,
	}, nil
}

func buildSection(rs rawSection) (Section, error) {
	if rs.Title == "" {
		return Section{}, fmt.Errorf("title is required")
	}
	switch {
	case rs.Semver != nil && rs.Author != nil:
		return Section{}, fmt.Errorf("semver and author are separate axes: state exactly one, not both")
	case rs.Semver != nil:
		switch *rs.Semver {
		case "major", "minor", "patch", "none":
		default:
			return Section{}, fmt.Errorf("invalid semver filter %q: want major, minor, patch or none", *rs.Semver)
		}
		return Section{Axis: AxisSemver, Value: *rs.Semver, Title: rs.Title}, nil
	case rs.Author != nil:
		return Section{Axis: AxisAuthor, Value: *rs.Author, Title: rs.Title}, nil
	}
	return Section{}, fmt.Errorf("a section must state its axis: set semver or author")
}
