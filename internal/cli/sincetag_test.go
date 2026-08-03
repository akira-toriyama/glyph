package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/gitsource"
)

// apiUnknownSHA is the route body meaning "answer this path the way GitHub
// answers for a commit it does not know yet": 422, DESIGN §4's API lag. A
// response BODY cannot express a status code, so a sentinel does — the release
// job runs seconds after the merge, and the walk outrunning the API is a shape
// several tests below must stub next to ordinary ones in the same routes map.
const apiUnknownSHA = "\x00 422 unknown sha"

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
		if body == apiUnknownSHA {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"No commit found for SHA"}`)
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

// mergePoint is one branch landed on main behind a two-parent merge commit —
// what GitHub's "Create a merge commit" button writes, and what 31 of the
// fleet's 34 repositories allow. Unlike a squash, the branch's commits stay on
// main under their ORIGINAL SHAs, so the walk sees the merge point AND every
// commit it merged.
type mergePoint struct {
	Merge  string   // the merge commit — GitHub's merge_commit_sha for the PR
	Branch []string // the merged commits as they sit on main, oldest first
}

// mergePR lands messages on main through a merge commit shaped exactly like the
// merge button's: GitHub's own `Merge pull request #N from owner/branch`
// subject, which is a POINTER to the PR and deliberately not gitmoji-formed —
// if the walk classified it (or skipped it for its two parents) instead of
// resolving it, the tests below would fail, which is the t-7zt7 regression
// being pinned.
func mergePR(t *testing.T, dir, author string, number int, messages ...string) mergePoint {
	t.Helper()
	return mergeInto(t, dir, author, author, prTopic(number), prMergeSubject(number), messages...)
}

// prTopic / prMergeSubject name the branch and the subject GitHub writes for
// pull request N, so a test that needs a different merge AUTHOR (an automation
// pressing the button) still lands the identical shape.
func prTopic(number int) string { return fmt.Sprintf("topic-%d", number) }

func prMergeSubject(number int) string {
	return fmt.Sprintf("Merge pull request #%d from akira-toriyama/%s", number, prTopic(number))
}

// mergeInto commits messages on a topic branch as author and merges it back
// with --no-ff as merger, under subject. The two identities are separate
// because they are separate in life: the merged commits carry the contributor,
// the merge commit carries whoever pressed the button — an automation whenever
// a repository auto-merges.
func mergeInto(t *testing.T, dir, author, merger, branch, subject string, messages ...string) mergePoint {
	t.Helper()
	testGit(t, dir, author, "checkout", "-q", "-b", branch)
	mp := mergePoint{}
	for _, m := range messages {
		testCommit(t, dir, author, m)
		mp.Branch = append(mp.Branch, testGit(t, dir, author, "rev-parse", "HEAD"))
	}
	testGit(t, dir, author, "checkout", "-q", "main")
	testGit(t, dir, merger, "merge", "-q", "--no-ff", "-m", subject, branch)
	mp.Merge = testGit(t, dir, author, "rev-parse", "HEAD")
	return mp
}

