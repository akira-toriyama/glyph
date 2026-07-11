package bump

import (
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// loadTable loads the real embedded rules table — classification is tested
// against the shipped source of truth, not a mock copy of it.
func loadTable(t *testing.T) *gitmoji.Table {
	t.Helper()
	table, err := gitmoji.Load()
	if err != nil {
		t.Fatalf("gitmoji.Load(): %v", err)
	}
	return table
}

// TestClassify pins the classification rule: an unknown code is a lint error
// (never a silent level), a breaking commit short-circuits to major, and
// everything else takes its table rung.
func TestClassify(t *testing.T) {
	table := loadTable(t)
	cases := []struct {
		name   string
		commit parser.Commit
		want   gitmoji.Bump
	}{
		{"patch rung", parser.Commit{Gitmoji: ":bug:"}, gitmoji.BumpPatch},
		{"minor rung is sparkles only", parser.Commit{Gitmoji: ":sparkles:"}, gitmoji.BumpMinor},
		{"major rung is boom without any flag", parser.Commit{Gitmoji: ":boom:"}, gitmoji.BumpMajor},
		{"none rung", parser.Commit{Gitmoji: ":memo:"}, gitmoji.BumpNone},
		{"ratified deviation stays none", parser.Commit{Gitmoji: ":wrench:"}, gitmoji.BumpNone},
		{"breaking overrides a none rung", parser.Commit{Gitmoji: ":memo:", Breaking: true}, gitmoji.BumpMajor},
		{"breaking overrides a patch rung", parser.Commit{Gitmoji: ":truck:", Breaking: true}, gitmoji.BumpMajor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Classify(c.commit, table)
			if err != nil {
				t.Fatalf("Classify(%+v): unexpected error: %v", c.commit, err)
			}
			if got != c.want {
				t.Fatalf("Classify(%+v) = %s, want %s", c.commit, got, c.want)
			}
		})
	}
}

// TestClassifyUnknownCode: an unknown gitmoji is a hard lint failure even when
// the commit is flagged breaking — membership is checked before everything.
func TestClassifyUnknownCode(t *testing.T) {
	table := loadTable(t)
	for _, commit := range []parser.Commit{
		{Gitmoji: ":not-a-real-code:", SHA: "abc1234"},
		{Gitmoji: ":not-a-real-code:", Breaking: true},
	} {
		_, err := Classify(commit, table)
		if err == nil {
			t.Fatalf("Classify(%+v) should fail on an unknown code", commit)
		}
		ce := core.AsError(err)
		if ce == nil || ce.Code != core.CodeLint {
			t.Fatalf("Classify(%+v) error = %v, want a *core.Error with the lint code", commit, err)
		}
	}
}

// TestReduce pins the fold: max over the lattice, none for an empty slice.
func TestReduce(t *testing.T) {
	cases := []struct {
		name   string
		levels []gitmoji.Bump
		want   gitmoji.Bump
	}{
		{"empty is none", nil, gitmoji.BumpNone},
		{"single", []gitmoji.Bump{gitmoji.BumpPatch}, gitmoji.BumpPatch},
		{"none and patch", []gitmoji.Bump{gitmoji.BumpNone, gitmoji.BumpPatch}, gitmoji.BumpPatch},
		{"minor beats patch", []gitmoji.Bump{gitmoji.BumpPatch, gitmoji.BumpMinor, gitmoji.BumpNone}, gitmoji.BumpMinor},
		{"major beats all", []gitmoji.Bump{gitmoji.BumpMinor, gitmoji.BumpMajor, gitmoji.BumpPatch}, gitmoji.BumpMajor},
		{"all none", []gitmoji.Bump{gitmoji.BumpNone, gitmoji.BumpNone}, gitmoji.BumpNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Reduce(c.levels); got != c.want {
				t.Fatalf("Reduce(%v) = %s, want %s", c.levels, got, c.want)
			}
		})
	}
}

