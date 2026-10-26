// Package config loads and validates glyph.toml — the configuration in
// which user-defined RE2 patterns decide the message grammar and the named
// group semver_sigil is the only input to version calculation. The sigil
// alphabet is fixed in the binary (= none / ~ patch / ^ minor / ! major /
// % promote to 1.0.0); everything else — where the sigil sits, what a subject
// looks like, which commits are skipped — belongs to the file, not to glyph.
//
// The package is pure: no I/O beyond StatFile and LoadFile looking at the one
// path they are given, no globals. It is deliberately strict where the house
// config style is lenient: unknown schema, unknown keys and half-specified
// patterns are errors, never clamped or ignored, because this file decides CI
// verdicts and a silently-misread key is a silently-changed verdict — the
// exact hole the design refuses to open (no silent none). There is no
// Default(): a missing or invalid glyph.toml is the caller's fact to act on,
// not this package's to paper over.
//
// Errors are never *core.Error: which exit code a bad glyph.toml maps to is
// the CLI's contract to define at its boundary. Every error Load returns is
// the same class (bad content), a plain error; StatFile, and LoadFile through
// it, add the filesystem's answers as types — *fs.PathError and
// *NotAFileError — so the caller classifies by type and call site, never by
// string match.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
)

// schema is the one glyph.toml schema this binary understands. A file that
// declares any other value is rejected whole — interpreting a config written
// for a future schema with today's field meanings would judge commits by
// rules the author never wrote.
const schema = 1

// sigilGroup is the regex group name every verdict flows through. RE2 group
// names cannot contain '-', which is why it is snake_case.
const sigilGroup = "semver_sigil"

// ScopeGroup is the regex group name attribution reads (DESIGN §4.1, rule
// 2): a commit whose files lie under no package is placed on the package
// its scope names. Nothing requires a pattern to capture it — a file without
// the group simply has no scope to consult, and the shipped presets capture
// it under this name. Once a pattern whose groups a commit binds does
// capture it, every package name must be a word one such capture can be
// (checkScopeWord), or no commit could name that line.
const ScopeGroup = "scope"

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

// parseSigil maps a captured semver_sigil group (or a pattern's fixed
// semver_sigil key) to its Sigil. Anything but the five one-rune signals is
// an error — a pattern loose enough to capture something else is a config
// bug, not a new kind of sigil.
func parseSigil(s string) (Sigil, error) {
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
	// Packages is the [[packages]] array in file order, empty when the file
	// declares none. Empty means ONE version line with no name — the shape
	// every repository had before packages existed — and nothing here
	// synthesises a root package to stand in for it: a consumer that branches
	// on len(Packages) > 0 is asking "did the author declare lines?", and a
	// synthesised entry would answer yes for every repository in the fleet
	// (DESIGN §4.1; mutation row packages-absent-changes-the-single-line).
	Packages []Package
}

// Commit carries the human-facing template block. glyph never parses it —
// the preset bytes init writes are its one home, and no validation applies
// beyond TOML well-formedness. Its one reader is Config.subjectForm, which
// quotes the template's first line back inside the no-pattern-matches lint
// violation, verbatim.
//
// There is deliberately no style key: the label the presets once wrote here
// named a grammar the patterns alone decide, and nothing read it (DESIGN §2).
// The strict decoder refuses it as unknown, which is the whole enforcement.
type Commit struct {
	Template string
}

// Package is one validated [[packages]] entry: a declared subtree of the
// repository with its own version line (DESIGN §4.1). Path is the subtree as
// written, already checked to be a clean relative path — "." for the root
// package, else a slash-separated path with no leading "./", no trailing "/"
// and no ".." — whose every segment git accepts in a refname and whose first
// character git tag accepts in a tag name, so the tag line is derivable from
// it without another normalisation step and every tag it names is one git tag
// can create (tagline.go: <prefix>vX.Y.Z, the prefix <path>/ with a major
// version subdirectory /vN folded into the major, empty for the root). Name is what a commit scope may
// call the package: the file's `name` key when set, else the last path
// segment (path.Base, "." for the root package; a major version subdirectory
// defaults to `<parent>/vN`, see defaultName). Wherever a pattern whose groups
// a commit binds captures a scope, the name — set or defaulted, the root's
// included — must be a word one such scope can be, which under the presets
// means the root and a /vN subdirectory set `name`; a file that captures no
// scope names nothing and is exempt. Names are unique across the array; the
// loader refuses two packages sharing one and names both.
//
// There is deliberately no tag_prefix key: the tag line is derived from Path
// (tagPrefix) and is not configurable (§4.1 rejects the knob; the strict
// decoder refuses the key as unknown, which is the whole enforcement).
type Package struct {
	Path string
	Name string
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
	// legal but undesirable: a match that folds none with nothing said is a
	// hole that stays silent for exactly as long as the pattern lives (the
	// fleet's retired v1-acceptance window was the first such pattern). Empty
	// means no warning.
	Warn string
	// Unlandable is the pattern-level unlandable key: why a message this
	// pattern claims may be WRITTEN but must never LAND. Match reports such a
	// message as claimed by no pattern, with this reason beside it, so every
	// gate that judges a commit which already exists refuses it exactly as it
	// refuses an unmatched one; only LintAuthoring — the commit-msg hook's
	// question — lets it through, with the reason as a warning. Made for
	// git's `amend!` subject (t-t84a): autosquash REPLACES the target's
	// message with the amend! body, so a skip drops the only commit carrying
	// the new sigil, and a refusal at the hook forces --no-verify. Empty
	// means the key is absent.
	Unlandable string

	re *regexp.Regexp
}