// verdictSHAs runs bump --json over revRange's walk and returns the
// participating commits' SHAs — the shape every "counted exactly once" test
// asserts on.
func verdictSHAs(t *testing.T, args ...string) (code int, shas []string, stderr string) {
	t.Helper()
	code, stdout, stderr := runGlyph(t, append(args, "--json")...)
	if code != 0 {
		return code, nil, stderr
	}
	var res struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	for _, c := range res.Commits {
		shas = append(shas, c.SHA)
	}
	return code, shas, stderr
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

// TestSinceTagRevertPRIsWalked: a revert PR squash-merges with the subject
// `Revert "..." (#N)` — the same prefix bump.ExcludedFromClassification uses to skip raw
// git-revert messages. On the walk that subject is a POINTER to a resolvable
// PR, not a message being classified: excluding it here would silently drop
// the whole revert (its :rewind: inner commits drive a patch) while the
// reverted feature still counts — a release advertising a feature that is
// gone.
func TestSinceTagRevertPRIsWalked(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", `Revert "Add a menu" (#9)`)
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(9, "2026-07-15T00:00:00Z", sha) + `]`,
		pullCommitsPath(9):   `[` + apiCommit("r1", "akira-toriyama", ":rewind: revert the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("a revert PR's squash commit exited %d, want 0 — it must resolve, not vanish\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 from the revert PR's :rewind: commit", stdout)
	}
}

// TestSinceTagRawRevertDirectPushIsExcluded: a raw `git revert` pushed straight
// to main has no PR to resolve; on the fallback path the generated-subject
// exclusion applies exactly as it does on --range — skipped silently, never a
// violation and never a warning.
func TestSinceTagRawRevertDirectPushIsExcluded(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", `Revert ":bug: fix a crash"`)
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("a lone raw-revert direct push exited %d, want 1 (no release — it is excluded)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if strings.Contains(stderr, "::warning::") {
		t.Fatalf("an excluded commit is skipped, never warned about (parity with --range):\n%s", stderr)
	}
}

// TestSinceTagRebaseMergedPRDoesNotDoubleCount: on a rebase-merge, every
// rebased commit is associated with the merged PR but only the LAST one equals
// merge_commit_sha. The earlier ones are covered by that PR — falling back on
// them would count each change twice (once rebased, once via the PR's
// expansion) and spray direct-push warnings over a perfectly normal merge.
func TestSinceTagRebaseMergedPRDoesNotDoubleCount(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles: add a menu")
	shaA := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "akira-toriyama", ":bug: fix the menu")
	shaB := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(shaA): `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`,
		commitPullsPath(shaB): `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `,` +
			apiCommit("b1", "akira-toriyama", ":bug: fix the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump over a rebase-merged PR exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("verdict counts %d commits, want exactly the PR's 2 (no double count): %+v", len(res.Commits), res.Commits)
	}
	if strings.Contains(stderr, "::warning::") {
		t.Fatalf("a rebase-merged PR is normal, not a direct push — no warnings:\n%s", stderr)
	}
}

// TestSinceTagMergeCommitPRIsExpanded is the t-7zt7 regression: a pull request
// landed with the "Create a merge commit" button must be resolved and expanded
// exactly like a squash-merged one. GitHub sets merge_commit_sha to the merge
// commit's OWN sha, so it is the PR's pointer commit; excluding it before
// resolution (for its two parents) dropped the whole PR out of both the version
// and the notes, silently — a click on the wrong green button was enough, on 31
// of the fleet's 34 repositories.
func TestSinceTagMergeCommitPRIsExpanded(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, ":memo: document the menu", ":sparkles:(ui) add a menu")
	ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		// `git log` (no --first-parent) lists the merged commits beside the
		// merge point, and GitHub associates each of them with the same PR —
		// but only the merge commit equals its merge_commit_sha.
		commitPullsPath(mp.Branch[0]): ref,
		commitPullsPath(mp.Branch[1]): ref,
		commitPullsPath(mp.Merge):     ref,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":memo: document the menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("bump over a merge-commit-merged PR exited %d, want 0 — the PR must not vanish\nstderr: %s", code, stderr)
	}
	if len(shas) != 2 || shas[0] != mp.Branch[0] || shas[1] != mp.Branch[1] {
		t.Fatalf("verdict counts %v, want the PR's two inner commits %v exactly once each", shas, mp.Branch)
	}
	if strings.Contains(stderr, "::warning::") {
		t.Fatalf("a merge-merged PR is a normal merge, not a direct push — no warnings:\n%s", stderr)
	}

	// The notes half of the same silence: a vanished PR took its release lines
	// with it.
	code, notes, stderr := runGlyph(t, "notes", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("notes over a merge-commit-merged PR exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(notes, "add a menu") {
		t.Fatalf("the merge-merged PR's commits are missing from the notes:\n%s", notes)
	}
}

// TestSinceTagSubPullSquashCommitIsNotRelinted is the wedge round 2 of t-7zt7
// found: pull request #6 is squash-merged INTO a topic branch, so its squash
// commit — subject `Add a menu (#6)`, deliberately not gitmoji-formed, which is
// exactly what a squash subject looks like — sits on that branch; the merge
// button then lands the branch on main as pull request #7, carrying #6's squash
// commit onto main verbatim. GET pulls/7/commits therefore hands the walk a
// commit it resolved and expanded one step earlier.
//
// The walk-wide SHA set remembered #6's INNER commits but not the canonical
// commit that REPRESENTED #6, so #7's expansion re-read that subject as an
// ordinary message and hard-failed the release (exit 3). Squash history is
// immutable, so that is a PERMANENT wedge — every future release fails at the
// same commit. A resolved commit is already represented in the fold; the set
// must record that about the commit ITSELF, not only about its expansion.
func TestSinceTagSubPullSquashCommitIsNotRelinted(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergeInto(t, dir, "akira-toriyama", "akira-toriyama", prTopic(7), prMergeSubject(7),
		"Add a menu (#6)", ":bug:(ui) polish the menu")
	sub, rest := mp.Branch[0], mp.Branch[1]
	subRef := apiPullRef(6, "2026-07-19T00:00:00Z", sub)
	prRef := apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge)
	srv := walkServer(t, map[string]string{
		// #6's squash commit is associated with BOTH pulls (it is inside #7's
		// branch), and only #6 equals its merge_commit_sha — so it resolves to
		// #6 and is COVERED by #7, which #7's own merge point represents.
		commitPullsPath(sub):      `[` + subRef + `,` + prRef + `]`,
		commitPullsPath(rest):     `[` + prRef + `]`,
		commitPullsPath(mp.Merge): `[` + prRef + `]`,
		pullCommitsPath(6):        `[` + apiCommit("i1", "akira-toriyama", ":sparkles: add a menu") + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(sub, "akira-toriyama", "Add a menu (#6)") + `,` +
			apiCommit(rest, "akira-toriyama", ":bug:(ui) polish the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("a sub-PR's squash commit inside a merge-merged PR exited %d, want 0 — it is already represented in the fold, not a malformed message to lint\nstderr: %s", code, stderr)
	}
	if len(shas) != 2 || shas[0] != "i1" || shas[1] != rest {
		t.Fatalf("verdict counts %v, want [i1 %.7s] — #6 expanded once, its squash commit never counted as itself", shas, rest)
	}
	if !strings.Contains(stderr, "::notice::") || !strings.Contains(stderr, sub[:7]) {
		t.Fatalf("skipping the already-resolved squash commit must be announced by SHA:\n%s", stderr)
	}
}

// TestSinceTagUnresolvableMergePointIsWarnedNotExpanded is the other half of the
// t-7zt7 silence, found by review on the very path the fix claims to handle: the
// branch commits of a merge-merged pull request resolve as COVERED (they name
// the pull, but its merge_commit_sha is the merge commit, not them), so they
// skip themselves in favour of the merge point — and then the merge point does
// not resolve. A release job runs seconds after the merge, so GitHub routinely
// knows the branch commits' association before it knows the merge commit at all;
// the merge commit is also unresolvable when an automation pressed the button
// (the author gate skips it before the API).
//
// Every commit of the pull then skipped itself: exit 1, "no release", with no
// warning and no notice. The "all N commit(s) unknown" guard cannot catch it
// either (three commits walked, at most one unknown). The loss is real and
// cannot be repaired from here — the pull's listing is its whole history and
// says nothing about this range (see the two tests below for what folding it
// anyway produces) — so what must never happen is losing it SILENTLY. The walk
// names the pull, says the release is short, and says what to do; the absent
// pulls/7/commits route is the proof it does not fold, since asking for it fails
// this test.
func TestSinceTagUnresolvableMergePointIsWarnedNotExpanded(t *testing.T) {
	for name, tc := range map[string]struct {
		mergeRoute  string
		wantLagWarn bool // the merge commit itself must be named as API lag
	}{
		"the merge commit is not on the API yet (422)": {apiUnknownSHA, true},
		"the merge commit has no association yet":      {`[]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			mp := mergePR(t, dir, "akira-toriyama", 7, ":memo: document the menu", ":sparkles:(ui) add a menu")
			ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
			srv := walkServer(t, map[string]string{
				commitPullsPath(mp.Branch[0]): ref,
				commitPullsPath(mp.Branch[1]): ref,
				commitPullsPath(mp.Merge):     tc.mergeRoute,
			})
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
			if code != 1 || stdout != "" {
				t.Fatalf("bump = exit %d stdout %q, want 1 / empty — the pull's commits stood aside and nothing may be invented for them\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "#7") {
				t.Fatalf("a pull the walk saw only from the inside must be named in a ::warning::, never lost in silence:\n%s", stderr)
			}
			for _, want := range []string{"NOT counted", "re-run"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the warning must say what was lost and what to do (missing %q):\n%s", want, stderr)
				}
			}
			if tc.wantLagWarn && !strings.Contains(stderr, mp.Merge[:7]) {
				t.Fatalf("a merge commit excluded while the API did not know it must keep its own diagnostic, not throw the lag away:\n%s", stderr)
			}
		})
	}
}

// TestSinceTagUnresolvedPullIsNeverFoldedFromItsListing pins WHY the warning
// above is warning-only. Folding the pull's listing there looks like a rescue
// and is a guess: the listing is the pull's entire history, and the walk's range
// is a git fact no listing carries. Two reviewers broke that guess independently,
// and each shape gets a subtest here — in both, pulls/7/commits is deliberately
// absent from the routes, so a walk that folds the pull fails the test outright.
//
//   - rebase-merged: GitHub's listing reports the ORIGINAL pre-rebase SHAs,
//     which can never equal the main-branch SHAs the walk-wide set holds, so the
//     dedup filter passes every one of them and the same change renders twice.
//   - already released: the listing reaches back past the previous tag, so a
//     commit that shipped one release ago folds straight back in — a minor bump
//     manufactured out of released work, on a stderr that carried nothing.
func TestSinceTagUnresolvedPullIsNeverFoldedFromItsListing(t *testing.T) {
	t.Run("a rebase-merged pull lists its pre-rebase SHAs", func(t *testing.T) {
		dir, _ := testRepo(t)
		testCommit(t, dir, "akira-toriyama", ":sparkles: add a menu")
		shaA := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testCommit(t, dir, "akira-toriyama", ":bug: fix the menu")
		shaB := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		srv := walkServer(t, map[string]string{
			// shaB is the pull's merge point — and the API does not know it yet,
			// so shaA stands aside for a commit that never resolves while shaB
			// falls back on its own message.
			commitPullsPath(shaA): `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`,
			commitPullsPath(shaB): apiUnknownSHA,
		})
		usePR(t, srv)
		t.Chdir(dir)

		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
		if code != 0 {
			t.Fatalf("bump exited %d, want 0 (shaB classifies itself)\nstderr: %s", code, stderr)
		}
		if len(shas) != 1 || shas[0] != shaB {
			t.Fatalf("verdict counts %v, want [%.7s] alone — folding the listing here would add its pre-rebase SHAs beside it and render the same change twice", shas, shaB)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "#7") {
			t.Fatalf("the pull nothing resolved to must still be named:\n%s", stderr)
		}
	})

	t.Run("the listing reaches back past the previous tag", func(t *testing.T) {
		dir, _ := testRepo(t)
		mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui) add a menu", ":memo: document the menu")
		// v0.1.1 shipped the pull's first commit already — the release walk then
		// starts strictly after it, but the pull's listing still begins there.
		testGit(t, dir, "akira-toriyama", "tag", "v0.1.1", mp.Branch[0])
		ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
		srv := walkServer(t, map[string]string{
			commitPullsPath(mp.Branch[1]): ref,
			commitPullsPath(mp.Merge):     `[]`, // the merge point resolves to nothing
		})
		usePR(t, srv)
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.1")
		if code != 1 || stdout != "" {
			t.Fatalf("bump = exit %d stdout %q, want 1 / empty — the only change the fold would find (%s) went out in v0.1.1\nstderr: %s", code, stdout, ":sparkles:(ui) add a menu", stderr)
		}
		if strings.Contains(stdout, "v0.2.0") {
			t.Fatalf("a minor manufactured out of a commit released one tag ago: %q", stdout)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "#7") {
			t.Fatalf("the pull nothing resolved to must still be named:\n%s", stderr)
		}
	})
}

// TestSinceTagHealthyRepositoryIsNeverWarnedAt: the covered-pull warning names a
// real loss, so it must be silent on every ordinary release — a warning that
// fires on healthy repositories is worse than none, and the fleet's 34
// repositories release through exactly these shapes. Standing aside requires the
// pull's merge point to be IN the range, and a merge point in range that
// resolves is expanded on the spot, so none of these can reach the ledger.
func TestSinceTagHealthyRepositoryIsNeverWarnedAt(t *testing.T) {
	for name, build := range map[string]func(t *testing.T, dir string) map[string]string{
		"squash-merged pulls": func(t *testing.T, dir string) map[string]string {
			sha1 := squashCommit(t, dir, "Add a menu", 7)
			sha2 := squashCommit(t, dir, "Fix a crash", 8)
			return map[string]string{
				commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
				commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
				pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
				pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
			}
		},
		"a stacked pair": func(t *testing.T, dir string) map[string]string {
			sha1 := squashCommit(t, dir, "Add a menu", 7)
			sha2 := squashCommit(t, dir, "Fix a crash", 8)
			base := apiPullRef(7, "2026-07-12T00:00:00Z", sha1)
			stacked := apiPullRef(8, "2026-07-13T00:00:00Z", sha2)
			return map[string]string{
				// The base pull's squash commit is inside the stacked pull's
				// branch, so GitHub associates it with both — it resolves to #7
				// and is COVERED by #8, whose own squash commit expands #8.
				commitPullsPath(sha1): `[` + base + `,` + stacked + `]`,
				commitPullsPath(sha2): `[` + stacked + `]`,
				pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
				pullCommitsPath(8): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `,` +
					apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
			}
		},
		"a rebase-merged pull": func(t *testing.T, dir string) map[string]string {
			testCommit(t, dir, "akira-toriyama", ":sparkles: add a menu")
			shaA := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testCommit(t, dir, "akira-toriyama", ":bug: fix the menu")
			shaB := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			ref := `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`
			return map[string]string{
				commitPullsPath(shaA): ref,
				commitPullsPath(shaB): ref,
				pullCommitsPath(7): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `,` +
					apiCommit("b1", "akira-toriyama", ":bug: fix the menu") + `]`,
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			srv := walkServer(t, build(t, dir))
			usePR(t, srv)
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
			if code != 0 {
				t.Fatalf("bump over an ordinary release shape exited %d, want 0\nstderr: %s", code, stderr)
			}
			if strings.Contains(stderr, "::warning::") {
				t.Fatalf("no warning may fire on a healthy repository — one that cries on every release is worse than none:\n%s", stderr)
			}
		})
	}
}

// TestSinceTagCoveredByAPullOnAnotherBaseBranchStillCounts is the boundary of
// standing aside, and the reason "covered" is gated on the merge point being IN
// the walked range rather than on "a merge point is always walked after the
// commits it merges". That reachability argument silently assumes the merge
// point is on this history at all: a pull merged into a DIFFERENT base branch (a
// backport line, a fork, a long-lived next branch) is associated with a commit
// that reached main by another route, while its own merge commit is on a history
// main never took. Standing aside for it would drop the commit from the release
// and blame a pull that has nothing to do with it.
//
// So the commit falls back on its own message — counted once, warned about —
// and pulls/9/commits is deliberately absent from the routes: asking for it
// fails the test.
func TestSinceTagCoveredByAPullOnAnotherBaseBranchStillCounts(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		// Merged — but its merge commit is not a commit of this range.
		commitPullsPath(sha): `[` + apiPullRef(9, "2026-07-14T00:00:00Z", "amergecommitonanotherbranch") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump = exit %d stdout %q, want 0 / v0.1.1 — the commit is on main and nothing in the range represents it\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, sha[:7]) {
		t.Fatalf("the fallback must name the commit it classified:\n%s", stderr)
	}
	if strings.Contains(stderr, "#9") {
		t.Fatalf("a pull merged into another base branch is not this release's business, in the verdict or in the diagnostics:\n%s", stderr)
	}
}

// TestSinceTagMergeCommitBranchCommitsCountOnce pins the double walk: the merge
// point and every commit it merged are BOTH in the range. Here GitHub has not
// associated the merged commits with the PR yet (the association lag right
// after a merge), so they take the fallback path and count from their own
// messages — and the merge point, walked last (git's revision walk discovers a
// parent only through its child, so a merge point can never precede the commits
// it merges, whatever the clocks say), must then fold in NOTHING new. Each
// change lands in the verdict exactly once, whichever path reached it first.
func TestSinceTagMergeCommitBranchCommitsCountOnce(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, ":memo: document the menu", ":sparkles:(ui) add a menu")
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): `[]`, // not associated (yet)
		commitPullsPath(mp.Branch[1]): `[]`,
		commitPullsPath(mp.Merge):     `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":memo: document the menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(shas) != 2 || shas[0] != mp.Branch[0] || shas[1] != mp.Branch[1] {
		t.Fatalf("verdict counts %v, want %v — each merged commit exactly once", shas, mp.Branch)
	}
	if !strings.Contains(stderr, "::notice::") || !strings.Contains(stderr, mp.Branch[1][:7]) {
		t.Fatalf("the dedup must announce each already-folded commit with a ::notice:: naming it:\n%s", stderr)
	}
}

// TestSinceTagPlainMergeCommitIsSkipped: a local `git merge` of a topic branch
// (or a merge from a fork) pushed to main resolves to NO pull request. It must
// keep being skipped silently, exactly as before — the fallback path still
// applies the message rules, where two parents (and git's own `Merge ` subject)
// exclude it. Only the resolution question stopped asking about the shape; the
// classification question never did.
func TestSinceTagPlainMergeCommitIsSkipped(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergeInto(t, dir, "akira-toriyama", "akira-toriyama", "topic", "Merge branch 'topic'", ":memo: note the topic")
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): `[]`,
		// The merge commit is asked about now (it costs the one round-trip that
		// buys the answer) and nothing merged it — the local-merge shape.
		commitPullsPath(mp.Merge): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 1 {
		t.Fatalf("a local merge of a :memo: branch exited %d, want 1 (no release — both commits count none)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
	if strings.Contains(stderr, mp.Merge[:7]) || strings.Contains(stderr, "Merge branch") {
		t.Fatalf("an unresolved merge commit is skipped silently, never warned about:\n%s", stderr)
	}
}

// TestSinceTagBotMergeCommitCostsNoLookup: the author gate still runs BEFORE
// resolution, merge commit or not — walkServer fails the test on any request it
// did not expect, so leaving the merge commit's own route out of the map is the
// proof that no round-trip was spent on it. That property is what keeps the
// routine fleet-sync push (every repo, every day) free.
//
// The branch commit's route is stubbed the way GitHub actually answers, which
// is the correction here: for a commit of a pull request GitHub considers
// merged it names that pull, with merge_commit_sha pointing at the merge commit
// — it does NOT answer []. (The stub used to answer [], a state the API cannot
// produce, and the test then asserted a release that the realistic shape does
// not produce on its own.) With the honest shape the pull is one the walk can
// only ever see from the INSIDE, so the price of keeping the round-trip free is
// paid here: the pull's commits stand aside for a merge point the walk will
// never ask about, and the release is short. That is a loss the walk cannot
// repair from the pull's listing, so it must at least be loud — the ::warning::
// naming the pull is the whole difference from the t-7zt7 silence.
func TestSinceTagBotMergeCommitCostsNoLookup(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergeInto(t, dir, "akira-toriyama", "github-actions[bot]", prTopic(7), prMergeSubject(7), ":bug: fix a crash")
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`,
		// The merge commit's own route is deliberately absent, and so is
		// pulls/7/commits: asking for either would fail this test — the first
		// proves the author gate still runs before the API, the second that a
		// pull nothing resolved to is never folded from its own listing.
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 1 || stdout != "" {
		t.Fatalf("bump = exit %d stdout %q, want 1 / empty (the bot's merge commit costs no lookup, so nothing resolves the pull and nothing may be invented for it)\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "#7") {
		t.Fatalf("a pull the walk only saw from the inside must be named in a ::warning::, never lost in silence:\n%s", stderr)
	}
}

// TestSinceTagSharedPreSquashCommitCountsOnce: a stacked branch carries its
// base PR's pre-squash commits, so after both squash-merge, both PRs' listings
// contain them. Each shared commit must fold and render exactly once,
// attributed to the PR the walk resolves first (the older merge on main).
func TestSinceTagSharedPreSquashCommitCountsOnce(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("a1", "akira-toriyama", ":sparkles: add a menu") + `,` + // the base PR's commit riding along
			apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0", "--json")
	if code != 0 {
		t.Fatalf("bump over a stacked pair exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("verdict counts %d commits, want 2 (a1 once + b1): %+v", len(res.Commits), res.Commits)
	}
	if !strings.Contains(stderr, "::notice::") || !strings.Contains(stderr, "a1") {
		t.Fatalf("the dedup must announce the shared commit with a ::notice:: naming it:\n%s", stderr)
	}

	code, notes, stderr := runGlyph(t, "notes", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("notes over a stacked pair exited %d, want 0\nstderr: %s", code, stderr)
	}
	if got := strings.Count(notes, "add a menu"); got != 1 {
		t.Fatalf("the shared commit renders %d note line(s), want exactly 1:\n%s", got, notes)
	}
}

// TestSinceTagFallbackSharesShaWithAnExpandedPR: a fast-forwarded branch puts a
// PR's pre-squash commits on main under their ORIGINAL SHAs. When the walk then
// falls back on such a commit (no merged PR resolves it), it must not fold what
// an expanded PR already contributed.
func TestSinceTagFallbackSharesShaWithAnExpandedPR(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	testCommit(t, dir, "akira-toriyama", ":sparkles: add a menu")
	sha2 := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[]`, // no PR resolves it — the fallback path
		// PR 7's listing already carries the commit under the SHA it has on main.
		pullCommitsPath(7): `[` + apiCommit(sha2, "akira-toriyama", ":sparkles: add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0", "--json")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("verdict counts %d commits, want 1 (the fallback must dedup against the expanded PR): %+v", len(res.Commits), res.Commits)
	}
}

// TestSinceTagExplicitEmptyIsUsage: `--since-tag=` (a workflow templating an
// unset variable) must not silently degrade to auto — the caller NAMED a tag
// and the name is empty. Usage, before anything runs.
func TestSinceTagExplicitEmptyIsUsage(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph") // so the repo guard cannot mask the tag guard
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag=")
	if code != 2 {
		t.Fatalf("bump --since-tag= exited %d, want 2 (usage — an empty tag is not auto)", code)
	}
	if !strings.Contains(stderr, "since-tag") {
		t.Fatalf("the error should name the flag:\n%s", stderr)
	}
}

// TestSinceTagOptionShapedTagIsUsage: symmetric with checkRangeFlag — an
// option-shaped or range-shaped tag is the caller's input, rejected as usage
// (2) before git runs, not surfaced as a retryable-looking API failure (4).
func TestSinceTagOptionShapedTagIsUsage(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph") // so the repo guard cannot mask the tag guard
	dir, _ := testRepo(t)
	t.Chdir(dir)
	for _, tag := range []string{"-v0.1.0", "v0.1.0..HEAD"} {
		code, _, stderr := runGlyph(t, "bump", "--since-tag="+tag)
		if code != 2 {
			t.Fatalf("bump --since-tag=%q exited %d, want 2 (usage)", tag, code)
		}
		if !strings.Contains(stderr, "since-tag") {
			t.Fatalf("the error for %q should name --since-tag:\n%s", tag, stderr)
		}
	}
}

// TestBumpSinceTagExplicitTagIsTheStepBase: naming a tag names the release
// being redone — the walk base and the step base must be the SAME tag, or the
// verdict is computed from one range and versioned from another. --current
// still wins when given.
func TestBumpSinceTagExplicitTagIsTheStepBase(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0") // base also carries a HIGHER tag
	sha := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(3, "2026-07-15T00:00:00Z", sha) + `]`,
		pullCommitsPath(3):   `[` + apiCommit("c1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("bump --since-tag=v0.1.0 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 — the named tag is the step base, not the higher v0.2.0", stdout)
	}

	code, stdout, _ = runGlyph(t, "bump", "--since-tag=v0.1.0", "--current", "v3.0.0")
	if code != 0 || stdout != "v3.0.1\n" {
		t.Fatalf("--current must still win: exit %d stdout %q, want 0 / v3.0.1", code, stdout)
	}
}

// TestSinceTagAllCommitsUnknownWarnsAboutTheRepo: one unknown SHA is API lag;
// EVERY SHA unknown is what a wrong --repo / inherited GITHUB_REPOSITORY looks
// like (a fork or reusable-workflow context) — the walk still soft-falls-back,
// but a summary warning must say the quiet part out loud so the
// misconfiguration is findable in the log.
func TestSinceTagAllCommitsUnknownWarnsAboutTheRepo(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testCommit(t, dir, "akira-toriyama", ":memo: document it")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"No commit found for SHA"}`)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("an all-unknown walk exited %d, want 0 (still a soft fallback)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--repo") {
		t.Fatalf("when EVERY commit is unknown the warning must point at --repo/GITHUB_REPOSITORY:\n%s", stderr)
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
	if !strings.Contains(stderr, "whole history") {
		t.Fatalf("walking everything is a cost the caller should see named (one API round-trip per commit):\n%s", stderr)
	}
}

// TestSinceTagUnknownShaFallsBack: GitHub answers commits/{sha}/pulls with 422
// when it does not yet know the commit — the walk running moments after a
// push, before the API catches up. That is DESIGN §4's API lag spelled out by
// the server, so it falls back exactly like an empty association instead of
// hard-failing the release. (A 404 — the auth-failure shape — still fails.)
func TestSinceTagUnknownShaFallsBack(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash, pushed moments ago")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != commitPullsPath(sha) {
			t.Errorf("unexpected request %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"No commit found for SHA: `+sha+`"}`)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("a 422 (SHA not yet on GitHub) exited %d, want 0 — the walk must fall back\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 from the commit's own message", stdout)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, sha[:7]) {
		t.Fatalf("the fallback must announce itself with a ::warning:: naming the commit:\n%s", stderr)
	}
}

// TestSinceTagSpaceFormIsGuided: --since-tag takes its optional value only in
// the = form (pflag's NoOptDefVal grammar), so `--since-tag v0.1.0` parses as a
// bare --since-tag plus a stray positional. That must not walk the WRONG range
// silently — it is a usage error that spells out the = form.
func TestSinceTagSpaceFormIsGuided(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag", "v0.1.0")
	if code != 2 {
		t.Fatalf("bump --since-tag v0.1.0 (space form) exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "--since-tag=v0.1.0") {
		t.Fatalf("the error should spell out the = form:\n%s", stderr)
	}
}

// TestRepoWithRangeIsUsage: --repo configures the API-backed sources (--pr,
// --since-tag); combined with the purely local --range it used to be silently
// ignored. No silent ignores — it is a usage error.
func TestRepoWithRangeIsUsage(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)

	code, _, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--repo", "akira-toriyama/glyph")
	if code != 2 {
		t.Fatalf("bump --range with --repo exited %d, want 2 (usage — --repo would be silently ignored)", code)
	}
}

// TestPullCommitCapIsWarned: GitHub truncates pulls/{n}/commits at 250 no
// matter the pagination. A PR that returns exactly the cap may have lost
// commits — and a lost commit could carry THE deciding gitmoji — so the walk
// must say so (house rule: no silent caps). One under the cap stays quiet.
func TestPullCommitCapIsWarned(t *testing.T) {
	build := func(n int) string {
		commits := make([]string, n)
		for i := range commits {
			commits[i] = apiCommit(fmt.Sprintf("c%03d", i), "akira-toriyama", ":bug: fix crash number "+fmt.Sprint(i))
		}
		return `[` + strings.Join(commits, ",") + `]`
	}
	for name, tc := range map[string]struct {
		n        int
		wantWarn bool
	}{
		"at the cap": {250, true},
		"one under":  {249, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := prServer(t, 7, build(tc.n))
			usePR(t, srv)
			dir, _ := testRepo(t)
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "bump", "--pr", "7")
			if code != 0 {
				t.Fatalf("bump --pr exited %d, want 0\nstderr: %s", code, stderr)
			}
			if got := strings.Contains(stderr, "::warning::"); got != tc.wantWarn {
				t.Fatalf("warning emitted = %v, want %v for %d commits:\n%s", got, tc.wantWarn, tc.n, stderr)
			}
		})
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

// TestSinceTagUnknownGitmojiBreakingFallbackIsMajor pins the ratified Q10
// policy: on the fallback path a breaking marker is NEVER suppressed — not
// even by an unknown gitmoji. An unknown code alone downgrades to none (the
// typo case, pinned above); an unknown code CARRYING a breaking marker (`!`
// or a BREAKING CHANGE footer) majors the verdict behind a ::warning::,
// normalized to the canonical :boom: — the failure asymmetry is deliberate:
// a typo can over-bump a version, but a breaking change must never be
// silently dropped from one.
func TestSinceTagUnknownGitmojiBreakingFallbackIsMajor(t *testing.T) {
	for name, message := range map[string]string{
		"bang":   ":notarealmoji:! drop the legacy flag",
		"footer": ":notarealmoji: drop the legacy flag\n\nBREAKING CHANGE: the flag is gone",
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			testCommit(t, dir, "akira-toriyama", message)
			sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			srv := walkServer(t, map[string]string{
				commitPullsPath(sha): `[]`, // a direct push — the fallback path
			})
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
			if code != 0 {
				t.Fatalf("an unknown+breaking fallback exited %d, want 0 (it must MAJOR, not vanish)\nstderr: %s", code, stderr)
			}
			var res struct {
				Level   string `json:"level"`
				Next    string `json:"next"`
				Commits []struct {
					Code     string `json:"code"`
					Level    string `json:"level"`
					Breaking bool   `json:"breaking"`
					Subject  string `json:"subject"`
				} `json:"commits"`
			}
			if err := json.Unmarshal([]byte(stdout), &res); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, stdout)
			}
			if res.Level != "major" || res.Next != "v1.0.0" {
				t.Fatalf("verdict = level %q next %q, want major → v1.0.0", res.Level, res.Next)
			}
			if len(res.Commits) != 1 || res.Commits[0].Code != ":boom:" || !res.Commits[0].Breaking {
				t.Fatalf("commits = %+v, want the one commit normalized to :boom: with breaking true", res.Commits)
			}
			if res.Commits[0].Subject != "drop the legacy flag" {
				t.Fatalf("subject = %q, want the commit's own subject kept verbatim", res.Commits[0].Subject)
			}
			if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, ":notarealmoji:") {
				t.Fatalf("the warning must name the real unknown code:\n%s", stderr)
			}
			if !strings.Contains(stderr, "breaking") {
				t.Fatalf("the warning must say WHY this one counted (the breaking marker):\n%s", stderr)
			}
		})
	}
}

