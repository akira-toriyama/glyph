package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins what an excluded author's commit on the released branch
// resolves to (walk.resolve; DESIGN §4, "The author excludes a message, never
// a merge point"): it is asked about like any other, and expands the pull it
// merged exactly when that pull lists a commit by someone else. GitHub writes
// a squash commit under the name of whoever opened the pull and a merge
// commit under whoever pressed the button, so the author of a merge point
// says nothing of who wrote the commits inside (t-saky).

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	return testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// mixedBotSquash lands dependabot's pull #5 the way GitHub squashes it — the
// bot's name on the squash commit, the pull's title its subject — with a
// human's fix pushed onto the bot's branch beside the bump in the listing.
func mixedBotSquash(t *testing.T) (dir string, routes map[string]string) {
	t.Helper()
	dir, _ = testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "dependabot[bot]", "Bump foo (#5)")
	s5 := headSHA(t, dir)
	return dir, map[string]string{
		commitPullsPath(s5): apiMerged(5, s5),
		pullCommitsPath(5): apiListing(
			apiCommit("d1", "dependabot[bot]", "Bump foo"),
			apiCommit("h1", "akira-toriyama", ":bug:~ fix the build after the foo bump")),
	}
}

// botPressedMerge lands #7, a human's fix, behind a merge commit an automation
// pressed the button for: the branch commit names #7 as merged at the merge
// commit, as GitHub answers for it.
func botPressedMerge(t *testing.T) (dir string, routes map[string]string, mp mergePoint) {
	t.Helper()
	dir, _ = testutil.NewTaggedRepo(t)
	mp = mergeInto(t, dir, "akira-toriyama", "dependabot[bot]", prTopic(7), prMergeSubject(7), ":bug:~ fix a crash")
	return dir, map[string]string{
		commitPullsPath(mp.Branch[0]): apiMerged(7, mp.Merge),
		commitPullsPath(mp.Merge):     apiMerged(7, mp.Merge),
		pullCommitsPath(7):            apiListing(apiCommit(mp.Branch[0], "akira-toriyama", ":bug:~ fix a crash")),
	}, mp
}

