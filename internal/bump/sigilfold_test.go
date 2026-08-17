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
pattern = '^:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) (?P<subject>.+)'

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
		// '%' is a breaking change like any other to every reader of Level;
		// what makes it more lives in Decision.Promote, not on the lattice.
		{config.SigilPromote, LevelMajor},
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
			if got.Level != c.want {
				t.Errorf("FoldSigils = %v, want %v", got.Level, c.want)
			}
			if got.Promote {
				t.Errorf("FoldSigils promoted a range holding no '%%' commit")
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
	if got.Level != LevelPatch {
		t.Errorf("FoldSigils = %v, want patch (bot commit contributes nothing)", got.Level)
	}
	// The exclusion also silences a would-be level, not only a would-be error.
	_, got, err = FoldSigils([]SigilCommit{
		{SHA: "a", Author: "dependabot[bot]", Message: ":boom:! bot claims a major"},
		{SHA: "b", Author: "akira", Message: ":memo:= reword"},
	}, cfg)
	if err != nil {
		t.Fatalf("FoldSigils: %v", err)
	}
	if got.Level != LevelNone {
		t.Errorf("FoldSigils = %v, want none (excluded author cannot move the version)", got.Level)
	}
}

// TestFoldSigilsPromoteIsOrderIndependent asserts that the promote half of
// the fold is OR — a '%' anywhere in the range promotes it, and where it sits
// among the other commits cannot change the answer. Order-independence is the
// property that lets a squash merge reorder a pull request's commits without
// moving the version, and promote had to earn it separately from the lattice.
func TestFoldSigilsPromoteIsOrderIndependent(t *testing.T) {
	cfg := sigilConfig(t)
	promote := SigilCommit{SHA: "p", Author: "akira", Message: ":rocket:% call it 1.0"}
	fix := SigilCommit{SHA: "f", Author: "akira", Message: ":bug:~ fix"}
	feature := SigilCommit{SHA: "s", Author: "akira", Message: ":sparkles:^ add"}
	orders := map[string][]SigilCommit{
		"first":  {promote, fix, feature},
		"middle": {fix, promote, feature},
		"last":   {fix, feature, promote},
	}
	for name, commits := range orders {
		t.Run(name, func(t *testing.T) {
			_, got, err := FoldSigils(commits, cfg)
			if err != nil {
				t.Fatalf("FoldSigils: %v", err)
			}
			if !got.Promote {
				t.Fatalf("FoldSigils dropped the promote with '%%' %s: %+v", name, got)
			}
			if got.Level != LevelMajor {
				t.Fatalf("FoldSigils = %v, want major (a promotion is breaking)", got.Level)
			}
		})
	}
}

// TestFoldSigilsPromoteClassifiesAsMajor pins the verdict ROW a '%' commit
// produces, which is what the release notes and pr-verdict read: the sigil
// column keeps the '%' the author wrote, and the level column says major so
// the commit lands in Breaking Changes instead of vanishing from every
// surface that filters on a closed four-word vocabulary.
func TestFoldSigilsPromoteClassifiesAsMajor(t *testing.T) {
	cfg := sigilConfig(t)
	rows, got, err := FoldSigils([]SigilCommit{
		{SHA: "p", Author: "akira", Message: ":rocket:% call it 1.0"},
	}, cfg)
	if err != nil {
		t.Fatalf("FoldSigils: %v", err)
	}
	if !got.Promote || got.Level != LevelMajor {
		t.Fatalf("fold = %+v, want {major true}", got)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Sigil != "%" || rows[0].Level != "major" {
		t.Fatalf("row = %+v, want sigil %%%% and level major", rows[0])
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