// TestNotesSinceTagUnknownBreakingFallbackHoists: the same commit must also
// SURFACE in the notes — a release that majors for a breaking change while
// its notes stay silent about it would advertise less than it ships. The
// normalized entry hoists into Breaking Changes with the subject verbatim.
func TestNotesSinceTagUnknownBreakingFallbackHoists(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":notarealmoji:! drop the legacy flag")
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes over an unknown+breaking fallback exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "## Breaking Changes") || !strings.Contains(stdout, "drop the legacy flag") {
		t.Fatalf("the breaking fallback must be hoisted into Breaking Changes:\n%s", stdout)
	}
}

// TestSinceTagResolvedPRLintNamesTheWedge pins the ratified Q1/t-2nzf error
// contract: a commit INSIDE a resolved merged PR that fails lint (a message
// that does not parse, or an unknown gitmoji) stays a hard exit 3 on the
// release walk — never a silent patch — but the error must not be the bare
// per-commit lint line: the walk found the PR itself (the caller never named
// one), and published history is immutable, so the same failure wedges EVERY
// future release until the range moves past the pull's MERGE POINT. The message
// therefore names the pull request, that merge point, and the escape (a hand-cut
// tag there, or an explicit --since-tag base naming one).
func TestSinceTagResolvedPRLintNamesTheWedge(t *testing.T) {
	for name, inner := range map[string]string{
		"malformed":      apiCommit("a1", "akira-toriyama", "no gitmoji leads this subject"),
		"unknown-etmoji": apiCommit("a1", "akira-toriyama", ":notarealmoji: tweak something"),
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			sha := squashCommit(t, dir, "Add a menu", 7)
			srv := walkServer(t, map[string]string{
				commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`,
				pullCommitsPath(7):   `[` + inner + `]`,
			})
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 3 {
				t.Fatalf("a resolved-PR lint failure exited %d, want 3 (hard, per Q1)\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("a lint failure wrote a payload:\n%s", stdout)
			}
			// A squash-merged pull has exactly one commit on main and it IS the
			// merge point, so naming the merge point still names the commit the
			// operator would reach for — the two only diverge on a merge-merged
			// pull (TestSinceTagWedgeEscapeIsTheMergePoint).
			for _, want := range []string{"akira-toriyama/glyph#7", "--since-tag", "by hand", sha[:7]} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the wedge error must carry %q (the PR, its merge point and the escape):\n%s", want, stderr)
				}
			}
			if !strings.Contains(stderr, "a1") {
				t.Errorf("the error must still name the offending commit:\n%s", stderr)
			}
		})
	}
}

// TestSinceTagWedgeEscapeIsTheOffendingCommit walks the escape the wedge error
// hands the operator and checks that the door actually opens.
//
// For a MERGE-merged pull the escape is the intuitive one — a base at or past
// the offending commit — and it is only intuitive again because of t-8xsb.
// Before mainFootprint, expanding a pull re-fetched its ENTIRE listing whenever
// its merge point was in range, so the offending commit came back over the API
// however far past it the tag sat; the hint had to send everyone to the merge
// COMMIT, throwing away every good commit in between. Now a listed commit that
// landed outside the range is dropped before it is parsed, so the first two rows
// below open (they exited 3 until this fix) and the hint names the commit.
//
// The four tags are the whole table, on one repository: pull #7 landed through
// the merge button with a non-conforming commit B1 inside it, and #8
// squash-merged afterwards so a cleared walk still has something to release.
func TestSinceTagWedgeEscapeIsTheOffendingCommit(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, "no gitmoji leads this subject", ":bug:(ui) polish the menu")
	bad, good := mp.Branch[0], mp.Branch[1]
	later := squashCommit(t, dir, "Fix a crash", 8)
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0", bad)
	testGit(t, dir, "akira-toriyama", "tag", "v0.3.0", good)
	testGit(t, dir, "akira-toriyama", "tag", "v0.4.0", mp.Merge)
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	routes := map[string]string{
		commitPullsPath(bad):      pr7,
		commitPullsPath(good):     pr7,
		commitPullsPath(mp.Merge): pr7,
		commitPullsPath(later):    `[` + apiPullRef(8, "2026-07-21T00:00:00Z", later) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(bad, "akira-toriyama", "no gitmoji leads this subject") + `,` +
			apiCommit(good, "akira-toriyama", ":bug:(ui) polish the menu") + `]`,
		pullCommitsPath(8): `[` + apiCommit("c1", "akira-toriyama", ":bug: fix a crash") + `]`,
	}
	for _, tc := range []struct {
		name     string
		tag      string
		wantCode int
		wantOut  string
	}{
		{"a base BEFORE the pull is the wedge itself", "v0.1.0", 3, ""},
		{"a tag AT the offending commit", "v0.2.0", 0, "v0.2.1\n"},
		{"a tag strictly PAST the offending commit", "v0.3.0", 0, "v0.3.1\n"},
		{"a tag at the pull's merge commit", "v0.4.0", 0, "v0.4.1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := walkServer(t, routes)
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag="+tc.tag)
			if code != tc.wantCode || stdout != tc.wantOut {
				t.Fatalf("bump --since-tag=%s = exit %d stdout %q, want %d / %q\nstderr: %s", tc.tag, code, stdout, tc.wantCode, tc.wantOut, stderr)
			}
			if tc.wantCode != 3 {
				return
			}
			env := decodeErrorEnvelope(t, stderr)
			if !strings.Contains(env.Message, bad[:7]) {
				t.Errorf("the error must still name the offending commit %.7s:\n%s", bad, env.Message)
			}
			// The escape names the OFFENDING COMMIT, because the two rows above
			// prove a base there opens the door — sending the operator to the
			// merge point instead would silently drop everything between.
			if want := "AT OR PAST " + bad[:7]; !strings.Contains(env.Message, want) {
				t.Errorf("the escape must name the offending commit (%q), the nearest base that opens the door:\n%s", want, env.Message)
			}
		})
	}
}

// TestBumpSinceTagNonVersionTag: an explicit --since-tag that is not a semver
// version still anchors the WALK at that tag, but names no version base — the
// bump falls back to the highest parseable v* tag. This is the third arm of
// the base resolution (auto / version tag / other tag), and the one a caller
// hits when releasing past a non-release tag (a marker like rc-1).
func TestBumpSinceTagNonVersionTag(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "rc-1") // same commit as v0.1.0
	sha := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=rc-1")
	if code != 0 {
		t.Fatalf("bump --since-tag=rc-1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 (walk from rc-1, step from the highest v* tag)", stdout)
	}
}

// TestSinceTagAPIFailureMidWalkPassesThrough: a NON-lint failure while
// expanding a resolved PR (a 500 from pulls/{n}/commits) must hard-fail as an
// ordinary API error (4) and pass through wedgeHint UNDECORATED — the wedge
// prose (immutable history, cut a tag past it) is for lint failures only; a
// transient server error is retryable and must never be dressed up as a
// permanent wedge.
func TestSinceTagAPIFailureMidWalkPassesThrough(t *testing.T) {
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Add a menu", 7)
	pullRef := `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case commitPullsPath(sha):
			fmt.Fprint(w, pullRef)
		case pullCommitsPath(7):
			// Retry-After: 0 keeps the client's transient-failure retries
			// (which a 5xx now triggers) instant, so the test stays fast while
			// still walking the whole retry schedule before the hard fail.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom"}`)
		default:
			t.Errorf("unexpected request %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 4 {
		t.Fatalf("a 500 mid-walk exited %d, want 4 (API)\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr)
	if env.Code != 4 || !strings.Contains(env.Message, "boom") {
		t.Fatalf("envelope = %+v, want code 4 carrying GitHub's message", env)
	}
	if !strings.Contains(env.Message, "gave up after") {
		t.Fatalf("a 5xx must be retried before the hard fail, and the message must say so:\n%s", env.Message)
	}
	if strings.Contains(env.Message, "wedge") || strings.Contains(env.Message, "by hand") {
		t.Fatalf("an API failure must not carry the lint wedge prose:\n%s", env.Message)
	}
}

// --- t-8xsb: a pull's listing is governed by the walk range ------------------
//
// A merged pull request's API commit listing is the pull's ENTIRE history and
// carries nothing about the walk's range. DESIGN §4 says exactly that, and says
// it as the reason the walk refuses to expand an UNRESOLVED pull — but the
// resolved arm expanded the listing whole regardless, so a version tag cut
// INSIDE a landed pull's footprint folded already-released commits back into the
// next release. Exit 0, empty stderr, a minor invented out of shipped work.
//
// The tests below pin the fix (mainFootprint) on both shapes that have a
// footprint, plus the controls that must not move.

// TestSinceTagDoesNotRefoldReleasedMergeMergedCommits is the reported bug: pull
// #7 lands through the merge button, and the release tag is cut at its FIRST
// branch commit — so that commit shipped, and only the second belongs to the
// next release. Before the fix this answered v0.2.0 with no diagnostic at all.
func TestSinceTagDoesNotRefoldReleasedMergeMergedCommits(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui) add a menu", ":memo: document the menu")
	shipped, pending := mp.Branch[0], mp.Branch[1]
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.1", shipped)
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		commitPullsPath(pending):  pr7,
		commitPullsPath(mp.Merge): pr7,
		pullCommitsPath(7): `[` +
			apiCommit(shipped, "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit(pending, "akira-toriyama", ":memo: document the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.1")
	if code != 1 {
		t.Fatalf("bump --since-tag=v0.1.1 = exit %d stdout %q, want 1 (only the :memo: is in range)\nstderr: %s", code, stdout, stderr)
	}
	// The drop is announced. A commit leaving the fold silently is the class of
	// failure this whole file exists to prevent — in either direction.
	if !strings.Contains(stderr, shipped[:7]) || !strings.Contains(stderr, "shipped under an earlier tag") {
		t.Errorf("the walk must name the released commit it declined to re-count:\n%s", stderr)
	}
	// The in-range half is still COUNTED — the verdict is none because a :memo:
	// is none, not because the pull was refused wholesale. A fix that dropped
	// the whole pull would pass the assertion above and fail this one.
	if !strings.Contains(stderr, "1 commit(s) participate") {
		t.Errorf("the in-range half of the pull must still participate:\n%s", stderr)
	}
}

// TestSinceTagRefoldControlWholePullInRange is the control for the test above,
// and the one that would catch an over-eager fix: with the tag BEFORE the pull,
// every commit of it is in range and the verdict must be untouched.
func TestSinceTagRefoldControlWholePullInRange(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui) add a menu", ":memo: document the menu")
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): pr7,
		commitPullsPath(mp.Branch[1]): pr7,
		commitPullsPath(mp.Merge):     pr7,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":memo: document the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("the whole pull is in range and must still release: exit %d\nstderr: %s", code, stderr)
	}
	if len(shas) != 2 {
		t.Errorf("both commits must participate, got %v\nstderr: %s", shas, stderr)
	}
}

// TestSinceTagRebaseMergeUsesOnMainSHAs covers the shape a naive ancestry check
// cannot: a REBASE-merge rewrites every commit, so the pull's listing reports
// pre-rebase SHAs that exist on no branch and can never equal a walked one. The
// walk aligns the listing against the first-parent run ending at the canonical
// commit — verified message by message — which fixes the range AND a defect of
// its own: before this, the notes cited SHAs the repository does not contain.
func TestSinceTagRebaseMergeUsesOnMainSHAs(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu")
	r1 := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "akira-toriyama", ":memo: document the menu")
	r2 := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.1", r1)
	// GitHub names the LAST replayed commit as merge_commit_sha, and the listing
	// still reports the branch's original, pre-rebase SHAs.
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", r2) + `]`
	listing := `[` +
		apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
		apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":memo: document the menu") + `]`
	routes := map[string]string{
		commitPullsPath(r1): pr7,
		commitPullsPath(r2): pr7,
		pullCommitsPath(7):  listing,
	}

	t.Run("tag inside the rebased run drops the released commit", func(t *testing.T) {
		srv := walkServer(t, routes)
		usePR(t, srv)
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.1")
		if code != 1 {
			t.Fatalf("bump --since-tag=v0.1.1 = exit %d stdout %q, want 1\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("the whole run in range still releases, citing real SHAs", func(t *testing.T) {
		srv := walkServer(t, routes)
		usePR(t, srv)
		t.Chdir(dir)
		code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.1.0")
		if code != 0 {
			t.Fatalf("the whole pull is in range: exit %d\nstderr: %s", code, stderr)
		}
		// The assertion that would have caught the phantom-SHA defect: every
		// reported commit must be one this repository actually has.
		for _, sha := range shas {
			if strings.HasPrefix(sha, "pre") {
				t.Errorf("the verdict cites a pre-rebase SHA the repository does not contain: %s", sha)
			}
			testGit(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
		}
	})
}

// The two tests below are the shapes that break "a listing is either ALL
// verbatim or ALL rewritten" — the reading under which the alignment used to run
// only when nothing had been placed under its own sha (t-7h15). Both were
// measured against the real API on akira-toriyama/glyph-test before they were
// written here, because the premise is GitHub's behaviour and not glyph's: a
// closed loop of fixtures would have agreed with whatever the code assumed.

// TestSinceTagStackedRebaseMergePlacesTheRewrittenHalf covers the MIXED listing.
//
// A stacked pull request carries its base pull's commits, and GitHub computes the
// listing against a STORED base sha rather than re-deriving it — so when the base
// pull lands through the merge button, its commit is on main under its own sha
// AND still in the stacked pull's listing (measured: glyph-test#28 kept listing
// ed1bd9e after #27 merged it verbatim). Rebase-merge the stacked pull and the
// listing is half placeable by ancestry, half only by alignment.
//
// Before the fix the first half short-circuited the second: every REWRITTEN entry
// kept an empty landing site, which foldPull reads as "the pull alone governs" and
// folds whatever the range says. Measured here: minor out of a :sparkles: released
// one tag ago, exit 0, no warning, and the notes citing pre-rebase shas main does
// not contain. It is t-8xsb through the front door, on the shape t-8xsb's own fix
// introduced.
func TestSinceTagStackedRebaseMergePlacesTheRewrittenHalf(t *testing.T) {
	dir, _ := testRepo(t)
	// The base pull's commit, landed verbatim by the merge button.
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu")
	base := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	// The stacked pull's two commits, as the rebase rewrote them onto main. The
	// tag sits at the first, so only the second belongs to the next release.
	testCommit(t, dir, "akira-toriyama", ":sparkles:(core) add the fold")
	shipped := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0", shipped)
	testCommit(t, dir, "akira-toriyama", ":bug:(ui) fix the menu")
	pending := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	srv := walkServer(t, map[string]string{
		commitPullsPath(pending): `[` + apiPullRef(7, "2026-07-20T00:00:00Z", pending) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(base, "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(core) add the fold") + `,` +
			apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":bug:(ui) fix the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.2.0", "--json")
	if code != 0 {
		t.Fatalf("the pending :bug: is in range and must release: exit %d\nstderr: %s", code, stderr)
	}
	// The level is the whole point: the rewritten entry that shipped under v0.2.0
	// must not be counted, and it carries the only :sparkles: in the range.
	if len(shas) != 1 || shas[0] != pending {
		t.Errorf("verdict counts %v, want [%.7s] alone — the released half of the rebased run is being folded back in", shas, pending)
	}
	// Every cited sha has to be one this repository holds. Placing the rewritten
	// half is what retires the pre-rebase shas as much as it fixes the range.
	for _, sha := range shas {
		testGit(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
	}
	if !strings.Contains(stderr, "shipped under an earlier tag") {
		t.Errorf("the walk must name the released commits it declined to re-count:\n%s", stderr)
	}
}

// TestSinceTagRebaseOverABaseMergeCommitStillAligns covers the other way the
// all-or-nothing reading fails: a rebase does not replay everything it is given.
//
// "Merge branch 'main' into <topic>" is an ordinary thing to have on a branch,
// GitHub lists that merge commit with the pull's own commits, and it PERMITS
// rebase-merge over it — measured on glyph-test#26: three listed commits, two
// landed, merge_commit_sha naming the last of the two. Counting the merge commit
// into the alignment window therefore made the length wrong, and the alignment
// was abandoned WHOLE, taking the two entries it could have placed with it. The
// listing then governed itself again: phantom shas, and a :sparkles: from before
// the tag folded back in.
func TestSinceTagRebaseOverABaseMergeCommitStillAligns(t *testing.T) {
	dir, _ := testRepo(t)
	// main moved while the pull was open — this is what the author merged in, and
	// it is what the alignment window runs off the end into if the merge commit is
	// left holding a position.
	testCommit(t, dir, "akira-toriyama", ":memo: note something on main")
	// The pull's two commits as the rebase replayed them; the tag sits at the first.
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu")
	shipped := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0", shipped)
	testCommit(t, dir, "akira-toriyama", ":bug:(ui) fix the menu")
	pending := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	srv := walkServer(t, map[string]string{
		commitPullsPath(pending): `[` + apiPullRef(7, "2026-07-20T00:00:00Z", pending) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":bug:(ui) fix the menu") + `,` +
			apiMergeCommit("pre3cccccccccccccccccccccccccccccccccccc", "akira-toriyama", "Merge branch 'main' into topic-7") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.2.0", "--json")
	if code != 0 {
		t.Fatalf("the pending :bug: is in range and must release: exit %d\nstderr: %s", code, stderr)
	}
	if len(shas) != 1 || shas[0] != pending {
		t.Errorf("verdict counts %v, want [%.7s] alone — the base-merge commit is displacing the alignment", shas, pending)
	}
	for _, sha := range shas {
		testGit(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
	}
	if !strings.Contains(stderr, "shipped under an earlier tag") {
		t.Errorf("the walk must name the released commit it declined to re-count:\n%s", stderr)
	}
}

// squashOf lands ONE commit on main shaped exactly the way GitHub writes a
// squash merge of a MULTI-commit pull request under COMMIT_OR_PR_TITLE: the
// subject is the PR TITLE plus (#N), whatever that title happens to be. It is
// the sibling of squashCommit, which fixes the non-gitmoji shape; here the title
// is the variable under test, because nothing lints it — the commit-lint job
// runs `glyph lint --range "$BASE..$HEAD"`, i.e. over the pull's COMMITS.
func squashOf(t *testing.T, dir, title string, number int) string {
	t.Helper()
	testCommit(t, dir, "akira-toriyama", fmt.Sprintf("%s (#%d)", title, number))
	return testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestSinceTagSquashMultiCommitDivergesWhenAPIDark pins what squash actually
// buys, against the claim doctor used to make. Squash guarantees a pull request
// resolves ALL-OR-NOTHING — one commit on main, and it IS the merge point — but
// it does NOT guarantee the same verdict with the API dark as with it answering.
// A multi-commit squash's subject is the PR title, and the fallback classifies
// that one subject instead of the pull's commits: a :sparkles:+:bug: pull titled
// :bug: reads minor live and patch dark.
//
// This is the sentence that shipped false twice, so it owns a test rather than a
// paragraph. Do NOT golden doctor's prose to pin it — pin the walk it describes.
func TestSinceTagSquashMultiCommitDivergesWhenAPIDark(t *testing.T) {
	for name, tc := range map[string]struct {
		dark bool
		want string
	}{
		"the API answers: the pull's own commits decide": {false, "v0.2.0\n"},
		"the API is dark: the PR title decides":          {true, "v0.1.1\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			sha := squashOf(t, dir, ":bug:(auth) fix a token refresh", 1)
			routes := map[string]string{
				commitPullsPath(sha): `[` + apiPullRef(1, "2026-07-20T00:00:00Z", sha) + `]`,
				pullCommitsPath(1): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles:(auth) add a refresh endpoint") + `,` +
					apiCommit("a2", "akira-toriyama", ":bug:(auth) fix a token refresh") + `]`,
			}
			if tc.dark {
				routes[commitPullsPath(sha)] = apiUnknownSHA
				delete(routes, pullCommitsPath(1)) // asking for it would fail the test
			}
			usePR(t, walkServer(t, routes))
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
			if code != 0 {
				t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout = %q, want %q — squash bounds an API-dark window to one wrong LEVEL, it does not close it", stdout, tc.want)
			}
		})
	}
}

// TestSinceTagNonGitmojiPRTitleCountsNoneWhenDark is the same divergence at its
// floor. A PR title carrying no gitmoji at all is perfectly legal — no gate reads
// it — so under COMMIT_OR_PR_TITLE a multi-commit squash can put an unparseable
// subject on main. Live the pull's commits still decide; dark there is nothing to
// classify and the whole release counts none.
func TestSinceTagNonGitmojiPRTitleCountsNoneWhenDark(t *testing.T) {
	dir, _ := testRepo(t)
	sha := squashOf(t, dir, "Add a refresh endpoint", 6)
	live := map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(6, "2026-07-20T00:00:00Z", sha) + `]`,
		pullCommitsPath(6):   `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(auth) add a refresh endpoint") + `]`,
	}
	usePR(t, walkServer(t, live))
	t.Chdir(dir)
	if code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0"); code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("with the API answering: exit %d stdout %q, want 0 / v0.2.0\nstderr: %s", code, stdout, stderr)
	}

	usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): apiUnknownSHA}))
	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 1 || stdout != "" {
		t.Fatalf("with the API dark: exit %d stdout %q, want 1 / empty — an unlinted PR title is what the fallback reads\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "does not parse") {
		t.Fatalf("the loss must name the subject that would not parse:\n%s", stderr)
	}
}

// TestSinceTagMergeCommitReproducesVerdictWhenFullyDark pins a claim by DELETING
// one: doctor and DESIGN §7 used to say a merge-merged PR counts none when the
// walk falls back, because the fallback skips the merge commit on its parent
// count. It does skip it — and it costs the pull request nothing, because
// gitsource.Log runs WITHOUT --first-parent: the merged branch's commits are in
// the range beside their merge point, gitmoji intact, and each classifies itself.
//
// So this test fails the moment someone re-asserts that sentence, and it fails
// just as loudly if anyone adds --first-parent to gitsource.Log to make the
// sentence true — which was the tempting "fix" and would have undone t-7zt7.
func TestSinceTagMergeCommitReproducesVerdictWhenFullyDark(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 3, ":sparkles:(ui) add a menu", ":memo: document the menu")
	ref := `[` + apiPullRef(3, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	live := map[string]string{
		commitPullsPath(mp.Merge): ref,
		pullCommitsPath(3): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":memo: document the menu") + `]`,
	}
	dark := map[string]string{commitPullsPath(mp.Merge): apiUnknownSHA}
	for _, b := range mp.Branch {
		live[commitPullsPath(b)] = ref
		dark[commitPullsPath(b)] = apiUnknownSHA
	}

	usePR(t, walkServer(t, live))
	t.Chdir(dir)
	if code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0"); code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("with the API answering: exit %d stdout %q, want 0 / v0.2.0\nstderr: %s", code, stdout, stderr)
	}

	usePR(t, walkServer(t, dark))
	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("fully dark: exit %d stdout %q, want 0 / v0.2.0 — the branch commits are on main and classify themselves\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, mp.Branch[0][:7]) || !strings.Contains(stderr, "classifying its own message") {
		t.Fatalf("the fallback must say it classified each branch commit itself:\n%s", stderr)
	}
}

// TestSinceTagFallbackOnlyOnUnknownCommit is the guard that makes "an outage
// reaches the fallback" un-writable again. Exactly ONE status branches to
// fallbackCommit — 422, github.IsCommitUnknown, the sha GitHub has not indexed
// yet. A 403 rate limit, a 404, a 429 and a 5xx that outlives the retry schedule
// all leave walkSince as errors and exit 4: glyph observed nothing, so it
// classifies nothing. Retry-After: 0 keeps the retryable arms instant (the same
// device TestSinceTagAPIFailureMidWalkPassesThrough uses) — the schedule's
// LENGTH is internal/github's business, its OUTCOME is this test's.
func TestSinceTagFallbackOnlyOnUnknownCommit(t *testing.T) {
	for name, status := range map[string]int{
		"403 rate limit":                   http.StatusForbidden,
		"404 not found":                    http.StatusNotFound,
		"429 too many requests":            http.StatusTooManyRequests,
		"500 outliving the retry schedule": http.StatusInternalServerError,
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testRepo(t)
			squashCommit(t, dir, "Fix a crash", 8)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"message":"synthetic %d"}`, status)
			}))
			t.Cleanup(srv.Close)
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
			if code != 4 || stdout != "" {
				t.Fatalf("a %d exited %d with stdout %q, want 4 / empty — only 422 is an API-dark walk\nstderr: %s", status, code, stdout, stderr)
			}
			if strings.Contains(stderr, "classifying its own message") {
				t.Fatalf("a %d must never reach the fallback — the walk observed nothing about the commit:\n%s", status, stderr)
			}
			if env := decodeErrorEnvelope(t, stderr); env.Code != 4 {
				t.Fatalf("envelope code = %d, want 4", env.Code)
			}
		})
	}
}

