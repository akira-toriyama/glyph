package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// walkServer stands in for api.github.com during a release walk: routes maps
// each expected request path to its response body, and any request outside the
// map fails the test — which is also how a test proves a SHA was NEVER asked
// about (bot commits must stay off the API).
func walkServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// commitPullsPath / pullCommitsPath name the two endpoints the walk touches,
// for the akira-toriyama/glyph repository the tests query.
func commitPullsPath(sha string) string {
	return "/repos/akira-toriyama/glyph/commits/" + sha + "/pulls"
}

func pullCommitsPath(number int) string {
	return fmt.Sprintf("/repos/akira-toriyama/glyph/pulls/%d/commits", number)
}

// apiPullRef renders one pull request in the shape GET commits/{sha}/pulls
// returns, carrying only the fields the walk reads.
func apiPullRef(number int, mergedAt, mergeSHA string) string {
	return fmt.Sprintf(`{"number":%d,"state":"closed","merged_at":%q,"merge_commit_sha":%q}`,
		number, mergedAt, mergeSHA)
}

// squashCommit adds one commit shaped like a squash-merge writes it — the PR
// title plus (#N), deliberately NOT gitmoji-formed — and returns its SHA. If
// the walk classified this subject instead of the PR's individual commits, the
// tests below would fail, which is the squash-safety being pinned.
func squashCommit(t *testing.T, dir string, title string, number int) string {
	t.Helper()
	testCommit(t, dir, "akira-toriyama", fmt.Sprintf("%s (#%d)", title, number))
	return testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestBumpSinceTagExpandsSquashCommits is the release walk end to end: two
// squash commits since v0.1.0, each resolved to its merged PR and expanded to
// the PR's individual commits; the fold runs across ALL inner commits (a
// :sparkles: in PR 7 beats the :bug: in PR 8), so the verdict is a minor —
// even though neither squash subject parses as a gitmoji commit at all.
func TestBumpSinceTagExpandsSquashCommits(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("a1", "akira-toriyama", ":memo: document the menu") + `,` +
			apiCommit("a2", "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.2.0\n" {
		t.Fatalf("bump --since-tag stdout = %q, want %q", stdout, "v0.2.0\n")
	}
}

// TestBumpSinceTagAutoDetectsTheTag: a bare --since-tag resolves the walk base
// itself — the highest parseable v* tag — so the release job never has to
// duplicate glyph's version-tag policy in shell. Only the one commit after
// v0.1.0 may be walked: the tagged base commit reaching the API would show up
// as an unexpected request.
func TestBumpSinceTagAutoDetectsTheTag(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(3, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(3):    `[` + apiCommit("c1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bare bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("bare bump --since-tag stdout = %q, want %q", stdout, "v0.1.1\n")
	}
}

// TestNotesSinceTag: notes walk the same way, so the release body carries one
// line per real change across every PR since the tag — not one line per squash
// subject.
func TestNotesSinceTag(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:(api) stop dropping the last page") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "add a menu") || !strings.Contains(stdout, "stop dropping the last page") {
		t.Fatalf("notes --since-tag body is missing the PRs' inner commits:\n%s", stdout)
	}
}

// TestSinceTagPicksTheMergedMatch: commits/{sha}/pulls returns every PR a
// commit is ASSOCIATED with — a revert PR or a mention can ride along. The walk
// must select on MergeCommitSHA == sha AND merged, never on order.
func TestSinceTagPicksTheMergedMatch(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` +
			apiPullRef(6, "", "somethingelse") + `,` + // associated, never merged
			apiPullRef(9, "2026-07-14T00:00:00Z", "othersha") + `,` + // merged, different squash
			apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`, // the real one
		pullCommitsPath(7): `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v0.2.0 (must pick PR 7 by merge_commit_sha)\nstderr: %s", code, stdout, stderr)
	}
}

// TestSinceTagBotCommitsNeverReachTheAPI: a bot commit on main (the routine
// fleet-sync direct push) is excluded by the participation rules BEFORE any
// resolution — walkServer would fail the test if its SHA were ever asked about.
func TestSinceTagBotCommitsNeverReachTheAPI(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "fleet-sync[bot]", ":robot: chore(fleet): sync a file")
	sha1 := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(3, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(3):    `[` + apiCommit("c1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v0.1.1 (the bot commit must be excluded pre-API)", code, stdout)
	}
}

// TestSinceTagNeedsARepo: the walk resolves PRs over the API, so the repository
// must be known up front — no repo is the caller's input, usage (2), before any
// request or git walk runs.
func TestSinceTagNeedsARepo(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 2 {
		t.Fatalf("bump --since-tag with no repository exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "repo") {
		t.Fatalf("the error should name the missing repository input:\n%s", stderr)
	}
}

// TestSinceTagIsExclusiveWithTheOtherSources: --range, --pr and --since-tag are
// three sources for the same answer; combining any two would silently pick one.
func TestSinceTagIsExclusiveWithTheOtherSources(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)
	for _, extra := range [][]string{
		{"--range", base + "..HEAD"},
		{"--pr", "7"},
	} {
		args := append([]string{"bump", "--since-tag"}, extra...)
		if code, _, _ := runGlyph(t, args...); code != 2 {
			t.Fatalf("bump --since-tag with %v should exit 2 (usage)", extra)
		}
	}
}

// TestSinceTagDirectPushFallsBackToItsOwnMessage: a commit with NO pull-request
// association — a human pushed straight to main — must not fail the release
// (DESIGN §4): it emits a ::warning:: and is classified from its own message.
func TestSinceTagDirectPushFallsBackToItsOwnMessage(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash, pushed directly")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`, // no PR knows this commit
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag over a direct push exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 (the direct push classifies from its own message)", stdout)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, sha[:7]) {
		t.Fatalf("the fallback must announce itself with a ::warning:: naming the commit:\n%s", stderr)
	}
}

// TestSinceTagUnmergedAssociationFallsBack: an association that is not the
// merged match (API lag right after a merge, or a mention from an open PR) is
// no resolution at all — same fallback as no association.
func TestSinceTagUnmergedAssociationFallsBack(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "", sha) + `]`, // associated but never merged
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("exit %d stdout %q, want 0 / v0.1.1 (an unmerged association must fall back)\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "::warning::") {
		t.Fatalf("the fallback must emit a ::warning::\n%s", stderr)
	}
}

// TestSinceTagMalformedFallbackNeverFailsTheRelease: DESIGN §4 — fallbacks
// never hard-fail. A direct-push commit whose message does not even parse is
// warned and counted none; the release proceeds on the rest of the walk. (The
// same message inside a PR is a hard lint error — the strictness lives on the
// lint gate, not on the release.)
func TestSinceTagMalformedFallbackNeverFailsTheRelease(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "hotfix without any gitmoji")
	bad := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	good := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(bad):  `[]`,
		commitPullsPath(good): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", good) + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("a malformed fallback commit exited %d, want 0 — the release must not hard-fail\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 from the resolved PR", stdout)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, bad[:7]) {
		t.Fatalf("the skipped commit must be warned about by SHA:\n%s", stderr)
	}
}

