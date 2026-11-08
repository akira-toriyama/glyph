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