// TestSinceTagRebaseLagCountsAReplayedCommitOnce pins what a rebase costs inside
// the API-lag window, on both sides of the line.
//
// A rebase writes NEW shas, and the pull's listing still reports the pre-rebase
// ones, so the walk-wide dedup has nothing to match on: a replayed commit
// classified during the lag used to be folded in a SECOND time when the last
// replayed commit expanded the pull, rendering the same change twice with
// nothing that said so. t-8xsb closed that by aligning the listing against the
// first-parent run ending at the canonical commit — but the alignment is
// verified message by message and abandoned whole if any differs, so a rebase
// the walk cannot align still double-folds. Both halves are measured here,
// because the doctor advice text and DESIGN §7 make a claim about each.
func TestSinceTagRebaseLagCountsAReplayedCommitOnce(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu")
	r1 := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "akira-toriyama", ":memo: document the menu")
	r2 := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	// GitHub names the LAST replayed commit as merge_commit_sha, and the pull's
	// listing still carries the ORIGINAL pre-rebase shas.
	t.Run("the alignment holds, so it is counted once and said out loud", func(t *testing.T) {
		srv := walkServer(t, map[string]string{
			commitPullsPath(r1): apiUnknownSHA,
			commitPullsPath(r2): `[` + apiPullRef(4, "2026-07-20T00:00:00Z", r2) + `]`,
			pullCommitsPath(4): `[` +
				apiCommit("orig111", "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
				apiCommit("orig222", "akira-toriyama", ":memo: document the menu") + `]`,
		})
		usePR(t, srv)
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "notes", "--since-tag=v0.1.0")
		if code != 0 {
			t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
		}
		if got := strings.Count(stdout, "add a menu"); got != 1 {
			t.Fatalf("the lagging replayed commit renders %d time(s), want 1:\n%s", got, stdout)
		}
		// Counted once is not enough — the walk must SAY it deduped, or the
		// difference between "deduped" and "never seen" is invisible.
		if !strings.Contains(stderr, "counted once") {
			t.Errorf("the dedup must announce itself:\n%s", stderr)
		}
	})

	t.Run("the alignment cannot be verified, so the double fold survives", func(t *testing.T) {
		srv := walkServer(t, map[string]string{
			commitPullsPath(r1): apiUnknownSHA,
			commitPullsPath(r2): `[` + apiPullRef(5, "2026-07-20T00:00:00Z", r2) + `]`,
			pullCommitsPath(5): `[` +
				apiCommit("orig111", "akira-toriyama", ":sparkles:(ui) add a different menu") + `,` +
				apiCommit("orig222", "akira-toriyama", ":memo: document the menu") + `]`,
		})
		usePR(t, srv)
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "notes", "--since-tag=v0.1.0")
		if code != 0 {
			t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
		}
		// This is the residual the advice text and DESIGN §7 both name. A walk
		// that closes it should DELETE those clauses and this subtest with them.
		if got := strings.Count(stdout, "add a"); got != 2 {
			t.Fatalf("an unalignable rebase still double-folds; got %d rendering(s), want 2:\n%s", got, stdout)
		}
	})
}

