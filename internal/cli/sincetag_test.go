package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
// `Revert "..." (#N)` — the same prefix bump.Excluded uses to skip raw
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

// TestSinceTagWedgeEscapeIsTheMergePoint walks the escape the wedge error hands
// the operator and checks that the door actually opens. It does not for a
// MERGE-merged pull under the old wording ("cut a release tag past it by hand",
// meaning past the offending commit): expanding a pull re-fetches its ENTIRE
// listing whenever its merge point is in range, so the offending commit comes
// back over the API however far past it the tag sits. Only a base at or past the
// pull's MERGE COMMIT stops the walk from resolving the pull at all.
//
// The three tags below are the whole table, on one repository: pull #7 landed
// through the merge button with a non-conforming commit B1 inside it, and #8
// squash-merged afterwards so a cleared walk still has something to release.
// A squash-merged pull has exactly one commit on main, which IS its merge point,
// so "past the commit" and "past the merge point" coincide there — which is why
// the old wording held until this branch made the other shape reachable.
func TestSinceTagWedgeEscapeIsTheMergePoint(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, "no gitmoji leads this subject", ":bug:(ui) polish the menu")
	bad, good := mp.Branch[0], mp.Branch[1]
	later := squashCommit(t, dir, "Fix a crash", 8)
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0", bad)
	testGit(t, dir, "akira-toriyama", "tag", "v0.3.0", good)
	testGit(t, dir, "akira-toriyama", "tag", "v0.4.0", mp.Merge)
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	routes := map[string]string{
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
		{"a tag AT the offending commit", "v0.2.0", 3, ""},
		{"a tag strictly PAST the offending commit — what the old escape said to do", "v0.3.0", 3, ""},
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
			// The lint line still names the offending commit (that is the
			// failure), but the ESCAPE must send the operator to the merge
			// commit — the only base these two subtests prove clears it.
			if !strings.Contains(env.Message, bad[:7]) {
				t.Errorf("the error must still name the offending commit %.7s:\n%s", bad, env.Message)
			}
			if want := "AT OR PAST " + mp.Merge[:7]; !strings.Contains(env.Message, want) {
				t.Errorf("the escape must name the merge point (%q), the only base that opens the door:\n%s", want, env.Message)
			}
			if strings.Contains(env.Message, "past it by hand") {
				t.Errorf("the old escape (a tag past the offending commit) is exactly what the two failing subtests disprove:\n%s", env.Message)
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
