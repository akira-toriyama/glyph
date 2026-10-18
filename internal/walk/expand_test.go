package walk

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
)

// The two tests below go straight at mainFootprint rather than through a walk,
// because both defences they cover are invisible from the outside: each one is
// the reason a walk does NOT blow up, and a walk that does not blow up looks
// exactly like a walk that never needed the guard. Measured before they existed:
// deleting either guard left `go test ./...` green in all 14 packages.

// TestMainFootprintSurvivesAListingLongerThanMain covers the length guard on the
// rebase-alignment arm. FirstParentLog's own contract is that it returns FEWER
// than n commits near the root of a history, and this is the only place in the
// tree that consumes it — after which the loop indexes `mains[i]` by the
// LISTING's length. Without the guard that is an index out of range, i.e. a
// panic, not a wrong answer.
//
// The trigger is not exotic: a repository whose main is shorter than the pull
// that landed on it, which is every repository on its first release, and
// glyph-test on day one. It needs a rebase-merge, because that is the shape
// whose listed SHAs exist on no branch — so `Have` says no to all of them,
// nothing is placed under its own SHA, and the walk falls through to alignment.
//
// The listed MESSAGES have to match main's, in order, for as far as main goes.
// That is not decoration, it is what makes the test bite: the comparison loop
// returns on the first mismatch, so a listing whose first entry already differs
// never reaches an out-of-range index and the guard can be deleted under it with
// the test still green. (Measured — the first version of this test was exactly
// that vacuous.) A rebase preserves messages and order, so matching them is also
// the honest fixture for the shape.
func TestMainFootprintSurvivesAListingLongerThanMain(t *testing.T) {
	dir, _ := testRepo(t) // root commit ":tada:= begin the project"
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	canonical := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	// Three listed commits against a two-commit main, oldest first — the order
	// FirstParentLog returns. The SHAs are well-formed and absent from this
	// repository, which is what a rebase-merge's pre-rebase listing looks like
	// from here, so `Have` says no to all three.
	listing := []gitsource.RawCommit{
		{SHA: "1111111111111111111111111111111111111111", Message: ":tada:= begin the project"},
		{SHA: "2222222222222222222222222222222222222222", Message: ":sparkles:(ui)^ add a menu"},
		{SHA: "3333333333333333333333333333333333333333", Message: ":bug:(ui)~ fix the menu"},
	}

	landed, err := at(dir).mainFootprint(t.Context(), canonical, listing)
	if err != nil {
		t.Fatalf("mainFootprint: %v", err)
	}
	if len(landed) != len(listing) {
		t.Fatalf("landed has %d entries, want one per listed commit (%d)", len(landed), len(listing))
	}
	// An alignment that cannot be attempted is abandoned WHOLE: every entry stays
	// empty, so the caller treats the pull as having no readable footprint rather
	// than placing some of it and guessing the rest.
	for i, sha := range landed {
		if sha != "" {
			t.Errorf("landed[%d] = %q, want empty: main is shorter than the listing, so nothing can be aligned", i, sha)
		}
	}
}

// TestMainFootprintEmptyListingAsksGitNothing covers the other early return:
// with no commits to place there is nothing to ask git ABOUT, so mainFootprint
// answers without consulting git about the canonical commit. The walk hands in
// a canonical from its own local log, where falling through only spends a
// `git log -n 0` that lands on the same return — invisible. A canonical this
// checkout lacks is the input that makes the fall-through observable (an exit-4
// git error), so the unknown canonical is the assertion.
//
// The guard it now exercises is the ALIGNMENT WINDOW's, not a private one for the
// empty listing. Once the window became "what is left after ancestry placed what
// it could" (t-7h15), an empty listing and a fully placed merge-button listing
// became the same state — nothing an alignment could answer about — and a second
// guard ahead of it would be a decision no test could reach.
func TestMainFootprintEmptyListingAsksGitNothing(t *testing.T) {
	dir, _ := testRepo(t)

	landed, err := at(dir).mainFootprint(t.Context(), "4444444444444444444444444444444444444444", nil)
	if err != nil {
		t.Fatalf("an empty listing must not consult git about the canonical commit, got: %v", err)
	}
	if len(landed) != 0 {
		t.Errorf("landed = %v, want empty for an empty listing", landed)
	}
}