// TestLatestVersionTagComparesVersionsNotRefnames: the walk base and the
// version base both come from this resolver, and it must answer with the
// highest VERSION rather than with whatever tag git listed first.
//
// git's --sort=-v:refname is a refname sort. It compares digit runs
// numerically, which is why it looks like a version sort, but it still starts
// on the leading byte — so every v-prefixed tag lands above every bare one
// ('v' > '1'), and ParseVersion accepts both spellings (^v?…). On the tags
// below git reports v0.0.2 first while the highest version is 100.0.0; taking
// git's first parseable entry moved the release two tags backwards in silence.
func TestLatestVersionTagComparesVersionsNotRefnames(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	for _, tag := range []string{"v0.0.1", "v0.0.2", "9.9.9", "100.0.0"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}
	t.Chdir(dir)

	// The premise: git really does report a lower tag first here. Without this
	// the assertion below could pass on a git whose sort changed, proving
	// nothing.
	if first := testGit(t, dir, "akira-toriyama", "tag", "--list", "--sort=-v:refname"); !strings.HasPrefix(first, "v0.1.0\n") {
		t.Fatalf("premise gone: git --sort=-v:refname now leads with %q, so this test no longer exercises the refname/version gap", strings.SplitN(first, "\n", 2)[0])
	}

	tag, v, err := latestVersionTag(t.Context())
	if err != nil {
		t.Fatalf("latestVersionTag: %v", err)
	}
	if tag != "100.0.0" {
		t.Errorf("latestVersionTag tag = %q, want %q (the highest version, not git's first entry)", tag, "100.0.0")
	}
	if want := (bump.Version{Major: 100}); v != want {
		t.Errorf("latestVersionTag version = %v, want %v", v, want)
	}
}