// TestSinceTagExcludedAuthorsSquashExpandsAPullSomeoneElseCommittedTo: a fix a
// human pushed onto dependabot's branch lands under the bot's name, and bump,
// notes and release all read it. Unresolved, the squash was one bot commit:
// bump answered "0 commit(s) participate" at exit 1 with no warning, while the
// notes credited the human under Dependencies (t-saky). The bot's own commit
// in the listing stays excluded: it moves nothing and is judged by no pattern.
func TestSinceTagExcludedAuthorsSquashExpandsAPullSomeoneElseCommittedTo(t *testing.T) {
	t.Run("bump", func(t *testing.T) {
		dir, routes := mixedBotSquash(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "v0.1.1\n" {
			t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v0.1.1 — the fix inside #5 is a patch\nstderr: %s", code, stdout, stderr)
		}
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
		if code != 0 || !slices.Equal(shas, []string{"h1"}) {
			t.Errorf("bump --since-tag --json = exit %d commits %v, want 0 and the fix alone — the bot's bump moves nothing\nstderr: %s", code, shas, stderr)
		}
	})
	t.Run("notes", func(t *testing.T) {
		dir, routes := mixedBotSquash(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 {
			t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stdout, "fix the build after the foo bump (#5)") || !strings.Contains(stdout, "Bump foo (#5)") {
			t.Errorf("the notes must hold the fix and the bump, each under #5:\n%s", stdout)
		}
	})
	t.Run("release --dry-run", func(t *testing.T) {
		dir, routes := mixedBotSquash(t)
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 || !strings.HasPrefix(stdout, "v0.1.1\n\n") || !strings.Contains(stdout, "fix the build after the foo bump (#5)") {
			t.Fatalf("release --dry-run = exit %d stdout:\n%s\nwant 0, v0.1.1 and the fix in the body\nstderr: %s", code, stdout, stderr)
		}
	})
}

// TestSinceTagExcludedAuthorsMergeCommitResolvesItsPull: a merge commit an
// automation pressed the button for is the merge point of the pull it merged,
// and resolves like one a human pressed. Skipped before resolution, the pull's
// commits stood aside for a merge point nothing asked about: bump answered
// none under a NOT-counted warning and release refused at 4 on every run
// (t-saky, t-b7t2).
func TestSinceTagExcludedAuthorsMergeCommitResolvesItsPull(t *testing.T) {
	t.Run("bump", func(t *testing.T) {
		dir, routes, mp := botPressedMerge(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
		if code != 0 || !slices.Equal(shas, mp.Branch) {
			t.Fatalf("bump --since-tag --json = exit %d commits %v, want 0 and #7's commit\nstderr: %s", code, shas, stderr)
		}
		if strings.Contains(stderr, "NOT counted") || strings.Contains(stderr, "::warning::") {
			t.Errorf("a merge point that resolves leaves nothing to warn about:\n%s", stderr)
		}
	})
	t.Run("notes", func(t *testing.T) {
		dir, routes, _ := botPressedMerge(t)
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 || !strings.Contains(stdout, "fix a crash (#7)") {
			t.Fatalf("notes --since-tag = exit %d, want 0 and the fix under #7:\n%s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("release --dry-run", func(t *testing.T) {
		dir, routes, _ := botPressedMerge(t)
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 || !strings.HasPrefix(stdout, "v0.1.1\n\n") {
			t.Fatalf("release --dry-run = exit %d stdout:\n%s\nwant 0 and v0.1.1 — the walk read the range whole\nstderr: %s", code, stdout, stderr)
		}
	})
}

// TestSinceTagExcludedAuthorsOwnPullIsNotExpanded prices the rule and pins its
// other half. A bot's commit costs the lookup every commit costs; one that
// resolves costs its pull's listing too, and a listing of excluded authors
// alone is not expanded: the squash stays one commit under its own sha,
// out of the fold and in the notes as it always was. A bot's direct push —
// the fleet's daily sync — resolves to no pull at one request.
func TestSinceTagExcludedAuthorsOwnPullIsNotExpanded(t *testing.T) {
	// A bot's pull a bot merged with the button: its branch commit names the
	// pull as merged at the merge commit, and standing aside for a merge
	// point the walk does not expand would lose it as a pull nothing
	// expanded — release at exit 4 on every run. An excluded author's commit
	// stands aside for nothing.
	t.Run("merged with the button", func(t *testing.T) {
		dir, _ := testutil.NewTaggedRepo(t)
		mp := mergeInto(t, dir, "dependabot[bot]", "dependabot[bot]", prTopic(5), prMergeSubject(5), "Bump foo")
		usePR(t, dryServer(t, map[string]string{
			commitPullsPath(mp.Branch[0]): apiMerged(5, mp.Merge),
			commitPullsPath(mp.Merge):     apiMerged(5, mp.Merge),
			pullCommitsPath(5):            apiListing(apiCommit(mp.Branch[0], "dependabot[bot]", "Bump foo")),
		}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 1 || stdout != "" || strings.Contains(stderr, "::warning::") {
			t.Fatalf("release --dry-run = exit %d stdout %q, want 1 and no warning — the walk read the range, and nothing in it moves a version\nstderr: %s", code, stdout, stderr)
		}
		code, stdout, stderr = runGlyph(t, "notes", "--since-tag")
		if code != 0 || strings.Count(stdout, "Bump foo") != 1 {
			t.Errorf("notes --since-tag = exit %d, want 0 and the bump once:\n%s\nstderr: %s", code, stdout, stderr)
		}
	})

	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "dependabot[bot]", ":robot:= chore(fleet): sync a file")
	push := headSHA(t, dir)
	testutil.Commit(t, dir, "dependabot[bot]", "Bump foo (#5)")
	s5 := headSHA(t, dir)
	srv, asked := recordingWalkServer(t, map[string]string{
		commitPullsPath(push): `[]`,
		commitPullsPath(s5):   apiMerged(5, s5),
		pullCommitsPath(5):    apiListing(apiCommit("d1", "dependabot[bot]", "Bump foo"), apiCommit("d2", "dependabot[bot]", "Bump foo again")),
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "0 commit(s) participate") {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 1 and nothing — a bot's own pull moves no version\nstderr: %s", code, stdout, stderr)
	}
	if want := []string{commitPullsPath(push), commitPullsPath(s5), pullCommitsPath(5)}; !slices.Equal(*asked, want) {
		t.Errorf("requests =\n%s\nwant one lookup per commit and the one listing:\n%s", strings.Join(*asked, "\n"), strings.Join(want, "\n"))
	}
	code, stdout, stderr = runGlyph(t, "notes", "--since-tag")
	if code != 0 || !strings.Contains(stdout, "Bump foo (#5)") || strings.Contains(stdout, "Bump foo again") {
		t.Errorf("notes --since-tag = exit %d, want 0 and the squash commit itself, never the listing behind it:\n%s\nstderr: %s", code, stdout, stderr)
	}
}

// TestSinceTagExcludedAuthorsCommitUnknownToGitHubIsSaid: under the API's lag
// a bot's commit may be the squash of a pull someone else committed to, and
// its own message would never say so. A single-parent one joins the walk
// with a warning, soft like every lag fallback; a merge commit is a merge
// point the walk could not resolve, whoever pressed the button, and the walk
// is incomplete.
func TestSinceTagExcludedAuthorsCommitUnknownToGitHubIsSaid(t *testing.T) {
	t.Run("a squash", func(t *testing.T) {
		dir, _ := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "dependabot[bot]", "Bump foo (#5)")
		s5 := headSHA(t, dir)
		usePR(t, walkServer(t, map[string]string{commitPullsPath(s5): apiUnknownSHA}))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "bump", "--since-tag")
		want := "::warning::glyph: commit " + s5[:7] + ", an exclude_authors author's, is not known to GitHub yet (API lag), so a pull request it may have merged could not be resolved"
		if code != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("bump --since-tag = exit %d, want 1 and the lag said:\n  %s\nstderr: %s", code, want, stderr)
		}
	})
	t.Run("a merge commit", func(t *testing.T) {
		dir, routes, mp := botPressedMerge(t)
		routes[commitPullsPath(mp.Merge)] = apiUnknownSHA
		delete(routes, pullCommitsPath(7))
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 4 || stdout != "" || !strings.Contains(stderr, mp.Merge[:7]) || !strings.Contains(stderr, "re-run the release") {
			t.Fatalf("release --dry-run = exit %d stdout %q, want 4 naming the merge commit and the re-run that clears a lag\nstderr: %s", code, stdout, stderr)
		}
	})
}

// TestPullSideReadsAnExcludedAuthorsMergePointAsItsOwn: #9, renovate's pull
// with a human's fix on it, squash-merged under the bot's name; #10, stacked
// on it, merged after. Read by number, #9 keeps the fix: the read-back
// resolves #9's own merge point first, as the walk would. With a gate of its
// own the read-back skipped the bot's squash, recorded the fix under #10, and
// dropped it from #9 as another pull's work.
//
// bite-exempt: the pre-PR source reads no pull back, so it passes; mutation
// row walk-skips-an-excluded-authors-merge-point is what bites.
func TestPullSideReadsAnExcludedAuthorsMergePointAsItsOwn(t *testing.T) {
	const h1 = "d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9"
	fixture := func(t *testing.T) {
		t.Helper()
		dir, root := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "renovate[bot]", "Update the lock file (#9)")
		s9 := headSHA(t, dir)
		s10 := squashCommit(t, dir, "Follow up", 10)
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s9):  apiMerged(9, s9),
			pullCommitsPath(9):   apiListing(apiCommitOn(h1, ":bug:~ fix the lock", root)),
			commitPullsPath(s10): apiMerged(10, s10),
			pullCommitsPath(10):  apiListing(apiCommitOn(h1, ":bug:~ fix the lock", root), apiCommitOn(stackedB1, ":memo:= document the lock", h1)),
		}))
		t.Chdir(dir)
	}
	t.Run("bump --pr", func(t *testing.T) {
		fixture(t)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "9")
		if code != 0 || !slices.Equal(shas, []string{h1}) {
			t.Fatalf("bump --pr 9 --json = exit %d commits %v, want 0 and the fix — #9 landed it, #10 only carried it\nstderr: %s", code, shas, stderr)
		}
		if strings.Contains(stderr, "another pull request") {
			t.Errorf("#9 is not another pull to its own commit:\n%s", stderr)
		}
	})
	t.Run("notes --pr", func(t *testing.T) {
		fixture(t)
		code, stdout, stderr := runGlyph(t, "notes", "--pr", "9")
		if code != 0 || !strings.Contains(stdout, "fix the lock") {
			t.Fatalf("notes --pr 9 = exit %d, want 0 and the fix:\n%s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("preview --pr", func(t *testing.T) {
		fixture(t)
		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview --pr 9 exited %d, want 0\nstderr: %s", code, stderr)
		}
		type verdict struct {
			PR      string `json:"pr"`
			Pending string `json:"pending"`
		}
		if got := decodeJSON[verdict](t, stdout); got.PR != "patch" || got.Pending != "patch" {
			t.Errorf("preview --pr 9 = pr %s, pending %s; want patch, patch — the fix is #9's and is pending once", got.PR, got.Pending)
		}
	})
	t.Run("the stacked pull", func(t *testing.T) {
		fixture(t)
		code, shas, stderr := verdictSHAs(t, "bump", "--pr", "10")
		if code != 1 || len(shas) != 0 || !strings.Contains(stderr, "already landed on the released branch") {
			t.Errorf("bump --pr 10 --json = exit %d commits %v, want 1 — #10 adds a = alone, the fix is #9's\nstderr: %s", code, shas, stderr)
		}
	})
}

