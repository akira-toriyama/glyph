package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadGemoji loads the embedded `glyph init --gemoji` preset — the exact
// artifact init writes — so every test here runs against what users will
// actually hold.
func loadGemoji(t *testing.T) *Config {
	t.Helper()
	data, ok := Preset("gemoji")
	if !ok {
		t.Fatalf("Preset(gemoji) missing")
	}
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load(gemoji preset): %v", err)
	}
	return cfg
}

// TestEveryPresetLoads pins the ship gate: a preset this binary offers to
// write must load under this binary's own loader, whole and strict.
func TestEveryPresetLoads(t *testing.T) {
	names := PresetNames()
	want := []string{"conventional", "gemoji"}
	if len(names) != len(want) {
		t.Fatalf("PresetNames() = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("PresetNames() = %v, want %v", names, want)
		}
	}
	for _, name := range names {
		data, ok := Preset(name)
		if !ok {
			t.Fatalf("Preset(%s) missing", name)
		}
		if _, err := Load(data); err != nil {
			t.Errorf("preset %s does not load: %v", name, err)
		}
	}
}

// TestConventionalPresetGrammar pins the conventional preset's shape: the
// sigil sits before the colon, so conventional's own `feat!:` reads as the
// major sigil unchanged, and a sigil-less `feat:` is a violation (the
// write-it-down half of the sigil's job).
func TestConventionalPresetGrammar(t *testing.T) {
	data, _ := Preset("conventional")
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := []struct {
		message string
		sigil   Sigil
	}{
		{"feat^: add a thing", SigilMinor},
		{"fix~: repair a thing", SigilPatch},
		{"chore=: touch nothing", SigilNone},
		{"feat(api)!: drop a flag", SigilMajor},
	}
	for _, c := range cases {
		m, err := cfg.Match(c.message)
		if err != nil || !m.Matched || m.Skip {
			t.Fatalf("Match(%q) = %+v, %v — want a sigil match", c.message, m, err)
		}
		if m.Sigil != c.sigil {
			t.Errorf("Match(%q).Sigil = %v, want %v", c.message, m.Sigil, c.sigil)
		}
	}
	if v := cfg.Lint("feat: no sigil written", "akira"); v.OK || v.Excluded {
		t.Errorf("a sigil-less conventional subject must be a violation, got %+v", v)
	}
}

func TestLoadGemojiConfig(t *testing.T) {
	cfg := loadGemoji(t)

	if cfg.Schema != 1 {
		t.Errorf("Schema = %d, want 1", cfg.Schema)
	}
	wantAuthors := []string{"dependabot[bot]", "renovate[bot]"}
	if len(cfg.ExcludeAuthors) != len(wantAuthors) {
		t.Fatalf("ExcludeAuthors = %q, want %q", cfg.ExcludeAuthors, wantAuthors)
	}
	for i, w := range wantAuthors {
		if cfg.ExcludeAuthors[i] != w {
			t.Errorf("ExcludeAuthors[%d] = %q, want %q", i, cfg.ExcludeAuthors[i], w)
		}
	}
	if cfg.Commit.Style != "gemoji" {
		t.Errorf("Commit.Style = %q, want %q", cfg.Commit.Style, "gemoji")
	}
	if !strings.Contains(cfg.Commit.Template, "<semver_sigil>") {
		t.Errorf("Commit.Template lost the literal placeholder: %q", cfg.Commit.Template)
	}

	if len(cfg.Patterns) != 3 {
		t.Fatalf("len(Patterns) = %d, want 3", len(cfg.Patterns))
	}
	if cfg.Patterns[0].Fixed != nil || cfg.Patterns[0].Skip {
		t.Errorf("Patterns[0] should rely on its capture group alone: %+v", cfg.Patterns[0])
	}
	if cfg.Patterns[1].Fixed == nil || *cfg.Patterns[1].Fixed != SigilPatch {
		t.Errorf("Patterns[1].Fixed = %v, want ~ (patch)", cfg.Patterns[1].Fixed)
	}
	if !cfg.Patterns[2].Skip {
		t.Errorf("Patterns[2].Skip = false, want true (merge commits leave processing)")
	}

	if cfg.Note.Line != "- $subject ($pr) @$author" {
		t.Errorf("Note.Line = %q", cfg.Note.Line)
	}
	if cfg.Note.DraftOnNone {
		t.Errorf("Note.DraftOnNone = true, want false (v1-compatible default)")
	}
	wantSections := []Section{
		{Axis: AxisSemver, Value: "major", Title: "Breaking Changes"},
		{Axis: AxisSemver, Value: "minor", Title: "Features"},
		{Axis: AxisSemver, Value: "patch", Title: "Fixes"},
		{Axis: AxisAuthor, Value: "dependabot[bot]", Title: "Dependencies"},
	}
	if len(cfg.Note.Sections) != len(wantSections) {
		t.Fatalf("len(Sections) = %d, want %d", len(cfg.Note.Sections), len(wantSections))
	}
	for i, w := range wantSections {
		if cfg.Note.Sections[i] != w {
			t.Errorf("Sections[%d] = %+v, want %+v", i, cfg.Note.Sections[i], w)
		}
	}
}

