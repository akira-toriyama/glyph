package cli

import (
	"encoding/json"
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