const botD1 = "d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"

// botOwnPullUnderAStack lands #9, dependabot's own pull — its listing holds
// the bot's commit alone, the routine shape — by squash under the bot's name,
// and then #10, stacked on it and merged after it, which lists the bot's
// commit beside its own fix. v1.0.0 is cut at #9's merge point when
// tagBetween.
func botOwnPullUnderAStack(t *testing.T, tagBetween bool) (s9 string) {
	t.Helper()
	dir, root := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "dependabot[bot]", "Bump foo from 1 to 2 (#9)")
	s9 = headSHA(t, dir)
	if tagBetween {
		testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
	}
	s10 := squashCommit(t, dir, "Follow up", 10)
	bump := apiCommitByOn(botD1, "dependabot[bot]", "Bump foo from 1 to 2", root)
	usePR(t, dryServer(t, map[string]string{
		commitPullsPath(s9):  apiMerged(9, s9),
		pullCommitsPath(9):   apiListing(bump),
		commitPullsPath(s10): apiMerged(10, s10),
		pullCommitsPath(10):  apiListing(bump, apiCommitOn(stackedB1, ":bug:~ fix the foo bump", botD1)),
	}))
	t.Chdir(dir)
	return s9
}

// TestPullSideReadsAnExcludedAuthorsOwnPullAsItsOwn: the pull the walk never
// expands — a bot's own, every listed commit an excluded author's — still
// landed what it lists. Read by number once a pull stacked on it has merged,
// #9 keeps its bump: the read-back lands #9's listing at #9's merge point
// before it reaches #10's. Read as no pull at all, the bump was recorded
// under #10 and `notes --pr 9` answered "no release notes" at exit 1 under a
// notice that the commit landed through another pull request.
func TestPullSideReadsAnExcludedAuthorsOwnPullAsItsOwn(t *testing.T) {
	for _, cmd := range []string{"notes", "bump", "preview"} {
		t.Run(cmd+" --pr 9", func(t *testing.T) {
			botOwnPullUnderAStack(t, false)
			code, stdout, stderr := runGlyph(t, cmd, "--pr", "9")
			if strings.Contains(stderr, "another pull request") {
				t.Errorf("#9 is not another pull to its own commit:\n%s", stderr)
			}
			if cmd == "notes" && (code != 0 || !strings.Contains(stdout, "Bump foo from 1 to 2 (#9)")) {
				t.Errorf("notes --pr 9 = exit %d, want 0 and the bump under #9:\n%s\nstderr: %s", code, stdout, stderr)
			}
		})
	}
	t.Run("the stacked pull", func(t *testing.T) {
		s9 := botOwnPullUnderAStack(t, false)
		code, stdout, stderr := runGlyph(t, "notes", "--pr", "10")
		if code != 0 || !strings.Contains(stdout, "fix the foo bump") || strings.Contains(stdout, "Bump foo") {
			t.Errorf("notes --pr 10 = exit %d, want 0 and #10's fix alone:\n%s\nstderr: %s", code, stdout, stderr)
		}
		if want := "commit " + botD1[:7] + " in pull request #10 already landed on the released branch as " + s9[:7] + " through another pull request"; !strings.Contains(stderr, want) {
			t.Errorf("the drop must name #9's merge point:\n  %s\nstderr: %s", want, stderr)
		}
	})
}

