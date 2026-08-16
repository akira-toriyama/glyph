package config

import (
	"strings"
	"testing"
)

func TestLint(t *testing.T) {
	cfg := loadGemoji(t)
	cases := []struct {
		name    string
		message string
		author  string
		want    LintVerdict
	}{
		{"conforming message passes", ":bug:(cli)~ stop the crash", "akira", LintVerdict{OK: true}},
		{"skip pattern passes", "Merge pull request #1", "akira", LintVerdict{OK: true}},
		{"fixed-sigil pattern passes", `Revert ":bug:~ x"`, "akira", LintVerdict{OK: true}},
		{"excluded author is not judged", "Bump x from 1 to 2", "dependabot[bot]", LintVerdict{Excluded: true}},
		{"excluded author with conforming message still excluded", ":bug:~ fix", "renovate[bot]", LintVerdict{Excluded: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cfg.Lint(c.message, c.author); got != c.want {
				t.Errorf("Lint(%q, %q) = %+v, want %+v", c.message, c.author, got, c.want)
			}
		})
	}
}

// TestLintHasNoTaste asserts the ratified stance: lint checks that a pattern
// claims the message, never whether the combination is advisable. A docs
// change carrying a major sigil is the author's call.
func TestLintHasNoTaste(t *testing.T) {
	cfg := loadGemoji(t)
	for _, msg := range []string{
		":memo:! docs change that declares itself breaking",
		":boom:= a boom that moves nothing",
		":bug:(scope)! a bug fix that majors",
	} {
		if got := cfg.Lint(msg, "akira"); !got.OK {
			t.Errorf("Lint(%q) = %+v, want OK — combinations are the author's call", msg, got)
		}
	}
}

func TestLintViolations(t *testing.T) {
	cfg := loadGemoji(t)
	t.Run("no pattern matches", func(t *testing.T) {
		got := cfg.Lint("fix: wrong grammar", "akira")
		if got.OK || got.Excluded {
			t.Fatalf("Lint = %+v, want violation", got)
		}
		if !strings.Contains(got.Reason, "matches none") {
			t.Errorf("Reason = %q", got.Reason)
		}
	})
	t.Run("capture outside the alphabet", func(t *testing.T) {
		loose := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>.) '\n")
		got := loose.Lint("z broken", "akira")
		if got.OK || got.Excluded {
			t.Fatalf("Lint = %+v, want violation", got)
		}
		if !strings.Contains(got.Reason, "invalid semver_sigil") {
			t.Errorf("Reason = %q", got.Reason)
		}
	})
}
