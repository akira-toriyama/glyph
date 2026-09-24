package config

import "testing"

// warnedToml is a two-pattern config in the shape the warn key was made for:
// the strict sigil grammar first, then a warned catch-all that accepts a
// sigil-less gitmoji subject as none. The fleet's retired v1-acceptance
// window had exactly this shape, and no shipped preset carries it any more;
// the literal stays because these tests assert the warn MECHANISM, which
// outlives the pattern it was made for.
const warnedToml = `schema = 1

[[patterns]]
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) (?P<subject>.+)'

[[patterns]]
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))? (?P<subject>.+)'
semver_sigil = '='
warn = 'no sigil: folds as none'
`

// TestLintSurfacesTheWinningPatternsWarn pins the warn mechanism's lint half:
// a warned pattern's match is a PASS that carries the file author's message,
// and only that pattern's wins carry it. This is what keeps a warned
// pattern's hole — a subject folding as silent none — visible for exactly as
// long as the pattern lives.
func TestLintSurfacesTheWinningPatternsWarn(t *testing.T) {
	cfg, err := Load([]byte(warnedToml))
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
	cfg, err := Load([]byte(warnedToml))
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
		t.Errorf("warned pattern's sigil = %v, want none", m.Sigil)
	}
}