// bindsGroups says whether a commit this pattern claims binds its named
// groups. Match hands the winning pattern's groups on unless it is a skip
// (claimed and dropped: placed nowhere, rendered in no section) or
// unlandable (reported unclaimed with no groups, rendered through the
// raw-line fallback). A group only such a pattern captures is never read, so
// every reader of the file's group names asks this predicate, not the flags:
// boundGroupNames (note.line's placeholders and the trailers' names) and
// scopeSpellers (the package names).
func (p *Pattern) bindsGroups() bool { return !p.Skip && p.Unlandable == "" }

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
	Trailers    []NoteTrailer
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
	Packages       []rawPackage `toml:"packages"`
}

type rawPackage struct {
	Path *string `toml:"path"`
	Name *string `toml:"name"`
}

type rawCommit struct {
	Template string `toml:"template"`
}

type rawPattern struct {
	Pattern     *string `toml:"pattern"`
	SemverSigil *string `toml:"semver_sigil"`
	Skip        bool    `toml:"skip"`
	Warn        *string `toml:"warn"`
	Unlandable  *string `toml:"unlandable"`
}

type rawNote struct {
	Line        string       `toml:"line"`
	DraftOnNone bool         `toml:"draft_on_none"`
	Sections    []rawSection `toml:"sections"`
	Trailers    []rawTrailer `toml:"trailers"`
}

type rawSection struct {
	Semver *string `toml:"semver"`
	Author *string `toml:"author"`
	Title  string  `toml:"title"`
}

// NotAFileError is StatFile's answer for a path that holds something other
// than a regular file — a directory, a named pipe, a socket, a device, or a
// symbolic link that resolves to no file. What is there was OBSERVED, by
// os.Stat or, for a link it cannot follow, os.Lstat, and the path was never
// opened: open(2) on a named pipe blocks until a writer appears, so a FIFO
// named glyph.toml hung every command that loads the config, and a directory
// surfaced as a read error whose remedy was the file's permissions (t-zvax).
// The loader's callers answer it as they answer a missing file, not as a read
// that failed; glyph init, which writes over a missing path, refuses it
// (DESIGN §5, §7).
type NotAFileError struct {
	Path string
	// Mode is the path's type bits (fs.FileMode.Type) as os.Stat reported
	// them, symlinks followed, or fs.ModeSymlink, os.Lstat's, for a link that
	// resolves to no file; never 0, which is a regular file.
	Mode fs.FileMode
}

func (e *NotAFileError) Error() string {
	return e.Path + " is " + e.Kind() + ", not a regular file"
}

// Kind names what is at the path, article included, for the sentence a
// caller words around it. A link is only ever os.Lstat's answer, for one
// os.Stat could not follow: os.Stat reports what a link resolves to. The last
// arm is unreachable on linux and darwin, where os.Stat sets one of the bits
// above for every type but a regular file (Go's os/stat_linux.go and
// os/stat_darwin.go: a whiteout reads as a device).
func (e *NotAFileError) Kind() string {
	switch {
	case e.Mode.IsDir():
		return "a directory"
	case e.Mode&fs.ModeSymlink != 0:
		return "a broken symbolic link"
	case e.Mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case e.Mode&fs.ModeSocket != 0:
		return "a socket"
	case e.Mode&fs.ModeDevice != 0:
		return "a device"
	}
	return "a file of unknown type"
}