// TestSinceTagStackedOverAnExcludedAuthorsOwnPullShowsItsCommitOnce: the
// release walk's face of the same landing. With a tag at #9's merge point the
// read-back behind the base lands the bump there and #10 drops it as shipped;
// with no tag between them the walk meets #9's merge point itself, does not
// expand it, and lands its listing all the same, so #10's sighting of the bump
// is the second and the notes show it once, under #9.
func TestSinceTagStackedOverAnExcludedAuthorsOwnPullShowsItsCommitOnce(t *testing.T) {
	t.Run("a tag between", func(t *testing.T) {
		s9 := botOwnPullUnderAStack(t, true)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 || !strings.Contains(stdout, "fix the foo bump (#10)") || strings.Contains(stdout, "Bump foo") {
			t.Fatalf("notes --since-tag = exit %d, want 0 and #10's fix alone — the bump shipped under v1.0.0:\n%s\nstderr: %s", code, stdout, stderr)
		}
		if want := "commit " + botD1[:7] + " in pull request #10 landed on the released branch as " + s9[:7] + ", which is outside v1.0.0..HEAD"; !strings.Contains(stderr, want) {
			t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
		}
	})
	t.Run("no tag between", func(t *testing.T) {
		botOwnPullUnderAStack(t, false)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 || strings.Count(stdout, "Bump foo") != 1 || !strings.Contains(stdout, "Bump foo from 1 to 2 (#9)") || !strings.Contains(stdout, "fix the foo bump (#10)") {
			t.Fatalf("notes --since-tag = exit %d, want 0, the bump once under #9 and the fix under #10:\n%s\nstderr: %s", code, stdout, stderr)
		}
		code, stdout, stderr = runGlyph(t, "release", "--dry-run")
		if code != 0 || !strings.HasPrefix(stdout, "v0.1.1\n\n") || strings.Count(stdout, "Bump foo") != 1 {
			t.Errorf("release --dry-run = exit %d, want 0, v0.1.1 and the bump once:\n%s\nstderr: %s", code, stdout, stderr)
		}
	})
}