// TestLatestVersionTagBreaksATieOnGitsOrder: one version spelled twice is not
// a version question, so the answer is whatever git listed first — pinned so
// the resolver stays deterministic rather than depending on map or loop order.
//
// This one passes against the pre-change tree by design: the tie-break is the
// half of the old behaviour the fix deliberately KEEPS, and a test that pins it
// is what makes a later "just sort the tags ourselves" visible.
//
// bite-exempt: pins the behaviour the fix preserves, not the behaviour it changes
func TestLatestVersionTagBreaksATieOnGitsOrder(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	for _, tag := range []string{"1.2.3", "v1.2.3"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}
	t.Chdir(dir)

	tag, _, err := latestVersionTag(t.Context())
	if err != nil {
		t.Fatalf("latestVersionTag: %v", err)
	}
	if tag != "v1.2.3" {
		t.Errorf("latestVersionTag tag = %q, want %q (git lists it first among the two spellings)", tag, "v1.2.3")
	}
}

// TestSinceTagAutoWalksFromTheHighestVersion carries the same defect up to the
// command surface, where it is a wrong RELEASE and not merely a wrong string:
// a bare --since-tag resolves the base, and a base two tags behind re-folds
// commits an earlier tag already shipped.
func TestSinceTagAutoWalksFromTheHighestVersion(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) ship the released feature")
	testGit(t, dir, "akira-toriyama", "tag", "100.0.0")
	testCommit(t, dir, "akira-toriyama", ":bug:(ui) fix the unreleased crash")
	t.Chdir(dir)

	revRange, base, err := sinceTagRange(t.Context(), sinceTagAuto)
	if err != nil {
		t.Fatalf("sinceTagRange: %v", err)
	}
	if revRange != "100.0.0..HEAD" {
		t.Errorf("walk range = %q, want %q", revRange, "100.0.0..HEAD")
	}
	if base == nil || *base != (bump.Version{Major: 100}) {
		t.Errorf("version base = %v, want v100.0.0", base)
	}
}

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
	dir, _ := testRepo(t) // root commit ":tada: begin the project"
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu")
	t.Chdir(dir)
	canonical := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	// Three listed commits against a two-commit main, oldest first — the order
	// FirstParentLog returns. The SHAs are well-formed and absent from this
	// repository, which is what a rebase-merge's pre-rebase listing looks like
	// from here, so `Have` says no to all three.
	listing := []gitsource.RawCommit{
		{SHA: "1111111111111111111111111111111111111111", Message: ":tada: begin the project"},
		{SHA: "2222222222222222222222222222222222222222", Message: ":sparkles:(ui) add a menu"},
		{SHA: "3333333333333333333333333333333333333333", Message: ":bug:(ui) fix the menu"},
	}

	landed, err := mainFootprint(t.Context(), canonical, listing)
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

