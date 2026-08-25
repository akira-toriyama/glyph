package config

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// windowToml is a two-pattern config in the migration-window shape: the
// strict sigil grammar first, then a warned catch-all that accepts a
// sigil-less gitmoji subject as none. The literal is deliberately not the
// shipped preset — these tests assert the warn MECHANISM; the preset's own
// content is pinned by the preset tests below.
const windowToml = `schema = 1

[[patterns]]
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) (?P<subject>.+)'

[[patterns]]
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))? (?P<subject>.+)'
semver_sigil = '='
warn = 'no sigil: folds as none'
`

// TestLintSurfacesTheWinningPatternsWarn pins the warn mechanism's lint half:
// a warned pattern's match is a PASS that carries the file author's message,
// and only that pattern's wins carry it. This is what keeps the v1-acceptance
// window's hole — a sigil-less subject folding as silent none — visible for
// exactly as long as the window pattern lives.
func TestLintSurfacesTheWinningPatternsWarn(t *testing.T) {
	cfg, err := Load([]byte(windowToml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	warned := cfg.Lint(":sparkles: add a feature with no sigil", "akira")
	if !warned.OK || warned.Warn != "no sigil: folds as none" {
		t.Fatalf("a warned pattern's match = %+v, want OK with the pattern's warn message", warned)
	}
	clean := cfg.Lint(":sparkles:^ add a feature", "akira")
	if !clean.OK || clean.Warn != "" {
		t.Fatalf("a strict match = %+v, want OK with no warning", clean)
	}
}

// TestMatchCarriesTheWinningPatternsWarn is the same decision one layer down,
// where the fold reads it.
func TestMatchCarriesTheWinningPatternsWarn(t *testing.T) {
	cfg, err := Load([]byte(windowToml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m, err := cfg.Match(":bug: fix without a sigil")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !m.Matched || m.Warn != "no sigil: folds as none" {
		t.Fatalf("Match = %+v, want a match carrying the warn message", m)
	}
	if m.Sigil != SigilNone {
		t.Errorf("window sigil = %v, want none", m.Sigil)
	}
}

// TestPresetWithV1WindowComposes pins the composed init artifact: it loads,
// the window is the LAST pattern, and it is the warned one — so a fleet
// repository generated with --v1-window gets the warning without hand-editing
// the eight lines every migrating repo used to retype.
func TestPresetWithV1WindowComposes(t *testing.T) {
	data, err := PresetWithV1Window("gemoji")
	if err != nil {
		t.Fatalf("PresetWithV1Window: %v", err)
	}
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("the composed preset does not load: %v", err)
	}
	last := cfg.Patterns[len(cfg.Patterns)-1]
	if last.Warn == "" || last.Fixed == nil || *last.Fixed != SigilNone {
		t.Fatalf("last pattern = %+v, want the warned none-folding window", last)
	}
	v := cfg.Lint(":sparkles: a v1 subject with no sigil", "akira")
	if !v.OK || v.Warn == "" {
		t.Fatalf("v1 subject under the composed preset = %+v, want OK with a warning", v)
	}
	if !bytes.Contains(data, []byte("`glyph init --gemoji --v1-window`")) {
		t.Errorf("the header must name the command that regenerates the file")
	}
}

// TestPresetWithV1WindowRefusesOtherGrammars: the window accepts the fleet's
// v1 history, which is gitmoji — composed onto any other grammar it would
// claim nothing and its warning would describe a migration that never
// happened.
func TestPresetWithV1WindowRefusesOtherGrammars(t *testing.T) {
	for _, name := range []string{"conventional", "no-such-preset"} {
		if _, err := PresetWithV1Window(name); err == nil {
			t.Errorf("PresetWithV1Window(%q) succeeded, want refusal", name)
		}
	}
}

// TestGlyphOwnConfigIsTheComposedV1WindowPreset holds glyph's own committed
// glyph.toml byte-identical to `init --gemoji --v1-window` output. Before
// this, the window block existed only as a hand edit in this one file, and
// the runbook asked 33 migrating repositories to reproduce it by hand —
// nothing could notice a retype drifting, and the block's own removal note
// cited a task that had already closed. One generated artifact, regenerated
// with `go run ./cmd/glyph init --gemoji --v1-window --force`, is the fix:
// edit the preset or the snippet, never this file.
func TestGlyphOwnConfigIsTheComposedV1WindowPreset(t *testing.T) {
	own, err := os.ReadFile("../../glyph.toml")
	if err != nil {
		t.Fatalf("read glyph.toml: %v", err)
	}
	want, err := PresetWithV1Window("gemoji")
	if err != nil {
		t.Fatalf("PresetWithV1Window: %v", err)
	}
	if !bytes.Equal(own, want) {
		t.Fatalf("glyph.toml is not the generated artifact — regenerate it with `go run ./cmd/glyph init --gemoji --v1-window --force` (never hand-edit; diff begins at %q)", firstDiffLine(string(own), string(want)))
	}
}

// firstDiffLine names the first line where two texts diverge, for a failure
// message that points instead of dumping both files.
func firstDiffLine(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return al[i]
		}
	}
	if len(al) < len(bl) {
		return bl[len(al)]
	}
	if len(bl) < len(al) {
		return al[len(bl)]
	}
	return ""
}
