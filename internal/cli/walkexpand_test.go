package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// This file pins the walk's resolution and expansion of every merge shape
// (DESIGN §4; internal/walk/expand.go: visit, expand, mainFootprint,
// mergedPullFor): a squash, a merge commit, a rebase and a stack each resolve
// to the pull that landed them and fold the pull's own commits, governed by
// where each listed commit landed on the released branch. The doubles every
// walk test shares live at the top — the API stand-in, and the commit shapes
// a squash and the merge button write.

// apiUnknownSHA is the route body meaning "answer this path the way GitHub
// answers for a commit it does not know yet": 422, DESIGN §4's API lag. A
// response BODY cannot express a status code, so a sentinel does — the release
// job runs seconds after the merge, and the walk outrunning the API is a shape
// several fallback tests (walkfallback_test.go) must stub next to ordinary
// ones in the same routes map.
const apiUnknownSHA = "\x00 422 unknown sha"

// walkServer stands in for api.github.com during a release walk: routes maps
// each expected request path to its response body, and any request outside the
// map fails the test — which is also how a test proves a SHA was NEVER asked
// about (bot commits must stay off the API).
func walkServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(walkHandler(t, routes))
	t.Cleanup(srv.Close)
	return srv
}

// walkHandler is walkServer's body, split out so recordingWalkServer can
// count what it answers.
func walkHandler(t *testing.T, routes map[string]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// recordingWalkServer is walkServer plus the request paths it answered, in
// order — the positive control a "these files are asked for" claim needs,
// since a route nobody requests is silent.
func recordingWalkServer(t *testing.T, routes map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	h := walkHandler(t, routes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
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

// squashCommit lands one commit on main shaped the way a squash merge lands
// when its subject is the PR title — the title plus (#N), what
// COMMIT_OR_PR_TITLE writes for a multi-commit pull (a single-commit pull
// keeps its own subject) — and returns its SHA. With the API answering, the
// walk folds the pull's own commits and never classifies this subject (the
// squash safety the tests pin); dark, the fallback has nothing else to read
// (walkfallback_test.go's divergence tests).
func squashCommit(t *testing.T, dir string, title string, number int) string {
	t.Helper()
	testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf("%s (#%d)", title, number))
	return testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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
	testutil.Git(t, dir, author, "checkout", "-q", "-b", branch)
	mp := mergePoint{}
	for _, m := range messages {
		testutil.Commit(t, dir, author, m)
		mp.Branch = append(mp.Branch, testutil.Git(t, dir, author, "rev-parse", "HEAD"))
	}
	testutil.Git(t, dir, author, "checkout", "-q", "main")
	testutil.Git(t, dir, merger, "merge", "-q", "--no-ff", "-m", subject, branch)
	mp.Merge = testutil.Git(t, dir, author, "rev-parse", "HEAD")
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
// :sparkles:^ in PR 7 beats the :bug:~ in PR 8), so the verdict is a minor —
// even though neither squash subject parses as a gitmoji commit at all.
func TestBumpSinceTagExpandsSquashCommits(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("a1", "akira-toriyama", ":memo:= document the menu") + `,` +
			apiCommit("a2", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:(api)~ stop dropping the last page") + `]`,
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
		pullCommitsPath(7): `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
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
	testutil.Commit(t, dir, "dependabot[bot]", ":robot:= chore(fleet): sync a file")
	sha1 := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(3, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(3):    `[` + apiCommit("c1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump --since-tag = exit %d stdout %q, want 0 / v0.1.1 (the bot commit must be excluded pre-API)", code, stdout)
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
	testutil.Commit(t, dir, "akira-toriyama", `Revert "Add a menu" (#9)`)
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(9, "2026-07-15T00:00:00Z", sha) + `]`,
		pullCommitsPath(9):   `[` + apiCommit("r1", "akira-toriyama", ":rewind:~ revert the menu") + `]`,
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

// TestSinceTagRebaseMergedPRDoesNotDoubleCount: on a rebase-merge, every
// rebased commit is associated with the merged PR but only the LAST one equals
// merge_commit_sha. The earlier ones are covered by that PR — falling back on
// them would count each change twice (once rebased, once via the PR's
// expansion) and spray direct-push warnings over a perfectly normal merge.
func TestSinceTagRebaseMergedPRDoesNotDoubleCount(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
	shaA := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix the menu")
	shaB := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(shaA): `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`,
		commitPullsPath(shaB): `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `,` +
			apiCommit("b1", "akira-toriyama", ":bug:~ fix the menu") + `]`,
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
	mp := mergePR(t, dir, "akira-toriyama", 7, ":memo:= document the menu", ":sparkles:(ui)^ add a menu")
	ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		// `git log` (no --first-parent) lists the merged commits beside the
		// merge point, and GitHub associates each of them with the same PR —
		// but only the merge commit equals its merge_commit_sha.
		commitPullsPath(mp.Branch[0]): ref,
		commitPullsPath(mp.Branch[1]): ref,
		commitPullsPath(mp.Merge):     ref,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":memo:= document the menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":sparkles:(ui)^ add a menu") + `]`,
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
		"Add a menu (#6)", ":bug:(ui)~ polish the menu")
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
		pullCommitsPath(6):        `[` + apiCommit("i1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(sub, "akira-toriyama", "Add a menu (#6)") + `,` +
			apiCommit(rest, "akira-toriyama", ":bug:(ui)~ polish the menu") + `]`,
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
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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
	mp := mergePR(t, dir, "akira-toriyama", 7, ":memo:= document the menu", ":sparkles:(ui)^ add a menu")
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): `[]`, // not associated (yet)
		commitPullsPath(mp.Branch[1]): `[]`,
		commitPullsPath(mp.Merge):     `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":memo:= document the menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":sparkles:(ui)^ add a menu") + `]`,
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
	mp := mergeInto(t, dir, "akira-toriyama", "dependabot[bot]", prTopic(7), prMergeSubject(7), ":bug:~ fix a crash")
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `,` + // the base PR's commit riding along
			apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
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

// TestSinceTagUnmergedAssociationFallsBack: an association that is not the
// merged match (API lag right after a merge, or a mention from an open PR) is
// no resolution at all — same fallback as no association.
func TestSinceTagUnmergedAssociationFallsBack(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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

// TestSinceTagAPIFailureMidWalkPassesThrough: a NON-lint failure while
// expanding a resolved PR (a 500 from pulls/{n}/commits) must hard-fail as an
// ordinary API error (4) and pass through UNDECORATED — the wedge
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
	mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui)^ add a menu", ":memo:= document the menu")
	shipped, pending := mp.Branch[0], mp.Branch[1]
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.1", shipped)
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		commitPullsPath(pending):  pr7,
		commitPullsPath(mp.Merge): pr7,
		pullCommitsPath(7): `[` +
			apiCommit(shipped, "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit(pending, "akira-toriyama", ":memo:= document the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.1")
	if code != 1 {
		t.Fatalf("bump --since-tag=v0.1.1 = exit %d stdout %q, want 1 (only the :memo:= is in range)\nstderr: %s", code, stdout, stderr)
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
	mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui)^ add a menu", ":memo:= document the menu")
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	srv := walkServer(t, map[string]string{
		commitPullsPath(mp.Branch[0]): pr7,
		commitPullsPath(mp.Branch[1]): pr7,
		commitPullsPath(mp.Merge):     pr7,
		pullCommitsPath(7): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":memo:= document the menu") + `]`,
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
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	r1 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the menu")
	r2 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.1", r1)
	// GitHub names the LAST replayed commit as merge_commit_sha, and the listing
	// still reports the branch's original, pre-rebase SHAs.
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", r2) + `]`
	listing := `[` +
		apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
		apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":memo:= document the menu") + `]`
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
			testutil.Git(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
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
// kept an empty landing site, which the walk's expand reads as "the pull alone governs" and
// folds whatever the range says. Measured here: minor out of a :sparkles:^ released
// one tag ago, exit 0, no warning, and the notes citing pre-rebase shas main does
// not contain. It is t-8xsb through the front door, on the shape t-8xsb's own fix
// introduced.
func TestSinceTagStackedRebaseMergePlacesTheRewrittenHalf(t *testing.T) {
	dir, _ := testRepo(t)
	// The base pull's commit, landed verbatim by the merge button.
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	// The stacked pull's two commits, as the rebase rewrote them onto main. The
	// tag sits at the first, so only the second belongs to the next release.
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(core)^ add the fold")
	shipped := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.2.0", shipped)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:(ui)~ fix the menu")
	pending := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	srv := walkServer(t, map[string]string{
		commitPullsPath(pending): `[` + apiPullRef(7, "2026-07-20T00:00:00Z", pending) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(base, "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(core)^ add the fold") + `,` +
			apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":bug:(ui)~ fix the menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.2.0", "--json")
	if code != 0 {
		t.Fatalf("the pending :bug:~ is in range and must release: exit %d\nstderr: %s", code, stderr)
	}
	// The level is the whole point: the rewritten entry that shipped under v0.2.0
	// must not be counted, and it carries the only :sparkles:^ in the range.
	if len(shas) != 1 || shas[0] != pending {
		t.Errorf("verdict counts %v, want [%.7s] alone — the released half of the rebased run is being folded back in", shas, pending)
	}
	// Every cited sha has to be one this repository holds. Placing the rewritten
	// half is what retires the pre-rebase shas as much as it fixes the range.
	for _, sha := range shas {
		testutil.Git(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
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
// listing then governed itself again: phantom shas, and a :sparkles:^ from before
// the tag folded back in.
func TestSinceTagRebaseOverABaseMergeCommitStillAligns(t *testing.T) {
	dir, _ := testRepo(t)
	// main moved while the pull was open — this is what the author merged in, and
	// it is what the alignment window runs off the end into if the merge commit is
	// left holding a position.
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= note something on main")
	// The pull's two commits as the rebase replayed them; the tag sits at the first.
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	shipped := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.2.0", shipped)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:(ui)~ fix the menu")
	pending := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	srv := walkServer(t, map[string]string{
		commitPullsPath(pending): `[` + apiPullRef(7, "2026-07-20T00:00:00Z", pending) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("pre1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit("pre2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "akira-toriyama", ":bug:(ui)~ fix the menu") + `,` +
			apiMergeCommit("pre3cccccccccccccccccccccccccccccccccccc", "akira-toriyama", "Merge branch 'main' into topic-7") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, shas, stderr := verdictSHAs(t, "bump", "--since-tag=v0.2.0", "--json")
	if code != 0 {
		t.Fatalf("the pending :bug:~ is in range and must release: exit %d\nstderr: %s", code, stderr)
	}
	if len(shas) != 1 || shas[0] != pending {
		t.Errorf("verdict counts %v, want [%.7s] alone — the base-merge commit is displacing the alignment", shas, pending)
	}
	for _, sha := range shas {
		testutil.Git(t, dir, "akira-toriyama", "cat-file", "-e", sha+"^{commit}")
	}
	if !strings.Contains(stderr, "shipped under an earlier tag") {
		t.Errorf("the walk must name the released commit it declined to re-count:\n%s", stderr)
	}
}