// TestExcluded pins which commits stay out of lint and the version fold: bot
// authors, merge commits (structurally, and by GitHub's "Merge " subject),
// autosquash artifacts, and git's own `Revert "…"` messages — exactly the
// tolerance the retired shell commit-lint gave existing history.
func TestExcluded(t *testing.T) {
	cases := []struct {
		name    string
		author  string
		subject string
		parents int
		want    bool
	}{
		{"human commit stays in", "akira-toriyama", ":bug: fix a crash", 1, false},
		{"dependabot", "dependabot[bot]", "build(deps): bump actions/checkout", 1, true},
		{"any bot suffix", "renovate[bot]", ":arrow_up: bump deps", 1, true},
		{"github-actions", "github-actions", ":robot: automated commit", 1, true},
		{"github-actions[bot]", "github-actions[bot]", "chore: sync", 1, true},
		{"web-flow", "web-flow", ":bug: via web ui", 1, true},
		{"merge commit by parent count", "akira-toriyama", ":bug: fix in a merge", 2, true},
		{"merge subject", "akira-toriyama", "Merge branch 'main' into feat/x", 1, true},
		{"git revert subject", "akira-toriyama", "Revert \":bug: fix a crash\"", 1, true},
		{"fixup artifact", "akira-toriyama", "fixup! :bug: fix a crash", 1, true},
		{"squash artifact", "akira-toriyama", "squash! :bug: fix a crash", 1, true},
		{"rewind revert stays in", "akira-toriyama", ":rewind: revert the flag change", 1, false},
		{"bot-ish name without marker stays in", "botond", ":bug: fix a crash", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, got := Excluded(c.author, c.subject, c.parents)
			if got != c.want {
				t.Fatalf("Excluded(%q, %q, %d) = %v (%q), want %v", c.author, c.subject, c.parents, got, reason, c.want)
			}
			if got && reason == "" {
				t.Fatalf("Excluded(%q, %q, %d) excluded without a reason", c.author, c.subject, c.parents)
			}
			if !got && reason != "" {
				t.Fatalf("Excluded(%q, %q, %d) kept the commit but returned reason %q", c.author, c.subject, c.parents, reason)
			}
		})
	}
}

// FuzzReduceInvariants: the fold must be order-independent (reversal and
// rotation), idempotent (folding the result back in changes nothing), and
// monotone (a prefix never exceeds the whole) — so squash order can never
// change the version.
func FuzzReduceInvariants(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3}, 1)
	f.Add([]byte{3, 3, 0}, 2)
	f.Add([]byte{}, 0)
	rungs := []gitmoji.Bump{gitmoji.BumpNone, gitmoji.BumpPatch, gitmoji.BumpMinor, gitmoji.BumpMajor}
	f.Fuzz(func(t *testing.T, seed []byte, rot int) {
		levels := make([]gitmoji.Bump, len(seed))
		for i, b := range seed {
			levels[i] = rungs[int(b)%len(rungs)]
		}
		want := Reduce(levels)

		reversed := make([]gitmoji.Bump, len(levels))
		for i, l := range levels {
			reversed[len(levels)-1-i] = l
		}
		if got := Reduce(reversed); got != want {
			t.Fatalf("Reduce(reversed %v) = %s, want %s", levels, got, want)
		}

		if len(levels) > 0 {
			k := ((rot % len(levels)) + len(levels)) % len(levels)
			rotated := append(append([]gitmoji.Bump{}, levels[k:]...), levels[:k]...)
			if got := Reduce(rotated); got != want {
				t.Fatalf("Reduce(rotated %v by %d) = %s, want %s", levels, k, got, want)
			}
		}

		if got := Reduce(append(append([]gitmoji.Bump{}, levels...), want)); got != want {
			t.Fatalf("Reduce is not idempotent: folding %s back into %v gave %s", want, levels, got)
		}

		for n := range levels {
			if Reduce(levels[:n]).Rank() > want.Rank() {
				t.Fatalf("Reduce(%v[:%d]) exceeds Reduce of the whole", levels, n)
			}
		}
	})
}
