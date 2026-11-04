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
		{"minor with scope", ":sparkles:(doctor)^ add the profile checks", SigilMinor, "doctor", ":sparkles:(doctor)^ add the profile checks"},
		{"patch without scope", ":bug:~ stop the crash", SigilPatch, "", ":bug:~ stop the crash"},
		{"major", ":boom:(cli)! drop the profile flag", SigilMajor, "cli", ":boom:(cli)! drop the profile flag"},
		{"none", ":memo:= reword the readme", SigilNone, "", ":memo:= reword the readme"},
		{"body does not leak into subject", ":bug:~ one line\n\nbody text here", SigilPatch, "", ":bug:~ one line"},
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
//
// It also pins WHERE the preset's subject group starts, which is a release-body
// decision rather than a parsing one. The group takes the whole line, quotes
// included, because the notes render $subject: capturing only the text inside
// the quotes puts the REVERTED commit's own subject in the body, so a release
// that removed something announces it as though it had been added. Measured in
// the field — glyph-test v0.4.1 shipped
//
//	## Fixes
//	- :sparkles:(soak)^ add the page the next round will revert (#53) (#54)
//
// for a pull request whose entire content was `git revert`. Nothing in the line
// said revert, and the gitmoji it carried was the one for a new feature.
func TestMatchFixedSigil(t *testing.T) {
	cfg := loadGemoji(t)
	const msg = `Revert ":sparkles:(cli)^ add the thing"`
	m, err := cfg.Match(msg)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !m.Matched || m.Skip {
		t.Fatalf("Match = %+v, want a sigil match", m)
	}
	if m.Sigil != SigilPatch {
		t.Errorf("Sigil = %v, want ~ (the pattern's fixed value)", m.Sigil)
	}
	if got := m.Groups["subject"]; got != msg {
		t.Errorf("subject = %q, want the whole line %q — a revert's release-note line has to "+
			"say a revert happened, and $subject is all the preset's template renders", got, msg)
	}
	// The inner subject must not be what survives: that is the exact string
	// that shipped as a Fixes entry describing an addition.
	if got := m.Groups["subject"]; got == `:sparkles:(cli)^ add the thing` {
		t.Error("subject is the reverted commit's own subject; a release body built from it " +
			"describes the change being made, in the section for undoing it")
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
		cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]?)x'\n")
		_, err := cfg.Match("x no sigil given")
		if err == nil || !strings.Contains(err.Error(), "captured nothing") {
			t.Fatalf("err = %v, want captured-nothing error", err)
		}
	})
	t.Run("empty capture falls back to fixed", func(t *testing.T) {
		cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]?)x'\nsemver_sigil = '^'\n")
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

// TestMatchReadsTheGroupThatTookPart: a name a pattern gives to one group per
// alternative binds the capture of the alternative the match took. Go's
// FindStringSubmatch fills a repeated name in index order, so read that way
// the later group's -1,-1 stood in for the earlier one's capture: under the
// pattern below `! :sparkles: first alt` exited 3 as a sigil group that
// captured nothing, and beside `semver_sigil = '='` it folded to none — a
// major shipped as no release, with no warning (measured 2026-11-04 at
// 2f9428a, t-wmk2). Both alternatives, for the sigil and the scope alike, and
// the two shapes a participant can take — a repeat's last turn, and an empty
// capture that did take part, which stands and is not a group that took none.
func TestMatchReadsTheGroupThatTookPart(t *testing.T) {
	cfg := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]) :[a-z]+:(?:\\((?P<scope>[a-z]+)\\))? .+|^:[a-z]+:(?:\\((?P<scope>[A-Z]+)\\))?(?P<semver_sigil>[=~^!%]) .+'\n")
	for _, c := range []struct {
		message string
		sigil   Sigil
		scope   string
	}{
		{"! :sparkles: first alt", SigilMajor, ""},
		{"~ :bug:(cli) first alt with a scope", SigilPatch, "cli"},
		{":sparkles:! second alt", SigilMajor, ""},
		{":bug:(CLI)~ second alt with a scope", SigilPatch, "CLI"},
	} {
		m, err := cfg.Match(c.message)
		if err != nil {
			t.Fatalf("Match(%q): %v", c.message, err)
		}
		if !m.Matched || m.Skip || m.Sigil != c.sigil || m.Groups["scope"] != c.scope {
			t.Errorf("Match(%q) = sigil %v scope %q (matched %v), want sigil %v scope %q", c.message, m.Sigil, m.Groups["scope"], m.Matched, c.sigil, c.scope)
		}
	}

	fixed := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]) :[a-z]+: .+|^:[a-z]+:(?P<semver_sigil>[=~^!%]) .+'\nsemver_sigil = '='\n")
	if m, err := fixed.Match("! :boom: break via first alt"); err != nil || m.Sigil != SigilMajor {
		t.Errorf("Match under a fixed = read the first alternative's ! as %v, %v; want ! — the fixed value stands in only for a capture no alternative made", m.Sigil, err)
	}

	repeat := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?:\\[(?P<scope>[a-z]+)\\])*(?P<semver_sigil>[=~^!%]) .+'\n")
	if m, err := repeat.Match("[a][haiku]~ swap"); err != nil || m.Groups["scope"] != "haiku" {
		t.Errorf("Match under a repeat = scope %q, %v; want the last turn's haiku", m.Groups["scope"], err)
	}

	empty := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<scope>[a-z]*)(?P<semver_sigil>[=~^!%]) .+|^x(?P<scope>[a-z]+)(?P<semver_sigil>[=~^!%]) .+'\n")
	if m, err := empty.Match("~ swap"); err != nil || m.Groups["scope"] != "" {
		t.Errorf("Match = scope %q, %v; want \"\" — an empty capture that took part is the alternative's answer", m.Groups["scope"], err)
	}
}

// FuzzMatch holds the pure-core invariants: Match never panics, and every
// outcome is one of the three legal shapes.
func FuzzMatch(f *testing.F) {
	f.Add(":sparkles:(cli)^ add the thing")
	f.Add(`Revert ":bug:~ x"`)
	f.Add("Merge branch 'main'")
	f.Add("")
	f.Add(":bug:~ subject\n\nbody with (?P<semver_sigil>!) inside")
	f.Add(amendMessage)
	f.Add("amend! fixup! :bug:~ fix b")
	data, ok := Preset("gemoji")
	if !ok {
		f.Fatalf("Preset(gemoji) missing")
	}
	// The preset plus a config that claims every amend! as unlandable. The
	// preset claims only an amend! whose body is a gemoji subject, so the
	// second config keeps the unlandable shape reachable from inputs the
	// engine has not learned to spell that way.
	var cfgs []*Config
	for _, src := range [][]byte{data, []byte(unlandableToml)} {
		cfg, err := Load(src)
		if err != nil {
			f.Fatalf("Load: %v", err)
		}
		cfgs = append(cfgs, cfg)
	}
	f.Fuzz(func(t *testing.T, message string) {
		for _, cfg := range cfgs {
			checkMatchShape(t, cfg, message)
		}
	})
}

func checkMatchShape(t *testing.T, cfg *Config, message string) {
	t.Helper()
	m, err := cfg.Match(message)
	if err != nil {
		return // a config-bug error is a legal outcome, not a panic
	}
	if m.Matched && m.Unlandable != "" {
		t.Fatalf("an unlandable claim must come back unmatched: %+v", m)
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
		case SigilNone, SigilPatch, SigilMinor, SigilMajor, SigilPromote:
		default:
			t.Fatalf("Sigil = %v outside the alphabet", m.Sigil)
		}
	}
}