// TestMainFootprintDoubleLandingKeepsGitsAnswer pins the ratified half of the
// t-7h15 nine-shape probe. Three of its shapes have TWO defensible answers,
// because the same change reached main twice: a rebase-merged pull's ORIGINAL
// commit later lands verbatim through another pull (GitHub lists against a
// stored base sha, so the entry outlives its own landing), or the branch
// carried a cherry-pick of a commit already on main and the rebase dropped the
// duplicate. The probe scored those shapes for "the copy this pull's own
// rebase wrote"; the tree answers with the landing GIT states, and that answer
// is ratified, not incidental (t-nsww): an ancestor sha landed as itself
// whoever landed it, a dropped replay aligns to the commit that made it
// redundant, and "this pull's copy is the real landing" is an intent git
// nowhere records. The readings reach different verdicts only when the two
// landings straddle the walk's base tag — and there git's answer maps the
// entry to the released landing, so the range drops it instead of counting the
// change again through the in-range copy (DESIGN §4).
//
// bite-exempt: ratifies the behaviour the tree already has, so it cannot fail
// the pre-PR source; the mutation-ledger row
// footprint-double-landing-counts-released-work-again.patch is what proves it
// still bites.
func TestMainFootprintDoubleLandingKeepsGitsAnswer(t *testing.T) {
	// Every fixture commit gets its own author date, because the doubles here
	// are near-identical on purpose: git REUSES a sha when tree, parents,
	// message, identity and dates all coincide, and this test needs "the same
	// change under two shas" to stay two shas. (An undated version of the
	// probe was a no-op for exactly that reason.)
	seq := 0
	commit := func(t *testing.T, dir, file, message string) string {
		t.Helper()
		seq++
		if file != "" {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(message+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			testGit(t, dir, "akira-toriyama", "add", "-A")
		}
		testGit(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty",
			"--date", fmt.Sprintf("2026-01-01T00:00:%02dZ", seq), "-m", message)
		return testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	}

	t.Run("the original lands later through another pull", func(t *testing.T) {
		dir, _ := testRepo(t)
		// The pull's two commits, pre-rebase, still on their branch.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-2")
		b1 := commit(t, dir, "fix.txt", ":bug:(ui)~ fix the menu")
		b2 := commit(t, dir, "speed.txt", ":zap:(ui)~ speed up the menu")
		// The rebase-merge replayed both onto main; GitHub names the last
		// replayed commit as merge_commit_sha, and the listing keeps b1 and b2.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		commit(t, dir, "fix.txt", ":bug:(ui)~ fix the menu")                     // b1'
		canonical := commit(t, dir, "speed.txt", ":zap:(ui)~ speed up the menu") // b2'
		// A later pull branches from the ORIGINAL b1 and lands through the
		// merge button — now b1 itself is an ancestor of HEAD, and the same
		// change sits on main twice: as b1 (that pull) and as b1' (this one).
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-3", b1)
		commit(t, dir, "mention.txt", ":memo:= mention the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		testGit(t, dir, "akira-toriyama", "merge", "-q", "--no-ff",
			"-m", "Merge pull request #3 from akira-toriyama/topic-3", "topic-3")

		landed, err := at(dir).mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: b1, Parents: 1, Message: ":bug:(ui)~ fix the menu"},
			{SHA: b2, Parents: 1, Message: ":zap:(ui)~ speed up the menu"},
		})
		if err != nil {
			t.Fatalf("mainFootprint: %v", err)
		}
		if len(landed) != 2 {
			t.Fatalf("landed = %v, want 2 entries", landed)
		}
		if landed[0] != b1 {
			t.Errorf("landed[0] = %.7s, want %.7s — the sha's own landing is a git fact and outranks the copy this pull's rebase wrote", landed[0], b1)
		}
		if landed[1] != canonical {
			t.Errorf("landed[1] = %.7s, want %.7s — the half only a rebase explains must still align", landed[1], canonical)
		}
	})

	t.Run("the original and everything under it land later", func(t *testing.T) {
		dir, _ := testRepo(t)
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-2")
		b1 := commit(t, dir, "fix.txt", ":bug:(ui)~ fix the menu")
		b2 := commit(t, dir, "speed.txt", ":zap:(ui)~ speed up the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		commit(t, dir, "fix.txt", ":bug:(ui)~ fix the menu")                     // b1'
		canonical := commit(t, dir, "speed.txt", ":zap:(ui)~ speed up the menu") // b2'
		// The later pull branches from b2, the LAST listed commit — and a
		// branch carries its ancestry, so landing it lands b1 as well. Step 1
		// places the whole listing and the rebase run plays no part.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-3", b2)
		commit(t, dir, "mention.txt", ":memo:= mention the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		testGit(t, dir, "akira-toriyama", "merge", "-q", "--no-ff",
			"-m", "Merge pull request #3 from akira-toriyama/topic-3", "topic-3")

		landed, err := at(dir).mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: b1, Parents: 1, Message: ":bug:(ui)~ fix the menu"},
			{SHA: b2, Parents: 1, Message: ":zap:(ui)~ speed up the menu"},
		})
		if err != nil {
			t.Fatalf("mainFootprint: %v", err)
		}
		if len(landed) != 2 {
			t.Fatalf("landed = %v, want 2 entries", landed)
		}
		if landed[0] != b1 || landed[1] != b2 {
			t.Errorf("landed = [%.7s %.7s], want [%.7s %.7s] — both originals are ancestors now, and both keep their own shas", landed[0], landed[1], b1, b2)
		}
	})

	t.Run("a cherry-pick of a commit already on main", func(t *testing.T) {
		dir, root := testRepo(t)
		// The change is on main FIRST — and shipped. The tag is why the answer
		// matters: C under v0.2.0 and a copy in range would straddle the base.
		C := commit(t, dir, "shared.txt", ":bug:(core)~ fix the hotfix")
		testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
		// The branch cherry-picked the same patch under its own sha, added one
		// commit of its own, and was rebase-merged: the rebase found c's patch
		// already upstream, dropped it, and replayed only d.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-2", root)
		c := commit(t, dir, "shared.txt", ":bug:(core)~ fix the hotfix")
		d := commit(t, dir, "add.txt", ":sparkles:(core)^ add a thing")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		canonical := commit(t, dir, "add.txt", ":sparkles:(core)^ add a thing") // d'

		landed, err := at(dir).mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: c, Parents: 1, Message: ":bug:(core)~ fix the hotfix"},
			{SHA: d, Parents: 1, Message: ":sparkles:(core)^ add a thing"},
		})
		if err != nil {
			t.Fatalf("mainFootprint: %v", err)
		}
		if len(landed) != 2 {
			t.Fatalf("landed = %v, want 2 entries", landed)
		}
		if landed[0] != C {
			t.Errorf("landed[0] = %.7s, want %.7s — the dropped replay aligns, message-verified, to the commit that made it redundant, one slot past the rebase's own run", landed[0], C)
		}
		if landed[1] != canonical {
			t.Errorf("landed[1] = %.7s, want %.7s — the replayed half still lands on its rewrite", landed[1], canonical)
		}
	})
}
