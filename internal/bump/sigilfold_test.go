package bump

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
)

// sigilConfig is the ratified `glyph init --gemoji` shape, reduced to what
// the fold reads: the gemoji pattern, the raw-revert fixed sigil, the merge
// skip, and the bot exclusions.
func sigilConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(`schema = 1
exclude_authors = ['dependabot[bot]']

[[patterns]]
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!]) (?P<subject>.+)'

[[patterns]]
pattern = '^Revert "(?P<subject>.+)"'
semver_sigil = '~'

[[patterns]]
pattern = '^Merge '
skip = true
`))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestSigilLevel(t *testing.T) {
	cases := []struct {
		sigil config.Sigil
		want  Level
	}{
		{config.SigilNone, LevelNone},
		{config.SigilPatch, LevelPatch},
		{config.SigilMinor, LevelMinor},
		{config.SigilMajor, LevelMajor},
	}
	for _, c := range cases {
		if got := SigilLevel(c.sigil); got != c.want {
			t.Errorf("SigilLevel(%v) = %v, want %v", c.sigil, got, c.want)
		}
	}
}

func TestFoldSigils(t *testing.T) {
	cfg := sigilConfig(t)
	cases := []struct {
		name    string
		commits []SigilCommit
		want    Level
	}{
		{"empty range is none", nil, LevelNone},
		{"max wins over the lattice", []SigilCommit{
			{SHA: "a", Author: "akira", Message: ":memo:= reword"},
			{SHA: "b", Author: "akira", Message: ":sparkles:^ add"},
			{SHA: "c", Author: "akira", Message: ":bug:~ fix"},
		}, LevelMinor},
		{"order cannot move the version", []SigilCommit{
			{SHA: "c", Author: "akira", Message: ":bug:~ fix"},
			{SHA: "b", Author: "akira", Message: ":sparkles:^ add"},
			{SHA: "a", Author: "akira", Message: ":memo:= reword"},
		}, LevelMinor},
		{"major short-circuits nothing but still tops", []SigilCommit{
			{SHA: "a", Author: "akira", Message: ":boom:! drop the flag"},
			{SHA: "b", Author: "akira", Message: ":memo:= reword"},
		}, LevelMajor},
		{"fixed sigil folds like a captured one", []SigilCommit{
			{SHA: "a", Author: "akira", Message: `Revert ":sparkles:^ add"`},
		}, LevelPatch},
		{"skip pattern leaves the fold", []SigilCommit{
			{SHA: "a", Author: "akira", Message: "Merge pull request #1"},
			{SHA: "b", Author: "akira", Message: ":bug:~ fix"},
		}, LevelPatch},
		{"all-none folds to none", []SigilCommit{
			{SHA: "a", Author: "akira", Message: ":memo:= reword"},
		}, LevelNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got, err := FoldSigils(c.commits, cfg)
			if err != nil {
				t.Fatalf("FoldSigils: %v", err)
			}
			if got != c.want {
				t.Errorf("FoldSigils = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFoldSigilsRefusesUnmatched asserts the ratified Q2 decision: a
// non-excluded commit no pattern claims refuses the whole range with the lint
// verdict, never folds as a silent none.
func TestFoldSigilsRefusesUnmatched(t *testing.T) {
	cfg := sigilConfig(t)
	_, _, err := FoldSigils([]SigilCommit{
		{SHA: "aaa1", Author: "akira", Message: ":bug:~ fine"},
		{SHA: "bbb2", Author: "akira", Message: "fix: wrong grammar entirely"},
	}, cfg)
	if err == nil {
		t.Fatalf("FoldSigils accepted a range holding an unmatched commit")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.CodeLint {
		t.Fatalf("err = %v, want *core.Error with CodeLint (exit 3)", err)
	}
	if !strings.Contains(err.Error(), "bbb2") {
		t.Errorf("refusal must name the offending commit: %v", err)
	}
}

// TestFoldSigilsExcludesAuthorBeforeMatching asserts the exclusion ORDER:
// exclude_authors exists for bots, whose messages are exactly the ones the
// patterns do not describe, so the author check must run before the message
// is matched — a bot's unmatched message is not a range refusal.
func TestFoldSigilsExcludesAuthorBeforeMatching(t *testing.T) {
	cfg := sigilConfig(t)
	_, got, err := FoldSigils([]SigilCommit{
		{SHA: "a", Author: "dependabot[bot]", Message: "Bump golang.org/x/text from 0.1 to 0.2"},
		{SHA: "b", Author: "akira", Message: ":bug:~ fix"},
	}, cfg)
	if err != nil {
		t.Fatalf("FoldSigils refused a range over an excluded author's message: %v", err)
	}
	if got != LevelPatch {
		t.Errorf("FoldSigils = %v, want patch (bot commit contributes nothing)", got)
	}
	// The exclusion also silences a would-be level, not only a would-be error.
	_, got, err = FoldSigils([]SigilCommit{
		{SHA: "a", Author: "dependabot[bot]", Message: ":boom:! bot claims a major"},
		{SHA: "b", Author: "akira", Message: ":memo:= reword"},
	}, cfg)
	if err != nil {
		t.Fatalf("FoldSigils: %v", err)
	}
	if got != LevelNone {
		t.Errorf("FoldSigils = %v, want none (excluded author cannot move the version)", got)
	}
}

func TestFoldSigilsPropagatesConfigBug(t *testing.T) {
	cfg, err := config.Load([]byte("schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>.) '\n"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, _, err = FoldSigils([]SigilCommit{{SHA: "abc3", Author: "akira", Message: "z captured outside the alphabet"}}, cfg)
	if err == nil {
		t.Fatalf("FoldSigils accepted an unparseable sigil capture")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.CodeLint {
		t.Fatalf("err = %v, want *core.Error with CodeLint", err)
	}
	if !strings.Contains(err.Error(), "abc3") {
		t.Errorf("error must name the commit that surfaced the config bug: %v", err)
	}
}