// TestSinceTagUnknownGitmojiFallbackCountsNone pins the ratified t-kbqx policy:
// on the FALLBACK path only, an unknown gitmoji degrades to a ::warning:: and
// counts none — the only resolution of DESIGN §2 ("unknown is a hard lint
// error, never a silent patch": the warning keeps it non-silent) with §4
// ("fallbacks never hard-fail a release"). Alone in the walk it yields a soft
// no-release, never a lint failure and never a patch.
func TestSinceTagUnknownGitmojiFallbackCountsNone(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":notarealmoji: tweak something")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code == 3 {
		t.Fatalf("an unknown gitmoji on the fallback path exited 3 (lint) — it must degrade to a warning:\n%s", stderr)
	}
	if code != 1 {
		t.Fatalf("exited %d, want 1 (the unknown commit counts none → no release)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, ":notarealmoji:") {
		t.Fatalf("the warning must name the unknown code:\n%s", stderr)
	}
}

// TestSinceTagAutoWithoutTagsWalksTheWholeHistory: a repository before its
// first release has no walk base — auto walks everything from the root, and
// the bump steps from v0.0.0.
func TestSinceTagAutoWithoutTagsWalksTheWholeHistory(t *testing.T) {
	dir, base := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	sha1 := squashCommit(t, dir, "Add a menu", 2)
	srv := walkServer(t, map[string]string{
		commitPullsPath(base): `[]`, // the root :tada: commit: fallback, none
		commitPullsPath(sha1): `[` + apiPullRef(2, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(2):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag before the first release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.0\n" {
		t.Fatalf("stdout = %q, want v0.1.0 (minor step from v0.0.0)", stdout)
	}
}

// TestSinceTagEmptyWalkIsNoRelease: nothing on main since the tag is the quiet
// week — a soft no-release (1) naming the range it read, not an error.
func TestSinceTagEmptyWalkIsNoRelease(t *testing.T) {
	dir, _ := testRepo(t)
	srv := walkServer(t, map[string]string{})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("an empty walk exited %d, want 1 (no release)", code)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "v0.1.0..HEAD") {
		t.Fatalf("the no-release reason should name the walked range:\n%s", stderr)
	}
}