// StatFile is the one classification of what is at a glyph.toml path, made
// without opening it — never open-then-fstat, because the open is what blocks
// on a named pipe. LoadFile asks it before it reads and `glyph init` before it
// writes, so the loader's commands and init classify one path alike. Its
// answers are told apart by type, and by fs.ErrNotExist within *fs.PathError;
// every *fs.PathError is os.Stat's own, unwrapped (it already names the
// operation and the path):
//
//   - nil: a regular file, symlinks followed;
//   - a *fs.PathError carrying fs.ErrNotExist: nothing is there, not even a
//     link — the caller's condition to handle, not a default-config case;
//   - a *NotAFileError: something other than a regular file is there;
//   - any other *fs.PathError: a stat the filesystem refused (permission
//     denied, an I/O fault), so nothing was observed.
func StatFile(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return &NotAFileError{Path: path, Mode: info.Mode().Type()}
		}
		return nil
	}
	// A link that dangles, loops, runs through a regular file or names a
	// target too long to exist fails os.Stat by its own fault, yet os.Lstat
	// sees the link itself — observed, so not a regular file (DESIGN §5). A
	// link os.Stat could not follow for a permission or an I/O fault keeps
	// that fault: where it leads was never observed.
	if link, lerr := os.Lstat(path); lerr == nil && link.Mode()&fs.ModeSymlink != 0 && linksOwnFault(err) {
		return &NotAFileError{Path: path, Mode: fs.ModeSymlink}
	}
	return err
}

// linksOwnFault reports whether os.Stat's failure on a path that is a link is
// the link's own — a target that does not exist, a loop, a target path
// through a regular file, a target name too long to exist. os.Lstat resolved
// the path itself, so ENAMETOOLONG can only come from the target: a component
// past NAME_MAX, or a path past PATH_MAX once the link is expanded.
func linksOwnFault(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) ||
		errors.Is(err, syscall.ENAMETOOLONG)
}

