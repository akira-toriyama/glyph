package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins what --pr reads of an open pull (walk.Walker.OpenPull; DESIGN
// §4, "A stacked pull lists what its base landed"): the listing less every
// commit another pull already landed on the released branch, read back from
// the listing's fork to HEAD. A stacked pull carries its base pull's commits
// after the base merges, and read whole the PR side promised what the release
// walk then refused to give (t-vc8c: glyph-test-2#7 over #6).

// TestPullSideDropsWhatAnotherPullLanded: #9 is stacked on #1 and lists #1's
// commits; preview's PR side, bump --pr and notes --pr count #9's own commit
// alone, in both shapes — #1 released under the base tag, and #1 merged since
// it. The pending side keeps its own answer: what #1 landed in range is pending.
func TestPullSideDropsWhatAnotherPullLanded(t *testing.T) {
	type verdict struct {
		Level   string `json:"level"`
		Next    string `json:"next"`
		PR      string `json:"pr"`
		Pending string `json:"pending"`
		Body    string `json:"body"`
	}
	const d9 = "d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9"
	t.Run("the base released under the tag", func(t *testing.T) {
		dir, routes, s1 := stackedSquashPair(t)
		root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "v0.1.0^{commit}")
		routes[pullCommitsPath(9)] = apiListing(
			apiCommitOn(stackedA1, ":boom:! break the api", root),
			apiCommitOn(stackedA2, ":memo:= document the break", stackedA1),
			apiCommitOn(d9, ":bug:~ fix a thing", stackedA2))
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		got := decodeJSON[verdict](t, stdout)
		if got.PR != "patch" || got.Level != "patch" || got.Next != "v1.0.1" || strings.Contains(got.Body, "break the api") {
			t.Errorf("preview = pr %s, level %s, next %s; want patch, patch, v1.0.1 and no row for a1\nbody: %s", got.PR, got.Level, got.Next, got.Body)
		}
		want := fmt.Sprintf("::notice::glyph: commit %.7s in pull request #9 already landed on the released branch as %.7s through another pull request — it is not this pull's own work and is not counted", stackedA1, s1)
		if !strings.Contains(stderr, want) {
			t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
		}
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{d9}) {
			t.Errorf("bump --pr 9 --json = exit %d commits %v, want 0 and d9 alone\nstderr: %s", code, shas, stderr)
		}
		code, stdout, stderr = runGlyph(t, "bump", "--pr", "9")
		if code != 0 || stdout != "v1.0.1\n" {
			t.Errorf("bump --pr 9 = exit %d stdout %q, want 0 / v1.0.1\nstderr: %s", code, stdout, stderr)
		}
		code, stdout, stderr = runGlyph(t, "notes", "--pr", "9")
		if code != 0 || !strings.Contains(stdout, "fix a thing (#9)") || strings.Contains(stdout, "break the api") {
			t.Errorf("notes --pr 9 = exit %d, want 0 with #9's fix and nothing #1 shipped:\n%s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("the base merged since the tag", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		s1 := squashCommit(t, dir, "Reshape the api", 1)
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s1): apiMerged(1, s1),
			pullCommitsPath(1):  apiListing(apiCommitOn(stackedA1, ":boom:! break the api", root)),
			pullCommitsPath(9): apiListing(
				apiCommitOn(stackedA1, ":boom:! break the api", root),
				apiCommitOn(d9, ":bug:~ fix a thing", stackedA1)),
		}))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		got := decodeJSON[verdict](t, stdout)
		if got.PR != "patch" || got.Pending != "major" || got.Level != "major" || strings.Contains(got.Body, "break the api") {
			t.Errorf("preview = pr %s, pending %s, level %s; want patch, major, major and no row for a1 — #1's major is pending, not this pull's\nbody: %s", got.PR, got.Pending, got.Level, got.Body)
		}
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{d9}) {
			t.Errorf("bump --pr 9 --json = exit %d commits %v, want 0 and d9 alone\nstderr: %s", code, shas, stderr)
		}
	})
}