// TestMainFootprintEmptyListingAsksGitNothing covers the other early return, and
// pins the property that makes it more than a micro-optimisation: with no commits
// to place there is nothing to ask git ABOUT, and the canonical commit is a sha
// this checkout may not hold. Falling through would run `git log` against it and
// turn "this pull listed nothing" into an exit-4 API/git error mid-walk.
//
// The unknown canonical is the assertion. A guard removed here does not merely
// cost a subprocess: it fails the run.
//
// The guard it now exercises is the ALIGNMENT WINDOW's, not a private one for the
// empty listing. Once the window became "what is left after ancestry placed what
// it could" (t-7h15), an empty listing and a fully placed merge-button listing
// became the same state — nothing an alignment could answer about — and a second
// guard ahead of it would be a decision no test could reach.
func TestMainFootprintEmptyListingAsksGitNothing(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	landed, err := mainFootprint(t.Context(), "4444444444444444444444444444444444444444", nil)
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
		b1 := commit(t, dir, "fix.txt", ":bug:(ui) fix the menu")
		b2 := commit(t, dir, "speed.txt", ":zap:(ui) speed up the menu")
		// The rebase-merge replayed both onto main; GitHub names the last
		// replayed commit as merge_commit_sha, and the listing keeps b1 and b2.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		commit(t, dir, "fix.txt", ":bug:(ui) fix the menu")                     // b1'
		canonical := commit(t, dir, "speed.txt", ":zap:(ui) speed up the menu") // b2'
		// A later pull branches from the ORIGINAL b1 and lands through the
		// merge button — now b1 itself is an ancestor of HEAD, and the same
		// change sits on main twice: as b1 (that pull) and as b1' (this one).
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-3", b1)
		commit(t, dir, "mention.txt", ":memo: mention the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		testGit(t, dir, "akira-toriyama", "merge", "-q", "--no-ff",
			"-m", "Merge pull request #3 from akira-toriyama/topic-3", "topic-3")
		t.Chdir(dir)

		landed, err := mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: b1, Parents: 1, Message: ":bug:(ui) fix the menu"},
			{SHA: b2, Parents: 1, Message: ":zap:(ui) speed up the menu"},
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
		b1 := commit(t, dir, "fix.txt", ":bug:(ui) fix the menu")
		b2 := commit(t, dir, "speed.txt", ":zap:(ui) speed up the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		commit(t, dir, "fix.txt", ":bug:(ui) fix the menu")                     // b1'
		canonical := commit(t, dir, "speed.txt", ":zap:(ui) speed up the menu") // b2'
		// The later pull branches from b2, the LAST listed commit — and a
		// branch carries its ancestry, so landing it lands b1 as well. Step 1
		// places the whole listing and the rebase run plays no part.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-3", b2)
		commit(t, dir, "mention.txt", ":memo: mention the menu")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		testGit(t, dir, "akira-toriyama", "merge", "-q", "--no-ff",
			"-m", "Merge pull request #3 from akira-toriyama/topic-3", "topic-3")
		t.Chdir(dir)

		landed, err := mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: b1, Parents: 1, Message: ":bug:(ui) fix the menu"},
			{SHA: b2, Parents: 1, Message: ":zap:(ui) speed up the menu"},
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
		C := commit(t, dir, "shared.txt", ":bug:(core) fix the hotfix")
		testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
		// The branch cherry-picked the same patch under its own sha, added one
		// commit of its own, and was rebase-merged: the rebase found c's patch
		// already upstream, dropped it, and replayed only d.
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic-2", root)
		c := commit(t, dir, "shared.txt", ":bug:(core) fix the hotfix")
		d := commit(t, dir, "add.txt", ":sparkles:(core) add a thing")
		testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
		canonical := commit(t, dir, "add.txt", ":sparkles:(core) add a thing") // d'
		t.Chdir(dir)

		landed, err := mainFootprint(t.Context(), canonical, []gitsource.RawCommit{
			{SHA: c, Parents: 1, Message: ":bug:(core) fix the hotfix"},
			{SHA: d, Parents: 1, Message: ":sparkles:(core) add a thing"},
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
