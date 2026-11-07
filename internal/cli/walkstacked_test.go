package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins what a stacked pull adds to a release (DESIGN §4, "A stacked
// pull lists what its base landed"; internal/walk/expand.go: placeStacked,
// readBack). GitHub goes on listing a base pull's commits in the pull stacked on
// it after the base is squash- or rebase-merged, and the listing's shas exist on
// no branch, so nothing but the base pull's own listing says they shipped. The
// stand-ins answer the two questions the walk asks: a merge point's merged pull,
// and a pull's listing.

// The listed commits of the fixtures: shas the checkouts do not hold, as a
// squash's or a rebase's listing reports them.
const (
	stackedA1 = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	stackedA2 = "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2"
	stackedB1 = "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"
	stackedX1 = "c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1"
)

// apiCommitOn renders a listed commit with the parents GitHub names for it,
// which is where the walk reads a listing's fork from. apiCommit's parent "p"
// names nothing a checkout holds, so its listings read nothing back.
func apiCommitOn(sha, message string, parents ...string) string {
	m, _ := json.Marshal(message)
	named := make([]string, len(parents))
	for i, p := range parents {
		named[i] = fmt.Sprintf(`{"sha":%q}`, p)
	}
	return fmt.Sprintf(`{"sha":%q,"commit":{"message":%s,"author":{"name":"akira-toriyama"}},"author":{"login":"akira-toriyama"},"parents":[%s]}`,
		sha, m, strings.Join(named, ","))
}

func apiListing(entries ...string) string { return `[` + strings.Join(entries, ",") + `]` }

func apiMerged(number int, mergeSHA string) string {
	return `[` + apiPullRef(number, "2026-07-20T00:00:00Z", mergeSHA) + `]`
}

// stackedSquashPair lands #1 and then #2, stacked on it, by squash, with v1.0.0
// cut between them. #1's subject is its title, as GitHub writes it over several
// commits, so no landed message says what #1 held. a1 is the only major and b1
// a patch, so the level alone tells a re-counted a1 from what #2 added.
func stackedSquashPair(t *testing.T) (dir string, routes map[string]string, s1 string) {
	t.Helper()
	dir, root := testutil.NewTaggedRepo(t)
	s1 = squashCommit(t, dir, "Reshape the api", 1)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	a1 := apiCommitOn(stackedA1, ":boom:! break the api", root)
	a2 := apiCommitOn(stackedA2, ":memo:= document the break", stackedA1)
	b1 := apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA2)
	return dir, map[string]string{
		commitPullsPath(s1): apiMerged(1, s1),
		pullCommitsPath(1):  apiListing(a1, a2),
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(a1, a2, b1),
	}, s1
}