// TestSinceTagExcludedAuthorsCappedListingIsTruncatedNotLost: a bot's pull
// GitHub lists at its cap of 250, every listed commit an excluded author's,
// cannot say that nobody else committed past the cap. It expands and is the
// truncated listing any capped pull is, under the clause release refuses a
// truncated listing with. Unexpanded, a merge commit a bot pressed over a
// human's commit lost the pull on every run — the human's commit stood aside
// for a merge point that resolved to nothing, under a warning that blamed the
// API's lag and asked for a re-run no re-run clears (t-b7t2) — and the squash
// shape answered "no release" at exit 1 with no word of the cap.
func TestSinceTagExcludedAuthorsCappedListingIsTruncatedNotLost(t *testing.T) {
	entries := make([]string, 0, 250)
	for k := range 250 {
		entries = append(entries, apiCommit(fmt.Sprintf("%040x", 0xdd0000+k), "dependabot[bot]", fmt.Sprintf("Bump foo %d", k)))
	}
	capped := apiListing(entries...)
	const truncated = "merged pull request #5 returned the maximum 250 commits, so GitHub truncated its listing and the rest could not be read"
	// The listing is capped again on every run, so the refusal's next step is
	// the wedge escape, never the re-run a lag gets (mutation row
	// truncated-listing-remedy-is-a-rerun).
	const escape = "could not read; cut a tag at or past what the walk cannot read, merged pull request #5's merge point"
	wedged := func(t *testing.T, stderr string) {
		t.Helper()
		if !strings.Contains(stderr, escape) || strings.Contains(stderr, "re-run") {
			t.Errorf("the refusal's next step must be the wedge escape and no re-run:\n  %s\nstderr: %s", escape, stderr)
		}
	}
	t.Run("a merge commit the bot pressed", func(t *testing.T) {
		dir, _ := testutil.NewTaggedRepo(t)
		mp := mergeInto(t, dir, "akira-toriyama", "dependabot[bot]", prTopic(5), prMergeSubject(5), ":bug:~ fix the build")
		usePR(t, dryServer(t, map[string]string{
			commitPullsPath(mp.Branch[0]): apiMerged(5, mp.Merge),
			commitPullsPath(mp.Merge):     apiMerged(5, mp.Merge),
			pullCommitsPath(5):            capped,
		}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 4 || stdout != "" || !strings.Contains(stderr, truncated) {
			t.Fatalf("release --dry-run = exit %d stdout %q, want 4 naming the truncation:\n  %s\nstderr: %s", code, stdout, truncated, stderr)
		}
		if strings.Contains(stderr, "nothing in the range resolved to it") || strings.Contains(stderr, "stood aside for a merge point nothing resolved") {
			t.Errorf("the pull resolved; it is truncated, not lost:\n%s", stderr)
		}
		wedged(t, stderr)
	})
	t.Run("a squash under the bot's name", func(t *testing.T) {
		dir, _ := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "dependabot[bot]", "Bump foo (#5)")
		s5 := headSHA(t, dir)
		usePR(t, dryServer(t, map[string]string{
			commitPullsPath(s5): apiMerged(5, s5),
			pullCommitsPath(5):  capped,
		}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 4 || stdout != "" || !strings.Contains(stderr, truncated) {
			t.Fatalf("release --dry-run = exit %d stdout %q, want 4 naming the truncation, as a human's squash of the same listing answers:\n  %s\nstderr: %s", code, stdout, truncated, stderr)
		}
		wedged(t, stderr)
	})
}

// TestReleaseExcludedAuthorsCommitsAllUnknownIsRefused: an excluded author's
// commit is asked about like any other, so it is one of the commits GitHub
// can fail to know. A range holding such commits alone, every one answered
// 422, is the all-unknown walk any range is: release refuses at 4 and bump
// reports — the squash may carry someone else's fix, and the walk read none
// of it. Beside a commit GitHub knows, the same squash is one lagging commit:
// warned about and joined, the release going ahead.
func TestReleaseExcludedAuthorsCommitsAllUnknownIsRefused(t *testing.T) {
	fixture := func(t *testing.T, known bool) (s5 string) {
		t.Helper()
		dir, _ := testutil.NewTaggedRepo(t)
		routes := map[string]string{}
		if known {
			s4 := squashCommit(t, dir, "Fix a thing", 4)
			routes[commitPullsPath(s4)] = apiMerged(4, s4)
			routes[pullCommitsPath(4)] = apiListing(apiCommit("h4", "akira-toriyama", ":bug:~ fix a thing"))
		}
		testutil.Commit(t, dir, "dependabot[bot]", "Bump foo (#5)")
		s5 = headSHA(t, dir)
		routes[commitPullsPath(s5)] = apiUnknownSHA
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		return s5
	}
	t.Run("alone", func(t *testing.T) {
		fixture(t, false)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if want := "every commit in it was unknown to akira-toriyama/glyph"; code != 4 || stdout != "" || !strings.Contains(stderr, want) {
			t.Fatalf("release --dry-run = exit %d stdout %q, want 4 and %q\nstderr: %s", code, stdout, want, stderr)
		}
		code, stdout, stderr = runGlyph(t, "bump", "--since-tag")
		if code != 1 || stdout != "" || !strings.Contains(stderr, "all 1 commit(s) in v0.1.0..HEAD were unknown") {
			t.Errorf("bump --since-tag = exit %d stdout %q, want 1 under the all-unknown warning\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("beside a commit GitHub knows", func(t *testing.T) {
		s5 := fixture(t, true)
		code, stdout, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 || !strings.HasPrefix(stdout, "v0.1.1\n\n") {
			t.Fatalf("release --dry-run = exit %d stdout:\n%s\nwant 0 and v0.1.1\nstderr: %s", code, stdout, stderr)
		}
		if want := "commit " + s5[:7] + ", an exclude_authors author's, is not known to GitHub yet (API lag)"; !strings.Contains(stderr, want) {
			t.Errorf("the lagging commit must be said:\n  %s\nstderr: %s", want, stderr)
		}
	})
}

// TestSinceTagStackedOverAnExcludedAuthorsPullCountsItsCommitsOnce: the
// release walk's face of the same reader. #10 is stacked on #9, whose squash
// a bot authored. With a tag at #9's merge point, the read-back behind the
// base resolves it and #10 does not count the minor #9 released; with no tag
// between them the walk expands #9 itself and #10's sighting is the second.
func TestSinceTagStackedOverAnExcludedAuthorsPullCountsItsCommitsOnce(t *testing.T) {
	const h1 = "d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9d9"
	fixture := func(t *testing.T, tagBetween bool) (s9 string) {
		t.Helper()
		dir, root := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "renovate[bot]", "Update the lock file (#9)")
		s9 = headSHA(t, dir)
		if tagBetween {
			testutil.Git(t, dir, "akira-toriyama", "tag", "v1.0.0")
		}
		s10 := squashCommit(t, dir, "Follow up", 10)
		usePR(t, walkServer(t, map[string]string{
			commitPullsPath(s9):  apiMerged(9, s9),
			pullCommitsPath(9):   apiListing(apiCommitOn(h1, ":sparkles:^ lock the api", root)),
			commitPullsPath(s10): apiMerged(10, s10),
			pullCommitsPath(10):  apiListing(apiCommitOn(h1, ":sparkles:^ lock the api", root), apiCommitOn(stackedB1, ":bug:~ fix the lock", h1)),
		}))
		t.Chdir(dir)
		return s9
	}
	t.Run("a tag between", func(t *testing.T) {
		s9 := fixture(t, true)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "v1.0.1\n" {
			t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v1.0.1 — the minor shipped under v1.0.0\nstderr: %s", code, stdout, stderr)
		}
		if want := "commit " + h1[:7] + " in pull request #10 landed on the released branch as " + s9[:7] + ", which is outside v1.0.0..HEAD"; !strings.Contains(stderr, want) {
			t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
		}
	})
	t.Run("no tag between", func(t *testing.T) {
		fixture(t, false)
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag")
		if code != 0 || !slices.Equal(shas, []string{h1, stackedB1}) {
			t.Fatalf("bump --since-tag --json = exit %d commits %v, want 0 and each commit once\nstderr: %s", code, shas, stderr)
		}
	})
}

// TestSinceTagPackagesExcludedAuthorsSquashIsPricedByWhoCommittedToIt is the
// rule's price under [[packages]] (DESIGN §4.1). The routine dependabot
// squash — nobody else in its listing — costs the lookup and the listing and
// no file read: it is one landed commit, placed by its own diff from local
// git. The same squash with a human's fix in its listing expands, and each
// inner commit placed by its files costs a file read like any other pull's:
// the fix under curry, where its diff lies, and the bot's bump under haiku.
func TestSinceTagPackagesExcludedAuthorsSquashIsPricedByWhoCommittedToIt(t *testing.T) {
	t.Run("the bot's own", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		s5 := testutil.Touch(t, dir, "dependabot[bot]", "Bump a haiku dep from 1 to 2 (#5)", "haiku/go.mod")
		srv, asked := recordingWalkServer(t, map[string]string{
			commitPullsPath(s5): apiMerged(5, s5),
			pullCommitsPath(5):  apiListing(apiCommit("d1", "dependabot[bot]", "Bump a haiku dep from 1 to 2")),
		})
		usePR(t, srv)
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 {
			t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
		}
		if haiku, curry := lineSection(stdout, "haiku"), lineSection(stdout, "curry"); !strings.Contains(haiku, "Bump a haiku dep from 1 to 2 (#5)") || strings.Contains(curry, "Bump a haiku dep") {
			t.Errorf("the squash is placed by its own diff, on haiku alone:\n%s", stdout)
		}
		if want := []string{commitPullsPath(s5), pullCommitsPath(5)}; !slices.Equal(*asked, want) {
			t.Errorf("requests = %v, want the lookup and the listing and no file read", *asked)
		}
	})
	t.Run("with a human's fix", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		s5 := testutil.Touch(t, dir, "dependabot[bot]", "Bump a haiku dep from 1 to 2 (#5)", "haiku/go.mod", "curry/rice.go")
		srv, asked := recordingWalkServer(t, map[string]string{
			commitPullsPath(s5): apiMerged(5, s5),
			pullCommitsPath(5): apiListing(
				apiCommit("d1", "dependabot[bot]", "Bump a haiku dep from 1 to 2"),
				apiCommit("h1", "akira-toriyama", ":bug:(curry)~ fix the curry after the bump")),
			commitFilesPath("d1"): apiFiles("haiku/go.mod"),
			commitFilesPath("h1"): apiFiles("curry/rice.go"),
		})
		usePR(t, srv)
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
		if code != 0 {
			t.Fatalf("bump --since-tag --json exited %d, want 0\nstderr: %s", code, stderr)
		}
		res := decodeJSON[packagesVerdict](t, stdout)
		if h, c := res.Packages[0], res.Packages[1]; h.Level != "none" || len(h.Commits) != 0 || c.Level != "patch" || len(c.Commits) != 1 {
			t.Errorf("haiku = %+v, curry = %+v; want haiku none and curry patch over the fix alone", h, c)
		}
		if len(*asked) != 4 {
			t.Errorf("requests = %v, want four: the lookup, the listing and a file read per inner commit", *asked)
		}
	})
}
