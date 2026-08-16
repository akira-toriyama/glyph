package config

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T, toml string) *Config {
	t.Helper()
	cfg, err := Load([]byte(toml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// TestMatchFirstPatternWins asserts the ratified ordering decision: the first
// pattern in file order that matches decides the commit, deliberately unlike
// semantic-release's highest-level-wins. Both patterns here match the
// message; only order separates a none verdict from a major one.
func TestMatchFirstPatternWins(t *testing.T) {
	cfg := mustLoad(t, `schema = 1
[[patterns]]
pattern = '^x'
semver_sigil = '='

[[patterns]]
pattern = '^x'
semver_sigil = '!'
`)
	m, err := cfg.Match("x: something")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !m.Matched || m.PatternIndex != 0 {
		t.Fatalf("PatternIndex = %d (matched=%v), want 0: first match must win", m.PatternIndex, m.Matched)
	}
	if m.Sigil != SigilNone {
		t.Errorf("Sigil = %v, want = (the first pattern's), not the later pattern's !", m.Sigil)
	}
}

func TestMatchGemojiGrammar(t *testing.T) {
	cfg := loadGemoji(t)
	cases := []struct {
		name    string
		message string
		sigil   Sigil
		scope   string
		subject string
	}{
		{"minor with scope", ":sparkles:(doctor)^ add the profile checks", SigilMinor, "doctor", "add the profile checks"},
		{"patch without scope", ":bug:~ stop the crash", SigilPatch, "", "stop the crash"},
		{"major", ":boom:(cli)! drop the profile flag", SigilMajor, "cli", "drop the profile flag"},
		{"none", ":memo:= reword the readme", SigilNone, "", "reword the readme"},
		{"body does not leak into subject", ":bug:~ one line\n\nbody text here", SigilPatch, "", "one line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := cfg.Match(c.message)
			if err != nil {
				t.Fatalf("Match(%q): %v", c.message, err)
			}
			if !m.Matched || m.Skip {
				t.Fatalf("Match(%q) = %+v, want a sigil match", c.message, m)
			}
			if m.Sigil != c.sigil {
				t.Errorf("Sigil = %v, want %v", m.Sigil, c.sigil)
			}
			if m.Groups["scope"] != c.scope {
				t.Errorf("scope = %q, want %q", m.Groups["scope"], c.scope)
			}
			if m.Groups["subject"] != c.subject {
				t.Errorf("subject = %q, want %q", m.Groups["subject"], c.subject)
			}
		})
	}
}

// TestMatchFixedSigil asserts the pattern-level semver_sigil key: a raw git
// revert carries no sigil in its message, and the fixed value supplies one.
func TestMatchFixedSigil(t *testing.T) {
	cfg := loadGemoji(t)
	m, err := cfg.Match(`Revert ":sparkles:(cli)^ add the thing"`)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !m.Matched || m.Skip {
		t.Fatalf("Match = %+v, want a sigil match", m)
	}
	if m.Sigil != SigilPatch {
		t.Errorf("Sigil = %v, want ~ (the pattern's fixed value)", m.Sigil)
	}
	if got := m.Groups["subject"]; got != `:sparkles:(cli)^ add the thing` {
		t.Errorf("subject = %q", got)
	}
}

// TestMatchSkip asserts skip = true: the commit is claimed (not a violation)
// and simultaneously carries no verdict.
func TestMatchSkip(t *testing.T) {
	cfg := loadGemoji(t)
	m, err := cfg.Match("Merge pull request #180 from akira-toriyama/topic")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !m.Matched || !m.Skip {
		t.Fatalf("Match = %+v, want Matched && Skip", m)
	}
}

// TestMatchNothingMatches asserts the no-match outcome stays first-class
// data: Matched=false and no error, so lint and bump each attach their own
// meaning (violation, range refusal) instead of this package guessing.
func TestMatchNothingMatches(t *testing.T) {
	cfg := loadGemoji(t)
	for _, msg := range []string{
		"fix: conventional style does not match the gemoji patterns",
		"WIP",
		"",
	} {
		m, err := cfg.Match(msg)
		if err != nil {
			t.Fatalf("Match(%q): %v", msg, err)
		}
		if m.Matched || m.PatternIndex != -1 {
			t.Errorf("Match(%q) = %+v, want Matched=false, PatternIndex=-1", msg, m)
		}
	}
}

func TestMatchSigilErrors(t *testing.T) {
	t.Run("capture outside the alphabet", func(t *testing.T) {
		cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>.) '\n")
		_, err := cfg.Match("z broken capture")
		if err == nil || !strings.Contains(err.Error(), "invalid semver_sigil") {
			t.Fatalf("err = %v, want invalid semver_sigil", err)
		}
	})
	t.Run("empty capture without fixed fallback", func(t *testing.T) {
		cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!]?)x'\n")
		_, err := cfg.Match("x no sigil given")
		if err == nil || !strings.Contains(err.Error(), "captured nothing") {
			t.Fatalf("err = %v, want captured-nothing error", err)
		}
	})
	t.Run("empty capture falls back to fixed", func(t *testing.T) {
		cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!]?)x'\nsemver_sigil = '^'\n")
		m, err := cfg.Match("x no sigil given")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		if m.Sigil != SigilMinor {
			t.Errorf("Sigil = %v, want ^ (fixed fallback)", m.Sigil)
		}
		m, err = cfg.Match("!x explicit sigil")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		if m.Sigil != SigilMajor {
			t.Errorf("Sigil = %v, want ! (capture beats fixed)", m.Sigil)
		}
	})
}

// FuzzMatch holds the pure-core invariants: Match never panics, and every
// outcome is one of the three legal shapes.
func FuzzMatch(f *testing.F) {
	f.Add(":sparkles:(cli)^ add the thing")
	f.Add(`Revert ":bug:~ x"`)
	f.Add("Merge branch 'main'")
	f.Add("")
	f.Add(":bug:~ subject\n\nbody with (?P<semver_sigil>!) inside")
	data, ok := Preset("gemoji")
	if !ok {
		f.Fatalf("Preset(gemoji) missing")
	}
	cfg, err := Load(data)
	if err != nil {
		f.Fatalf("Load: %v", err)
	}
	f.Fuzz(func(t *testing.T, message string) {
		m, err := cfg.Match(message)
		if err != nil {
			return // a config-bug error is a legal outcome, not a panic
		}
		switch {
		case !m.Matched:
			if m.Skip || m.PatternIndex != -1 {
				t.Fatalf("unmatched shape corrupt: %+v", m)
			}
		case m.Skip:
			// no sigil to check
		default:
			switch m.Sigil {
			case SigilNone, SigilPatch, SigilMinor, SigilMajor:
			default:
				t.Fatalf("Sigil = %v outside the alphabet", m.Sigil)
			}
		}
	})
}