// TestPullSideForkedAtTheTipAsksNothingMore prices the PR side: a pull cut from
// HEAD has no window to read back, so its listing is the one request, as it was.
// A checkout standing on the pull's own branch holds the listed commits in the
// window, and they are asked about neither: an open pull's commit names no
// merged pull, and asking would cost the pull's length on every push.
func TestPullSideForkedAtTheTipAsksNothingMore(t *testing.T) {
	t.Run("from the released branch", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		srv, asked := recordingWalkServer(t, map[string]string{
			pullCommitsPath(9): apiListing(apiCommitOn(stackedB1, ":bug:~ fix a thing", root)),
		})
		usePR(t, srv)
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--pr", "9")
		if code != 0 || stdout != "v0.1.1\n" {
			t.Fatalf("bump --pr 9 = exit %d stdout %q, want 0 / v0.1.1\nstderr: %s", code, stdout, stderr)
		}
		if want := []string{pullCommitsPath(9)}; !slices.Equal(*asked, want) {
			t.Errorf("requests = %v, want %v", *asked, want)
		}
	})
	t.Run("from the pull's own branch", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "-b", "feat")
		testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a thing")
		own := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		srv, asked := recordingWalkServer(t, map[string]string{
			pullCommitsPath(9): apiListing(apiCommitOn(own, ":bug:~ fix a thing", root)),
		})
		usePR(t, srv)
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{own}) {
			t.Fatalf("bump --pr 9 --json = exit %d commits %v, want 0 and the pull's own commit\nstderr: %s", code, shas, stderr)
		}
		if want := []string{pullCommitsPath(9)}; !slices.Equal(*asked, want) {
			t.Errorf("requests = %v, want %v — the pull's own commit was asked about", *asked, want)
		}
	})
}

// TestPullSideReadsAMergedPullAsItsOwn: --pr of a pull already merged. Its
// own merge point sits in the fork..HEAD window and resolves back to the pull
// being read, which is never "another pull" to itself; a pull stacked on it
// and merged after it lists the same shas, and they stay this pull's. The
// frozen packages live fire previews merged pulls (glyph-monorepo-test's e2e
// walk tier, #10 and #11 at 380db9d), and read as landed through another pull
// they previewed "moves nothing" (verified 2026-11-08 at 4d20780).
//
// bite-exempt: the pre-PR source reads no pull back, so it passes; mutation
// row pr-side-reads-a-merged-pull-as-another-pull is what proves it bites.
func TestPullSideReadsAMergedPullAsItsOwn(t *testing.T) {
	const d9 = "d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9"
	t.Run("squash-merged", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		s9 := squashCommit(t, dir, "Fix a thing", 9)
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s9): apiMerged(9, s9),
			pullCommitsPath(9):  apiListing(apiCommitOn(d9, ":bug:~ fix a thing", root)),
		}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--pr", "9")
		if code != 0 || stdout != "v0.1.1\n" || strings.Contains(stderr, "through another pull request") {
			t.Errorf("bump --pr 9 = exit %d %q, want 0 / v0.1.1 and no drop\nstderr: %s", code, stdout, stderr)
		}
		code, stdout, stderr = runGlyph(t, "notes", "--pr", "9")
		if code != 0 || !strings.Contains(stdout, "fix a thing (#9)") {
			t.Errorf("notes --pr 9 = exit %d, want 0 with the pull's own fix:\n%s\nstderr: %s", code, stdout, stderr)
		}
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		if got := decodeJSON[struct {
			PR string `json:"pr"`
		}](t, stdout); got.PR != "patch" {
			t.Errorf("preview --pr 9 = pr %q, want patch", got.PR)
		}
	})
	t.Run("merge-merged", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		mp := mergePR(t, dir, "akira-toriyama", 9, ":bug:~ fix a thing")
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(mp.Merge): apiMerged(9, mp.Merge),
			pullCommitsPath(9):        apiListing(apiCommitOn(mp.Branch[0], ":bug:~ fix a thing", root)),
		}))
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, mp.Branch) {
			t.Errorf("bump --pr 9 --json = exit %d commits %v, want 0 and the pull's own commit %v\nstderr: %s", code, shas, mp.Branch, stderr)
		}
	})
	t.Run("a pull stacked on it merged after it", func(t *testing.T) {
		dir, root := testutil.NewTaggedRepo(t)
		s1 := squashCommit(t, dir, "Reshape the api", 1)
		s2 := squashCommit(t, dir, "Follow up on the api", 2)
		a1 := apiCommitOn(stackedA1, ":boom:! break the api", root)
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s1): apiMerged(1, s1),
			pullCommitsPath(1):  apiListing(a1),
			commitPullsPath(s2): apiMerged(2, s2),
			pullCommitsPath(2):  apiListing(a1, apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA1)),
		}))
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "1")
		if code != 0 || !slices.Equal(shas, []string{stackedA1}) {
			t.Errorf("bump --pr 1 --json = exit %d commits %v, want 0 and a1 — #2 lists it, #1 landed it\nstderr: %s", code, shas, stderr)
		}
		code, shas, stderr = verdictSHAs(t, "bump", "--pr", "2")
		if code != 0 || !slices.Equal(shas, []string{stackedB1}) {
			t.Errorf("bump --pr 2 --json = exit %d commits %v, want 0 and b1 alone\nstderr: %s", code, shas, stderr)
		}
		if want := fmt.Sprintf("commit %.7s in pull request #2 already landed on the released branch as %.7s through another pull request", stackedA1, s1); !strings.Contains(stderr, want) {
			t.Errorf("the drop must name #1's merge point:\n  %s\nstderr: %s", want, stderr)
		}
	})
	t.Run("under [[packages]]", func(t *testing.T) {
		dir, root := packagesRepo(t)
		s10 := testutil.Touch(t, dir, "akira-toriyama", "Reshape the haiku (#10)", "haiku/a.go")
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s10):       apiMerged(10, s10),
			pullCommitsPath(10):        apiListing(apiCommitOn(stackedA1, ":sparkles:(haiku)^ reshape the haiku", root)),
			commitFilesPath(stackedA1): apiFiles("haiku/a.go"),
		}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "preview", "--pr", "10", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		got := decodeJSON[previewVerdictLines](t, stdout)
		if len(got.Packages) != 1 || got.Packages[0].Path != "haiku" || got.Packages[0].PR != "minor" || got.Packages[0].Next != "v0.2.0" {
			t.Errorf("preview --pr 10 packages = %+v, want haiku pr minor next v0.2.0 — the merged pull's own work", got.Packages)
		}
	})
}