// TestSinceTagStackedSquashDoesNotRecountWhatItsBaseReleased: #2 lists a1 and a2
// after #1 shipped them under v1.0.0. Read as #2's own they are v2.0.0 out of
// released work, exit 0, nothing on stderr. Every command that reads the walk
// takes the same answer: bump, notes, release's dry run, and preview's pending
// side.
func TestSinceTagStackedSquashDoesNotRecountWhatItsBaseReleased(t *testing.T) {
	t.Run("bump", func(t *testing.T) {
		dir, routes, s1 := stackedSquashPair(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "v1.0.1\n" {
			t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v1.0.1 — a1's major shipped under v1.0.0\nstderr: %s", code, stdout, stderr)
		}
		for _, sha := range []string{stackedA1, stackedA2} {
			want := fmt.Sprintf("::notice::glyph: commit %.7s in pull request #2 landed on the released branch as %.7s, which is outside v1.0.0..HEAD — it shipped under an earlier tag and is not counted again", sha, s1)
			if !strings.Contains(stderr, want) {
				t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
			}
		}
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
		if code != 0 || !slices.Equal(shas, []string{stackedB1}) {
			t.Errorf("bump --since-tag --json = exit %d commits %v, want 0 and b1 alone\nstderr: %s", code, shas, stderr)
		}
	})
	t.Run("notes", func(t *testing.T) {
		dir, routes, _ := stackedSquashPair(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 {
			t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stdout, "fix the follow-up (#2)") || strings.Contains(stdout, "break the api") || strings.Contains(stdout, "Breaking") {
			t.Errorf("the notes must hold what #2 added and nothing #1 shipped:\n%s", stdout)
		}
	})
	t.Run("release --dry-run", func(t *testing.T) {
		dir, routes, _ := stackedSquashPair(t)
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 || !strings.HasPrefix(stdout, "v1.0.1\n\n") {
			t.Fatalf("release --dry-run = exit %d stdout:\n%s\nwant 0 and v1.0.1\nstderr: %s", code, stdout, stderr)
		}
		if strings.Contains(stdout, "break the api") || strings.Contains(stdout, "Breaking") {
			t.Errorf("the draft's body re-announces what v1.0.0 shipped:\n%s", stdout)
		}
	})
	t.Run("preview's pending side", func(t *testing.T) {
		dir, routes, _ := stackedSquashPair(t)
		routes[pullCommitsPath(9)] = `[` + apiCommit("d9", "akira-toriyama", ":memo:= document it") + `]`
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		type verdict struct {
			Level   string `json:"level"`
			Next    string `json:"next"`
			Pending string `json:"pending"`
		}
		got := decodeJSON[verdict](t, stdout)
		if got.Pending != "patch" || got.Level != "patch" || got.Next != "v1.0.1" {
			t.Errorf("preview = pending %s, level %s, next %s; want patch, patch, v1.0.1 — a pending major out of released work", got.Pending, got.Level, got.Next)
		}
	})
}

// TestSinceTagStackedRebaseWithALandingBetween is the other arm: a rebase-merged
// base, cut under v1.0.0, and any commit landed before the stacked pull is
// rebase-merged. GitHub replays only #2's own commit, so the run ending at its
// merge point is b1' over x, and the listing [a1 b1] aligns with neither: left
// there, a1 counts as #2's own and both cite pre-rebase shas no branch holds.
// With what #1 landed out of the way, what is left aligns.
//
// With nothing landed between, the alignment reaches a1' on its own and the walk
// reads nothing back: the routes that would answer it are absent, and asking
// fails the test.
func TestSinceTagStackedRebaseWithALandingBetween(t *testing.T) {
	land := func(t *testing.T, between bool) (dir, a1Landed, b1Landed string, routes map[string]string) {
		t.Helper()
		dir, root := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "akira-toriyama", ":boom:! break the api")
		a1Landed = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
		routes = map[string]string{}
		if between {
			x := squashCommit(t, dir, "Note the break", 3)
			routes[commitPullsPath(x)] = apiMerged(3, x)
			routes[pullCommitsPath(3)] = apiListing(apiCommitOn(stackedX1, ":memo:= note the break", a1Landed))
			routes[commitPullsPath(a1Landed)] = apiMerged(1, a1Landed)
			routes[pullCommitsPath(1)] = apiListing(apiCommitOn(stackedA1, ":boom:! break the api", root))
		}
		testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix the follow-up")
		b1Landed = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		routes[commitPullsPath(b1Landed)] = apiMerged(2, b1Landed)
		routes[pullCommitsPath(2)] = apiListing(
			apiCommitOn(stackedA1, ":boom:! break the api", root),
			apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA1))
		return dir, a1Landed, b1Landed, routes
	}
	for name, between := range map[string]bool{"a pull landed between": true, "nothing landed between": false} {
		t.Run(name, func(t *testing.T) {
			dir, a1Landed, b1Landed, routes := land(t, between)
			usePR(t, walkServer(t, routes))
			t.Chdir(dir)
			code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 0 || stdout != "v1.0.1\n" {
				t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v1.0.1 — a1's major shipped under v1.0.0\nstderr: %s", code, stdout, stderr)
			}
			want := fmt.Sprintf("commit %.7s in pull request #2 landed on the released branch as %.7s, which is outside v1.0.0..HEAD", stackedA1, a1Landed)
			if !strings.Contains(stderr, want) {
				t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
			}
			code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
			if code != 0 || !slices.Contains(shas, b1Landed) || slices.Contains(shas, stackedB1) || slices.Contains(shas, stackedA1) {
				t.Errorf("bump --since-tag --json = exit %d commits %v, want b1 under its on-branch sha %.7s and no pre-rebase sha\nstderr: %s", code, shas, b1Landed, stderr)
			}
		})
	}
}