// LoadFile reads and validates the glyph.toml at path. Its failures are
// StatFile's; os.ReadFile's own *fs.PathError, unwrapped, for a read the
// filesystem refused; and any other error is the content's, from Load,
// prefixed with path — told apart as StatFile's answers are, by type and by
// fs.ErrNotExist within *fs.PathError.
func LoadFile(path string) (*Config, error) {
	if err := StatFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the caller's own glyph.toml; reading a named path is this function's contract
	if err != nil {
		return nil, err
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
// note.line, a note.line placeholder nothing can bind, a package path git
// cannot prefix a tag with, a package name no read scope group can spell,
// and a section that does not state exactly one axis are all load failures.
func Load(data []byte) (*Config, error) {
	var r raw
	if err := strictUnmarshal(data, &r); err != nil {
		return nil, err
	}

	if r.Schema == nil {
		return nil, fmt.Errorf("schema is required (this glyph understands schema = %d)", schema)
	}
	if *r.Schema != schema {
		return nil, fmt.Errorf("unsupported schema = %d (this glyph understands schema = %d); refusing to guess what its fields mean", *r.Schema, schema)
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

	trailers, err := buildTrailers(r.Note.Trailers, patterns)
	if err != nil {
		return nil, err
	}

	spans, err := parseLine(r.Note.Line)
	if err != nil {
		return nil, fmt.Errorf("note.line: %w", err)
	}
	if err := validateLineNames(spans, patterns, trailers); err != nil {
		return nil, fmt.Errorf("note.line: %w", err)
	}

	packages, err := buildPackages(r.Packages, patterns)
	if err != nil {
		return nil, err
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
			Trailers:    trailers,
		},
		Packages: packages,
	}, nil
}

// buildPackages validates the [[packages]] array against the compiled
// patterns. It rejects rather than repairs, like everything else here: a
// path that is not already clean is refused with the clean form named
// instead of being normalised, because the path is the tag prefix and a tag
// line must be readable from the file as written; and a name no scope group
// a commit binds can spell is refused with the groups it asked, because the
// name exists only to be written in a scope. A nil result for an absent or
// empty array is the one-line shape Config.Packages documents.
func buildPackages(raws []rawPackage, patterns []Pattern) ([]Package, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	spellers, err := scopeSpellers(patterns)
	if err != nil {
		return nil, err
	}
	packages := make([]Package, 0, len(raws))
	byPath := make(map[string]int, len(raws))
	byName := make(map[string]int, len(raws))
	for i, rp := range raws {
		p, err := buildPackage(rp)
		if err != nil {
			return nil, fmt.Errorf("packages[%d]: %w", i, err)
		}
		if err := checkScopeWord(p, rp.Name != nil, spellers); err != nil {
			return nil, fmt.Errorf("packages[%d] (%q): %w", i, p.Path, err)
		}
		if j, dup := byPath[p.Path]; dup {
			return nil, fmt.Errorf("packages[%d] and packages[%d] declare the same path %q: one subtree is one version line", j, i, p.Path)
		}
		if j, dup := byName[p.Name]; dup {
			return nil, fmt.Errorf("packages[%d] (%q) and packages[%d] (%q) share the name %q: a scope naming it could mean either line — set name on one of them", j, packages[j].Path, i, p.Path, p.Name)
		}
		byPath[p.Path] = i
		byName[p.Name] = i
		packages = append(packages, p)
	}
	return packages, nil
}

func buildPackage(rp rawPackage) (Package, error) {
	if rp.Path == nil || *rp.Path == "" {
		return Package{}, fmt.Errorf("path is required and must not be empty (\".\" declares the root package)")
	}
	p := *rp.Path
	switch {
	case strings.HasPrefix(p, "/"):
		return Package{}, fmt.Errorf("path %q is absolute: a package is a subtree of the repository, written relative to glyph.toml", p)
	case p == ".." || strings.HasPrefix(p, "../"):
		return Package{}, fmt.Errorf("path %q escapes the repository: a package is a subtree of the checkout glyph.toml sits in", p)
	case path.Clean(p) != p:
		return Package{}, fmt.Errorf("path %q is not in clean form: write %q (the line's tags, <prefix>vX.Y.Z, take their prefix from the path as written)", p, path.Clean(p))
	}
	if p != "." {
		for seg := range strings.SplitSeq(p, "/") {
			if why := refnameSegment(seg); why != "" {
				return Package{}, fmt.Errorf("path %q cannot prefix a tag: its segment %q %s, which git refuses in a refname (git check-ref-format), so every tag this line steps to would be one git cannot create", p, seg, why)
			}
		}
		if why := refnameLead(p); why != "" {
			return Package{}, fmt.Errorf("path %q cannot prefix a tag: it %s, so every tag this line steps to would be one git tag cannot create", p, why)
		}
	}
	name := defaultName(p)
	if rp.Name != nil {
		if *rp.Name == "" {
			return Package{}, fmt.Errorf("name is empty: drop the key to take the default (%q) or write the word a scope will use", name)
		}
		name = *rp.Name
	}
	return Package{Path: p, Name: name}, nil
}

// The unknown-key hint names no direction: a key a newer glyph introduced
// and one an older glyph wrote but this one dropped (commit.style) reach this
// refusal alike, so a hint that guesses "newer" is false for the second. No
// key gets a message of its own (DESIGN §2).
func strictUnmarshal(data []byte, r *raw) error {
	d := toml.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(r); err != nil {
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			keys := make([]string, 0, len(strict.Errors))
			for i := range strict.Errors {
				keys = append(keys, strings.Join(strict.Errors[i].Key(), "."))
			}
			noun := "key"
			if len(keys) > 1 {
				noun = "keys"
			}
			return fmt.Errorf("unknown %s %s (a typo, or written for another glyph version):\n%s", noun, strings.Join(keys, ", "), strict.String())
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
		if name == sigilGroup {
			hasGroup = true
		}
	}

	var fixed *Sigil
	if rp.SemverSigil != nil {
		if rp.Skip {
			return Pattern{}, fmt.Errorf("skip = true and semver_sigil = %q contradict: a skipped commit has no sigil to give", *rp.SemverSigil)
		}
		s, err := parseSigil(*rp.SemverSigil)
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

	unlandable := ""
	if rp.Unlandable != nil {
		switch {
		case rp.Skip:
			return Pattern{}, fmt.Errorf("skip = true and unlandable contradict: a skipped commit lands unjudged, an unlandable one is refused wherever it exists")
		case fixed != nil:
			return Pattern{}, fmt.Errorf("unlandable and semver_sigil = %q contradict: an unlandable message is never folded, so it has no sigil to give", *rp.SemverSigil)
		case rp.Warn != nil:
			return Pattern{}, fmt.Errorf("unlandable and warn contradict: warn keeps a match legal at every gate, unlandable refuses it at every gate but the commit-msg hook — the unlandable reason is already the hook's warning")
		case *rp.Unlandable == "":
			return Pattern{}, fmt.Errorf("unlandable is empty: the reason IS the refusal every gate prints and the warning the commit-msg hook prints — say what must happen to the message before it lands, or drop the key")
		}
		unlandable = *rp.Unlandable
	}

	if !rp.Skip && unlandable == "" && !hasGroup && fixed == nil {
		return Pattern{}, fmt.Errorf("pattern %q has no (?P<%s>...) group, no semver_sigil key, no skip = true and no unlandable: a match could never yield a verdict", *rp.Pattern, sigilGroup)
	}

	return Pattern{
		Pattern:    *rp.Pattern,
		Fixed:      fixed,
		Skip:       rp.Skip,
		Warn:       warn,
		Unlandable: unlandable,
		re:         re,
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