// TestPullSideStaleBeyondTheCapReadsNothingBack prices the PR side's window.
// A pull forked more than walkCap walk-visible commits behind HEAD would cost
// a lookup per commit on every push, so nothing is read back: the listing is
// read whole, one request, and one warning says that a commit another pull
// landed may count as this pull's. At the cap the window is read, and the
// bot commits in it are neither counted against the cap nor asked about.
func TestPullSideStaleBeyondTheCapReadsNothingBack(t *testing.T) {
	const d9 = "d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9"
	// staleHistory lands #1 (a1) by squash under the fixture's tag, then humans
	// more commits by akira-toriyama and ten by a bot, and returns the shas the
	// read-back would ask about, oldest first.
	staleHistory := func(t *testing.T, humans int) (dir, root string, visible []string) {
		t.Helper()
		dir, root = testutil.NewTaggedRepo(t)
		visible = append(visible, squashCommit(t, dir, "Reshape the api", 1))
		for i := range humans {
			testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= human commit %d", i))
			visible = append(visible, testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD"))
		}
		for i := range 10 {
			testutil.Commit(t, dir, "dependabot[bot]", fmt.Sprintf(":robot:= chore(fleet): sync %d", i))
		}
		return dir, root, visible
	}
	listing := func(root string) string {
		return apiListing(apiCommitOn(stackedA1, ":boom:! break the api", root), apiCommitOn(d9, ":bug:~ fix a thing", stackedA1))
	}
	t.Run("past the cap", func(t *testing.T) {
		dir, root, visible := staleHistory(t, 200)
		srv, asked := recordingWalkServer(t, map[string]string{pullCommitsPath(9): listing(root)})
		usePR(t, srv)
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{stackedA1, d9}) {
			t.Fatalf("bump --pr 9 --json = exit %d commits %v, want 0 and the listing whole\nstderr: %s", code, shas, stderr)
		}
		if want := []string{pullCommitsPath(9)}; !slices.Equal(*asked, want) {
			t.Errorf("requests = %v, want the listing alone", *asked)
		}
		want := fmt.Sprintf("::warning::glyph: pull request #9 forked %d commits behind HEAD — reading back which of them another pull request landed would ask GitHub about each (the cap is 200), so the listing is read whole: a commit another pull request already landed on the released branch may be counted as this pull's own. Rebase the branch onto the released branch to narrow the window", len(visible))
		if !strings.Contains(stderr, want) {
			t.Errorf("the over-count must be said:\n  %s\nstderr: %s", want, stderr)
		}
	})
	t.Run("at the cap", func(t *testing.T) {
		dir, root, visible := staleHistory(t, 199)
		routes := map[string]string{
			pullCommitsPath(9):          listing(root),
			commitPullsPath(visible[0]): apiMerged(1, visible[0]),
			pullCommitsPath(1):          apiListing(apiCommitOn(stackedA1, ":boom:! break the api", root)),
		}
		for _, sha := range visible[1:] {
			routes[commitPullsPath(sha)] = `[]`
		}
		srv, asked := recordingWalkServer(t, routes)
		usePR(t, srv)
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{d9}) {
			t.Fatalf("bump --pr 9 --json = exit %d commits %v, want 0 and d9 alone — #1 landed a1\nstderr: %s", code, shas, stderr)
		}
		if strings.Contains(stderr, "the cap is") {
			t.Errorf("200 walk-visible commits sit at the cap, not past it:\n%s", stderr)
		}
		if len(*asked) != 2+len(visible) {
			t.Errorf("%d requests, want the listing, %d lookups and #1's listing:\n%v", len(*asked), len(visible), *asked)
		}
	})
}
