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

// This file pins the walk's fallback arm (DESIGN §4; internal/walk/expand.go:
// fallback, reconcile): a direct push, the API lagging behind a merge, a pull
// the walk only saw from the inside, and the whole API dark — what is read
// from a commit's own message, what is dropped, and what is warned about.

// TestSinceTagRawRevertDirectPushIsAPatch: a raw `git revert` pushed straight
// to main has no PR to resolve; on the fallback path the generated-subject
// exclusion applies exactly as it does on --range — skipped silently, never a
// violation and never a warning.
func TestSinceTagRawRevertDirectPushIsAPatch(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", `Revert ":bug:~ fix a crash"`)
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("a lone raw-revert direct push exited %d, want 0 — the preset's Revert pattern supplies the ~ sigil (ratified: a raw git revert is a patch)\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1", stdout)
	}
	if !strings.Contains(stderr, "::warning::") {
		t.Fatalf("the fallback read must announce itself:\n%s", stderr)
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
			mp := mergePR(t, dir, "akira-toriyama", 7, ":memo:= document the menu", ":sparkles:(ui)^ add a menu")
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
		testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
		shaA := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix the menu")
		shaB := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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
		mp := mergePR(t, dir, "akira-toriyama", 7, ":sparkles:(ui)^ add a menu", ":memo:= document the menu")
		// v0.1.1 shipped the pull's first commit already — the release walk then
		// starts strictly after it, but the pull's listing still begins there.
		testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.1", mp.Branch[0])
		ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
		srv := walkServer(t, map[string]string{
			commitPullsPath(mp.Branch[1]): ref,
			commitPullsPath(mp.Merge):     `[]`, // the merge point resolves to nothing
		})
		usePR(t, srv)
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.1")
		if code != 1 || stdout != "" {
			t.Fatalf("bump = exit %d stdout %q, want 1 / empty — the only change the fold would find (%s) went out in v0.1.1\nstderr: %s", code, stdout, ":sparkles:(ui)^ add a menu", stderr)
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
				pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
				pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
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
				pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
				pullCommitsPath(8): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `,` +
					apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
			}
		},
		"a rebase-merged pull": func(t *testing.T, dir string) map[string]string {
			testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
			shaA := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix the menu")
			shaB := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			ref := `[` + apiPullRef(7, "2026-07-15T00:00:00Z", shaB) + `]`
			return map[string]string{
				commitPullsPath(shaA): ref,
				commitPullsPath(shaB): ref,
				pullCommitsPath(7): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `,` +
					apiCommit("b1", "akira-toriyama", ":bug:~ fix the menu") + `]`,
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

// TestSinceTagPlainMergeCommitIsSkipped: a local `git merge` of a topic branch
// (or a merge from a fork) pushed to main resolves to NO pull request. It must
// keep being skipped silently, exactly as before — the fallback path still
// applies the message rules, where two parents (and git's own `Merge ` subject)
// exclude it. Only the resolution question stopped asking about the shape; the
// classification question never did.
func TestSinceTagPlainMergeCommitIsSkipped(t *testing.T) {
	dir, _ := testRepo(t)
	mp := mergeInto(t, dir, "akira-toriyama", "akira-toriyama", "topic", "Merge branch 'topic'", ":memo:= note the topic")
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
		t.Fatalf("a local merge of a :memo:= branch exited %d, want 1 (no release — both commits count none)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
	if strings.Contains(stderr, mp.Merge[:7]) || strings.Contains(stderr, "Merge branch") {
		t.Fatalf("an unresolved merge commit is skipped silently, never warned about:\n%s", stderr)
	}
}

// TestSinceTagFallbackSharesShaWithAnExpandedPR: a fast-forwarded branch puts a
// PR's pre-squash commits on main under their ORIGINAL SHAs. When the walk then
// falls back on such a commit (no merged PR resolves it), it must not fold what
// an expanded PR already contributed.
func TestSinceTagFallbackSharesShaWithAnExpandedPR(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
	sha2 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[]`, // no PR resolves it — the fallback path
		// PR 7's listing already carries the commit under the SHA it has on main.
		pullCommitsPath(7): `[` + apiCommit(sha2, "akira-toriyama", ":sparkles:^ add a menu") + `]`,
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

// TestSinceTagAllCommitsUnknownWarnsAboutTheRepo: one unknown SHA is API lag;
// EVERY SHA unknown is what a wrong --repo / inherited GITHUB_REPOSITORY looks
// like (a fork or reusable-workflow context) — the walk still soft-falls-back,
// but a summary warning must say the quiet part out loud so the
// misconfiguration is findable in the log.
func TestSinceTagAllCommitsUnknownWarnsAboutTheRepo(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document it")
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
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash, pushed directly")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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

// TestSinceTagUnknownShaFallsBack: GitHub answers commits/{sha}/pulls with 422
// when it does not yet know the commit — the walk running moments after a
// push, before the API catches up. That is DESIGN §4's API lag spelled out by
// the server, so it falls back exactly like an empty association instead of
// hard-failing the release. (A 404 — the auth-failure shape — still fails.)
func TestSinceTagUnknownShaFallsBack(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash, pushed moments ago")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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

// TestSinceTagFreePrefixBangFallbackIsMajor: no code table classifies the
// prefix — it is the author's free choice — and the ! sigil alone majors a
// direct-push fallback commit, so an unknown prefix can never suppress a
// breaking marker.
func TestSinceTagFreePrefixBangFallbackIsMajor(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":notarealmoji:! drop the legacy flag")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[]`, // a direct push — the fallback path
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("a bang fallback exited %d, want 0 (it must MAJOR, not vanish)\nstderr: %s", code, stderr)
	}
	var res struct {
		Level   string `json:"level"`
		Next    string `json:"next"`
		Commits []struct {
			Sigil   string `json:"sigil"`
			Level   string `json:"level"`
			Subject string `json:"subject"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	// major CLASSIFICATION, 0.x STEP: the repository is at v0.1.0, so the
	// breaking commit steps the minor while still reading as major everywhere
	// a level is published.
	if res.Level != "major" || res.Next != "v0.2.0" {
		t.Fatalf("verdict = level %q next %q, want major → v0.2.0", res.Level, res.Next)
	}
	if len(res.Commits) != 1 || res.Commits[0].Sigil != "!" {
		t.Fatalf("commits = %+v, want the one ! commit", res.Commits)
	}
}

// TestNotesSinceTagUnknownBreakingFallbackHoists: the same commit must also
// SURFACE in the notes — a release that majors for a breaking change while
// its notes stay silent about it would advertise less than it ships. The
// normalized entry hoists into Breaking Changes with the subject verbatim.
func TestNotesSinceTagUnknownBreakingFallbackHoists(t *testing.T) {
	dir, _ := testRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":notarealmoji:! drop the legacy flag")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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

// TestSinceTagSquashMultiCommitDivergesWhenAPIDark pins what squash actually
// buys, against the claim doctor used to make. Squash guarantees a pull request
// resolves ALL-OR-NOTHING — one commit on main, and it IS the merge point — but
// it does NOT guarantee the same verdict with the API dark as with it answering.
// A multi-commit squash's subject is the PR title, and the fallback classifies
// that one subject instead of the pull's commits: a :sparkles:+:bug:~ pull titled
// :bug:~ reads minor live and patch dark.
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
			sha := squashCommit(t, dir, ":bug:(auth)~ fix a token refresh", 1)
			routes := map[string]string{
				commitPullsPath(sha): `[` + apiPullRef(1, "2026-07-20T00:00:00Z", sha) + `]`,
				pullCommitsPath(1): `[` +
					apiCommit("a1", "akira-toriyama", ":sparkles:(auth)^ add a refresh endpoint") + `,` +
					apiCommit("a2", "akira-toriyama", ":bug:(auth)~ fix a token refresh") + `]`,
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
// floor. A PR title carrying no gitmoji at all can still reach main — lint.yml's
// squash-title step excludes bot pulls, and a repository need not call lint.yml —
// so under COMMIT_OR_PR_TITLE a multi-commit squash can put an unparseable
// subject on main. Live the pull's commits still decide; dark the subject is
// dropped from the fold with a warning and the whole release counts none.
func TestSinceTagNonGitmojiPRTitleCountsNoneWhenDark(t *testing.T) {
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Add a refresh endpoint", 6)
	live := map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(6, "2026-07-20T00:00:00Z", sha) + `]`,
		pullCommitsPath(6):   `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(auth)^ add a refresh endpoint") + `]`,
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
	if want := fmt.Sprintf("cannot be folded (matches none of the %d configured patterns)", len(testutil.Config(t).Patterns)); !strings.Contains(stderr, want) {
		t.Fatalf("the loss must say the subject matched no pattern:\n%s", stderr)
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
	mp := mergePR(t, dir, "akira-toriyama", 3, ":sparkles:(ui)^ add a menu", ":memo:= document the menu")
	ref := `[` + apiPullRef(3, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	live := map[string]string{
		commitPullsPath(mp.Merge): ref,
		pullCommitsPath(3): `[` +
			apiCommit(mp.Branch[0], "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit(mp.Branch[1], "akira-toriyama", ":memo:= document the menu") + `]`,
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
	t.Chdir(dir)
	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("fully dark: exit %d stdout %q, want 0 / v0.2.0 — the branch commits are on main and classify themselves\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, mp.Branch[0][:7]) || !strings.Contains(stderr, "reading its own message") {
		t.Fatalf("the fallback must say it read each branch commit itself:\n%s", stderr)
	}
}

// TestSinceTagFallbackOnlyOnUnknownCommit is the guard that makes "an outage
// reaches the fallback" un-writable again. Exactly ONE status branches to the
// walk's lag fallback — 422, github.IsCommitUnknown, the sha GitHub has not
// indexed yet. A 403 rate limit, a 404, a 429 and a 5xx that outlives the retry schedule
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
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	r1 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the menu")
	r2 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	// GitHub names the LAST replayed commit as merge_commit_sha, and the pull's
	// listing still carries the ORIGINAL pre-rebase shas.
	t.Run("the alignment holds, so it is counted once and said out loud", func(t *testing.T) {
		srv := walkServer(t, map[string]string{
			commitPullsPath(r1): apiUnknownSHA,
			commitPullsPath(r2): `[` + apiPullRef(4, "2026-07-20T00:00:00Z", r2) + `]`,
			pullCommitsPath(4): `[` +
				apiCommit("orig111", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
				apiCommit("orig222", "akira-toriyama", ":memo:= document the menu") + `]`,
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
				apiCommit("orig111", "akira-toriyama", ":sparkles:(ui)^ add a different menu") + `,` +
				apiCommit("orig222", "akira-toriyama", ":memo:= document the menu") + `]`,
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
