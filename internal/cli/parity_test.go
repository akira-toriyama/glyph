package cli

import (
	"encoding/json"
	"testing"
)

// parityTwins are message pairs that SAY the same thing in the two
// vocabularies: each conventional message uses the type whose counterpart
// gitmoji the twin uses (the §3.1 derivation), with identical scope, subject
// and body. The pairs deliberately cover every distinct bump the conventional
// table can produce (minor, patch ×3 sources, none ×2) plus both breaking
// triggers the profiles share (`!` and the footer) — the lattice's whole
// reachable range under that vocabulary.
var parityTwins = []struct{ gitmoji, conventional string }{
	{":sparkles:(ui) add a menu", "feat(ui): add a menu"},
	{":bug: fix a crash", "fix: fix a crash"},
	{":zap:(core) speed up the fold", "perf(core): speed up the fold"},
	{":rewind: undo the parser change", "revert: undo the parser change"},
	{":memo: document the bump model", "docs: document the bump model"},
	{":recycle: restructure the walk", "refactor: restructure the walk"},
	{":bug:(api)! drop the flag", "fix(api)!: drop the flag"},
	{":recycle: rename the field\n\nBREAKING CHANGE: consumers must re-map", "refactor: rename the field\n\nBREAKING CHANGE: consumers must re-map"},
}

// TestVerdictParityAcrossProfiles is epic e-b3t3's goal pinned end to end:
// two hermetic repositories carrying the SAME changes in the two
// vocabularies, walked by the real bump command under each profile, must
// produce the same verdict — level, next version, and the per-commit levels
// in order. This is the invariant the derivation test defends one table row
// at a time, asserted where it matters instead: on the walk's actual output,
// where a grammar bug (a swallowed `!`, a footer read under one profile only)
// would also surface, which no table comparison can see.
//
// Growing the twins list keeps this test honest for free; shrinking the
// COVERAGE cannot happen silently, because the loop below fails if the twins
// stop spanning both breaking triggers and a minor.
func TestVerdictParityAcrossProfiles(t *testing.T) {
	type verdict struct {
		Level   string `json:"level"`
		Next    string `json:"next"`
		Commits []struct {
			Level    string `json:"level"`
			Breaking bool   `json:"breaking"`
		} `json:"commits"`
	}
	walk := func(profile string, messages []string) verdict {
		t.Helper()
		dir, base := testRepo(t)
		t.Chdir(dir)
		for _, m := range messages {
			testCommit(t, dir, "akira-toriyama", m)
		}
		code, got, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--profile="+profile, "--json")
		if code != 0 {
			t.Fatalf("bump --profile=%s exited %d\nstderr: %s", profile, code, stderr)
		}
		var v verdict
		if err := json.Unmarshal([]byte(got), &v); err != nil {
			t.Fatalf("bump --json undecodable: %v\n%s", err, got)
		}
		return v
	}

	var gms, convs []string
	sawBang, sawFooter, sawMinor := false, false, false
	for _, tw := range parityTwins {
		gms = append(gms, tw.gitmoji)
		convs = append(convs, tw.conventional)
	}
	g := walk("gitmoji", gms)
	c := walk("conventional", convs)

	if g.Level != c.Level || g.Next != c.Next {
		t.Fatalf("the twin walks disagree: gitmoji %s/%s, conventional %s/%s — the profiles have split on identical changes",
			g.Level, g.Next, c.Level, c.Next)
	}
	if len(g.Commits) != len(parityTwins) || len(c.Commits) != len(parityTwins) {
		t.Fatalf("walks saw %d/%d commits, want %d each — a twin was excluded on one side only",
			len(g.Commits), len(c.Commits), len(parityTwins))
	}
	for i := range g.Commits {
		if g.Commits[i].Level != c.Commits[i].Level || g.Commits[i].Breaking != c.Commits[i].Breaking {
			t.Errorf("twin %d (%q / %q): gitmoji %s breaking=%v, conventional %s breaking=%v",
				i, parityTwins[i].gitmoji, parityTwins[i].conventional,
				g.Commits[i].Level, g.Commits[i].Breaking, c.Commits[i].Level, c.Commits[i].Breaking)
		}
		if c.Commits[i].Breaking {
			sawFooter = sawFooter || len(parityTwins[i].conventional) > len("fix(api)!: drop the flag")
			sawBang = sawBang || parityTwins[i].conventional == "fix(api)!: drop the flag"
		}
		if c.Commits[i].Level == "minor" {
			sawMinor = true
		}
	}
	if !sawBang || !sawFooter || !sawMinor {
		t.Fatalf("the twins no longer span the lattice (bang=%v footer=%v minor=%v) — a shrunk list "+
			"is a shrunk claim; restore the coverage before trusting the parity", sawBang, sawFooter, sawMinor)
	}

	// Both walks must also LINT green under their own profile: parity of
	// verdicts would be vacuous if one vocabulary's twin messages were not
	// even legal in it.
	for profile, msgs := range map[string][]string{"gitmoji": gms, "conventional": convs} {
		dir, base := testRepo(t)
		t.Chdir(dir)
		for _, m := range msgs {
			testCommit(t, dir, "akira-toriyama", m)
		}
		if code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD", "--profile="+profile); code != 0 {
			t.Fatalf("the %s twins do not lint clean under their own profile (exit %d):\n%s", profile, code, stderr)
		}
	}
}