// TestSinceTagStackedRebaseUnderATagInsideItsBase: the tag sits inside #1's
// rebased run, so #1's merge point is in range and nothing behind the base
// resolves to it — only #1's own expansion in this walk says where #2's listed
// a1 and a2 landed. a1 is dropped at both sightings and a2 counted once, under
// its landing site, and the same holds with the whole stack in range, where #2's
// sighting would be a second note line for each.
func TestSinceTagStackedRebaseUnderATagInsideItsBase(t *testing.T) {
	land := func(t *testing.T, tagAtFirst bool) (a2Landed, b1Landed string) {
		t.Helper()
		dir, root := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "akira-toriyama", ":boom:! break the api")
		a1Landed := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		if tagAtFirst {
			testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
		}
		testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the break")
		a2Landed = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		x := squashCommit(t, dir, "Note the break", 3)
		testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix the follow-up")
		b1Landed = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		a1 := apiCommitOn(stackedA1, ":boom:! break the api", root)
		a2 := apiCommitOn(stackedA2, ":memo:= document the break", stackedA1)
		routes := map[string]string{
			commitPullsPath(a2Landed): apiMerged(1, a2Landed),
			pullCommitsPath(1):        apiListing(a1, a2),
			commitPullsPath(x):        apiMerged(3, x),
			pullCommitsPath(3):        apiListing(apiCommitOn(stackedX1, ":memo:= note the break", a2Landed)),
			commitPullsPath(b1Landed): apiMerged(2, b1Landed),
			pullCommitsPath(2):        apiListing(a1, a2, apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA2)),
		}
		// a1' stands aside for #1's merge point whichever side of the tag it is on.
		routes[commitPullsPath(a1Landed)] = apiMerged(1, a2Landed)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		return a2Landed, b1Landed
	}

	t.Run("the tag at the base's first commit", func(t *testing.T) {
		a2Landed, b1Landed := land(t, true)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "v1.0.1\n" {
			t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v1.0.1 — a1's major shipped under v1.0.0\nstderr: %s", code, stdout, stderr)
		}
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
		if code != 0 {
			t.Fatalf("bump --since-tag --json exited %d\nstderr: %s", code, stderr)
		}
		if want := []string{a2Landed, stackedX1, b1Landed}; !slices.Equal(shas, want) {
			t.Errorf("commits = %v, want %v — each once, under its landing site\nstderr: %s", shas, want, stderr)
		}
	})
	t.Run("the whole stack in range", func(t *testing.T) {
		land(t, false)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 {
			t.Fatalf("notes --since-tag exited %d\nstderr: %s", code, stderr)
		}
		if n := strings.Count(stdout, "break the api"); n != 1 {
			t.Errorf("a1 renders %d note line(s), want 1:\n%s", n, stdout)
		}
	})
}

// TestSinceTagRepeatedMessageOfAReleasedCommitStillCounts is the false drop a
// message would make: #5 forked behind v1.0.0 and carries a new commit whose
// message repeats what #1 released. The identity is the sha, so it is a new
// major, and the read-back that happened to read #1 changes nothing.
//
// bite-exempt: a guard on the read-back, which the pre-PR source does not have;
// mutation row walk-read-back-matches-a-released-message is what proves it bites.
func TestSinceTagRepeatedMessageOfAReleasedCommitStillCounts(t *testing.T) {
	dir, root := testutil.NewTaggedRepo(t)
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	s5 := squashCommit(t, dir, "Break it again", 5)
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(s1): apiMerged(1, s1),
		pullCommitsPath(1):  apiListing(apiCommitOn(stackedA1, ":boom:! break the api", root)),
		commitPullsPath(s5): apiMerged(5, s5),
		pullCommitsPath(5):  apiListing(apiCommitOn(stackedB1, ":boom:! break the api", root)),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "v2.0.0\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v2.0.0 — a new commit repeating a released message is new work\nstderr: %s", code, stdout, stderr)
	}
	if strings.Contains(stderr, "shipped under an earlier tag") {
		t.Errorf("a new commit was announced as released:\n%s", stderr)
	}
}

// TestSinceTagStackedPullLandedFirstReleasedItsBase: the order the fleet's real
// pair took (.github#171 merged before #170, which it was stacked on): the
// stacked pull's squash carried its base's commits, so the base's own later
// landing adds nothing. a1 shipped under v1.0.0 with #2, and #1 has no commit
// left to count.
func TestSinceTagStackedPullLandedFirstReleasedItsBase(t *testing.T) {
	dir, root := testutil.NewTaggedRepo(t)
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	a1 := apiCommitOn(stackedA1, ":boom:! break the api", root)
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(a1, apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA1)),
		commitPullsPath(s1): apiMerged(1, s1),
		pullCommitsPath(1):  apiListing(a1),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 || stdout != "" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 1 and nothing — a1 shipped with #2's squash\nstderr: %s", code, stdout, stderr)
	}
}