// minimal returns a syntactically complete config with the given fragment
// spliced in, so each error case states only what it is about.
const minimalPatterns = "[[patterns]]\npattern = '(?P<semver_sigil>[=~^!]) (?P<subject>.+)'\n"

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string // substring of the error
	}{
		{"malformed toml", "schema = ", "parse glyph.toml"},
		{"missing schema", minimalPatterns, "schema is required"},
		{"unsupported schema", "schema = 2\n" + minimalPatterns, "unsupported schema = 2"},
		{"schema zero is declared not missing", "schema = 0\n" + minimalPatterns, "unsupported schema = 0"},
		{"unknown top-level key", "schema = 1\nexclude_author = ['x']\n" + minimalPatterns, "unknown key"},
		{"unknown pattern key", "schema = 1\n[[patterns]]\npattern = 'x'\nsigil = '='\n", "unknown key"},
		{"no patterns", "schema = 1\n", "at least one [[patterns]]"},
		{"empty pattern", "schema = 1\n[[patterns]]\npattern = ''\n", "must not be empty"},
		{"invalid regex", `schema = 1` + "\n[[patterns]]\npattern = '(?=lookahead)'\n", "RE2"},
		{"pattern with no sigil source", "schema = 1\n[[patterns]]\npattern = '^x'\n", "could never yield a verdict"},
		{"skip contradicts fixed sigil", "schema = 1\n[[patterns]]\npattern = '^x'\nskip = true\nsemver_sigil = '~'\n", "contradict"},
		{"fixed sigil invalid", "schema = 1\n[[patterns]]\npattern = '^x'\nsemver_sigil = '+'\n", "invalid semver_sigil"},
		{"section with both axes", "schema = 1\n" + minimalPatterns + "[[note.sections]]\nsemver = 'major'\nauthor = 'x'\ntitle = 'T'\n", "exactly one"},
		{"section with no axis", "schema = 1\n" + minimalPatterns + "[[note.sections]]\ntitle = 'T'\n", "state its axis"},
		{"section bad semver value", "schema = 1\n" + minimalPatterns + "[[note.sections]]\nsemver = 'huge'\ntitle = 'T'\n", "invalid semver filter"},
		{"section missing title", "schema = 1\n" + minimalPatterns + "[[note.sections]]\nsemver = 'major'\n", "title is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load([]byte(c.toml))
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want substring %q", err, c.want)
			}
		})
	}
}

func TestLoadFileMissing(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "glyph.toml"))
	if err == nil {
		t.Fatalf("LoadFile on a missing path succeeded")
	}
	if !os.IsNotExist(unwrapAll(err)) {
		t.Errorf("error should preserve os.IsNotExist: %v", err)
	}
}

func unwrapAll(err error) error {
	for {
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

func TestParseSigil(t *testing.T) {
	cases := []struct {
		in   string
		want Sigil
	}{
		{"=", SigilNone},
		{"~", SigilPatch},
		{"^", SigilMinor},
		{"!", SigilMajor},
	}
	for _, c := range cases {
		got, err := ParseSigil(c.in)
		if err != nil {
			t.Fatalf("ParseSigil(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseSigil(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "+", "==", "≈", "major"} {
		if _, err := ParseSigil(bad); err == nil {
			t.Errorf("ParseSigil(%q) succeeded, want error", bad)
		}
	}
}