// TestSinceTagReadBackAsksOnlyAboutWhatTheForkMissed prices the read-back: of
// everything behind v1.0.0 it asks only about the commits the pull forked over,
// less an excluded author's, once however many pulls forked behind the base —
// walkServer fails on the routes left out (the commit under the fork, the bot's
// push), and the recording counts the one it serves. A pull forked in range, or
// at the base itself, reads nothing back.
func TestSinceTagReadBackAsksOnlyAboutWhatTheForkMissed(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= land something under the fork")
	fork := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	testutil.Commit(t, dir, "dependabot[bot]", ":robot:= sync a file")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	s3 := squashCommit(t, dir, "Follow up again", 3)
	s4 := squashCommit(t, dir, "Forked at the tag", 4)
	s5 := squashCommit(t, dir, "Forked in range", 5)
	srv, asked := recordingWalkServer(t, map[string]string{
		commitPullsPath(s1): apiMerged(1, s1),
		pullCommitsPath(1):  apiListing(apiCommitOn(stackedA1, ":sparkles:^ add a thing", fork)),
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(apiCommitOn(stackedA1, ":sparkles:^ add a thing", fork), apiCommitOn(stackedB1, ":bug:~ fix a thing", stackedA1)),
		commitPullsPath(s3): apiMerged(3, s3),
		pullCommitsPath(3):  apiListing(apiCommitOn(stackedX1, ":bug:~ fix another thing", fork)),
		commitPullsPath(s4): apiMerged(4, s4),
		pullCommitsPath(4):  apiListing(apiCommitOn("d4", ":memo:= forked at the tag", base)),
		commitPullsPath(s5): apiMerged(5, s5),
		pullCommitsPath(5):  apiListing(apiCommitOn("d5", ":memo:= forked in range", s2)),
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v1.0.0")
	if code != 0 || stdout != "v1.0.1\n" {
		t.Fatalf("bump = exit %d stdout %q, want 0 / v1.0.1\nstderr: %s", code, stdout, stderr)
	}
	want := []string{
		commitPullsPath(s2), pullCommitsPath(2), commitPullsPath(s1), pullCommitsPath(1),
		commitPullsPath(s3), pullCommitsPath(3),
		commitPullsPath(s4), pullCommitsPath(4),
		commitPullsPath(s5), pullCommitsPath(5),
	}
	if !slices.Equal(*asked, want) {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(*asked, "\n"), strings.Join(want, "\n"))
	}
}

// TestSinceTagReadBackOfAnUnknownCommitCountsAsBefore: a commit behind the base
// GitHub does not know names no pull, which leaves the listing as the pull alone
// governs it and fails nothing — the read-back is no read of the range, and the
// walk is complete without it.
//
// bite-exempt: a guard on the read-back, which the pre-PR source does not have;
// mutation row walk-read-back-dies-on-an-unknown-commit is what proves it bites.
func TestSinceTagReadBackOfAnUnknownCommitCountsAsBefore(t *testing.T) {
	dir, root := testutil.NewTaggedRepo(t)
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	usePR(t, dryServer(t, map[string]string{
		commitPullsPath(s1): apiUnknownSHA,
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(apiCommitOn(stackedB1, ":bug:~ fix the follow-up", root)),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 || !strings.HasPrefix(stdout, "v1.0.1\n\n") {
		t.Fatalf("release --dry-run = exit %d stdout:\n%s\nwant 0 and v1.0.1: an unread pull behind the base is no shortfall of the range\nstderr: %s", code, stdout, stderr)
	}
}

// TestSinceTagForkNoCheckoutHoldsReadsNothingBack: a listing can fork from a
// commit this checkout never fetched, a deleted branch's. Nothing behind the base
// can then be told from what the fork already held, so nothing is read back, and
// the git error `<fork>..<base>` would raise is no failure of the walk.
//
// bite-exempt: a guard on the read-back, which the pre-PR source does not have;
// mutation row walk-read-back-fails-on-a-fork-no-checkout-holds is what proves it
// bites.
func TestSinceTagForkNoCheckoutHoldsReadsNothingBack(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	const gone = "dededededededededededededededededededede"
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(apiCommitOn(stackedB1, ":bug:~ fix the follow-up", gone)),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "v1.0.1\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v1.0.1 and no request about %.7s\nstderr: %s", code, stdout, s1, stderr)
	}
}

// TestSinceTagWholeHistoryHasNoBaseToReadBehind: with no tag the range is the whole
// history and nothing is released, so even a fork the checkout holds off HEAD's
// history reads nothing back. Read against HEAD, the walk would take its own range
// for released work: #1 resolved twice, and c1 cited under #1's merge point.
//
// bite-exempt: a guard on the read-back, which the pre-PR source does not have;
// mutation row walk-read-back-takes-the-range-for-released-work is what proves it
// bites.
func TestSinceTagWholeHistoryHasNoBaseToReadBehind(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "-b", "side")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= a commit off the released branch")
	side := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "main")
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	srv, asked := recordingWalkServer(t, map[string]string{
		commitPullsPath(root): `[]`,
		commitPullsPath(s1):   apiMerged(1, s1),
		pullCommitsPath(1):    apiListing(apiCommitOn(stackedX1, ":bug:~ fix a thing", side)),
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
	if code != 0 || !slices.Equal(shas, []string{root, stackedX1}) {
		t.Fatalf("bump --since-tag --json = exit %d commits %v, want 0 and [%.7s %.7s]\nstderr: %s", code, shas, root, stackedX1, stderr)
	}
	if want := []string{commitPullsPath(root), commitPullsPath(s1), pullCommitsPath(1)}; !slices.Equal(*asked, want) {
		t.Errorf("requests = %v, want %v", *asked, want)
	}
}

// TestSinceTagEscapeAtTheBasePullClearsItsStack: a refused commit #1 and #2 both
// list. A tag at #1's merge point is a base that holds the commit, so #2 listing
// it again wedges nothing.
func TestSinceTagEscapeAtTheBasePullClearsItsStack(t *testing.T) {
	dir, root := testutil.NewTaggedRepo(t)
	s1 := squashCommit(t, dir, "Reshape the api", 1)
	s2 := squashCommit(t, dir, "Follow up on the api", 2)
	x := apiCommitOn(stackedA1, "no gitmoji leads this subject", root)
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(s1): apiMerged(1, s1),
		pullCommitsPath(1):  apiListing(x),
		commitPullsPath(s2): apiMerged(2, s2),
		pullCommitsPath(2):  apiListing(x, apiCommitOn(stackedB1, ":bug:~ fix the follow-up", stackedA1)),
	}))
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "bump", "--since-tag"); code != 3 {
		t.Fatalf("bump --since-tag exited %d, want 3 before the escape\nstderr: %s", code, stderr)
	}
	testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0", s1)
	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "v1.0.1\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q after a tag at #1's merge point, want 0 / v1.0.1\nstderr: %s", code, stdout, stderr)
	}
}

// TestSinceTagPackagesStackedPullLeavesWhatEveryLineReleased: under [[packages]]
// the read-back runs from the union's base, so what it finds is released on
// every line. #2's a1 is dropped before its files are asked for (no route
// answers them), and #2 steps haiku by its own fix alone.
func TestSinceTagPackagesStackedPullLeavesWhatEveryLineReleased(t *testing.T) {
	dir, root := packagesRepo(t)
	s1 := testutil.Touch(t, dir, "akira-toriyama", "Reshape the haiku (#1)", "haiku/a.go")
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.2.0")
	s2 := testutil.Touch(t, dir, "akira-toriyama", "Follow up on the haiku (#2)", "haiku/b.go")
	a1 := apiCommitOn(stackedA1, ":sparkles:(haiku)^ reshape the haiku", root)
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(s1):        apiMerged(1, s1),
		pullCommitsPath(1):         apiListing(a1),
		commitPullsPath(s2):        apiMerged(2, s2),
		pullCommitsPath(2):         apiListing(a1, apiCommitOn(stackedB1, ":bug:(haiku)~ fix the haiku", stackedA1)),
		commitFilesPath(stackedB1): apiFiles("haiku/b.go"),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "haiku/v0.2.1\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / haiku/v0.2.1 — a1 shipped under haiku/v0.2.0\nstderr: %s", code, stdout, stderr)
	}
}
