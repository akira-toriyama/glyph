package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prServer stands in for api.github.com: it serves one pull request's commits at
// the pulls/{n}/commits path and fails the test on any other path. The client
// finds it through GITHUB_API_URL — the same variable a GitHub Enterprise runner
// sets — so no test-only injection hook is needed.
func prServer(t *testing.T, number int, body string) *httptest.Server {
	t.Helper()
	want := fmt.Sprintf("/repos/akira-toriyama/glyph/pulls/%d/commits", number)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != want {
			t.Errorf("requested %q, want %q", r.URL.Path, want)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// apiCommit renders one commit in the shape GET pulls/{n}/commits returns.
func apiCommit(sha, author, message string) string {
	m, _ := json.Marshal(message)
	return fmt.Sprintf(`{"sha":%q,"commit":{"message":%s,"author":{"name":%q}},"parents":[{"sha":"p"}]}`,
		sha, m, author)
}

// usePR points the CLI at the stand-in API and names the repository, exactly as
// GitHub Actions would.
func usePR(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

// TestBumpPR is the whole point of the squash-safe design: the version comes from
// the PR's INDIVIDUAL commits, which a squash-merge would otherwise collapse into
// one subject. A feature commit rides alongside a docs commit; the fold takes the
// max, so the PR is a minor.
func TestBumpPR(t *testing.T) {
	srv := prServer(t, 7, `[`+
		apiCommit("a1", "akira-toriyama", ":memo: document the fold")+`,`+
		apiCommit("b2", "akira-toriyama", ":sparkles:(ui) add a menu")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t) // carries the v0.1.0 tag bump steps from
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--pr", "7")
	if code != 0 {
		t.Fatalf("bump --pr exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.2.0\n" {
		t.Fatalf("bump --pr stdout = %q, want %q", stdout, "v0.2.0\n")
	}
}

// TestBumpPRExcludesBots: the participation rules must be identical whether the
// commits are read from git or from the API. A bot's commit inside a PR cannot
// move the version — the same rule that keeps fleet-sync out of the fold.
func TestBumpPRExcludesBots(t *testing.T) {
	srv := prServer(t, 9, `[`+
		apiCommit("a1", "fleet-sync[bot]", ":sparkles: a bot must not minor the release")+`,`+
		apiCommit("b2", "akira-toriyama", ":bug: fix a crash")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--pr", "9")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump --pr = exit %d stdout %q, want 0 / v0.1.1 (the bot's :sparkles: must not count)", code, stdout)
	}
}

// TestNotesPR: notes read the same PR commits, so a squash-merged PR still
// renders one line per real change.
func TestNotesPR(t *testing.T) {
	srv := prServer(t, 7, `[`+
		apiCommit("a1", "akira-toriyama", ":sparkles: add a menu")+`,`+
		apiCommit("b2", "akira-toriyama", ":bug:(api) stop dropping the last page")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--pr", "7")
	if code != 0 {
		t.Fatalf("notes --pr exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "add a menu") || !strings.Contains(stdout, "stop dropping the last page") {
		t.Fatalf("notes --pr body is missing the PR's commits:\n%s", stdout)
	}
}

// TestPRRepoFlagBeatsTheEnvironment: --repo is the explicit override for a caller
// outside Actions (or one pointing at another repository).
func TestPRRepoFlagBeatsTheEnvironment(t *testing.T) {
	srv := prServer(t, 3, `[`+apiCommit("a1", "akira-toriyama", ":bug: fix a crash")+`]`)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "someone-else/wrong") // must be overridden
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--pr", "3", "--repo", "akira-toriyama/glyph")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("--repo should win over GITHUB_REPOSITORY: exit %d stdout %q", code, stdout)
	}
}

// TestPRWithoutARepoIsUsage: with neither --repo nor GITHUB_REPOSITORY there is
// no repository to ask — the caller's input is incomplete, so usage, not an API
// failure (nothing was ever sent).
func TestPRWithoutARepoIsUsage(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "7")
	if code != 2 {
		t.Fatalf("bump --pr with no repository exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "repo") {
		t.Fatalf("the error should name the missing repository input:\n%s", stderr)
	}
}

// TestPRAndRangeAreMutuallyExclusive: they are two different sources for the same
// answer; taking both would silently pick one.
func TestPRAndRangeAreMutuallyExclusive(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)
	if code, _, _ := runGlyph(t, "bump", "--pr", "7", "--range", base+"..HEAD"); code != 2 {
		t.Fatalf("bump --pr with --range should exit 2 (usage)")
	}
}

// TestPRNumberMustBePositive: a zero or negative --pr is the caller's input,
// caught before any request goes out — and the error must name --pr, not misfire
// as a --range complaint. --pr 0 is the reachable one: it is exactly what a
// workflow yields from a null PR number (`--pr $(jq .number)` on a non-PR event),
// so the presence check dispatches on cmd.Flags().Changed, not the zero value.
func TestPRNumberMustBePositive(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph")
	for _, n := range []string{"0", "-3"} {
		t.Run("pr="+n, func(t *testing.T) {
			dir, _ := testRepo(t)
			t.Chdir(dir)
			code, _, stderr := runGlyph(t, "bump", "--pr", n)
			if code != 2 {
				t.Fatalf("bump --pr %s exited %d, want 2 (usage)", n, code)
			}
			if strings.Contains(stderr, "--range") || !strings.Contains(stderr, "--pr") {
				t.Fatalf("bump --pr %s must complain about --pr, not --range:\n%s", n, stderr)
			}
		})
	}
}

// TestPRNotFoundIsAPI: a 404 from GitHub is an API failure (exit 4) — the caller's
// arguments were well-formed; the remote said no.
func TestPRNotFoundIsAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "404")
	if code != 4 {
		t.Fatalf("bump --pr against a 404 exited %d, want 4 (API)", code)
	}
	if !strings.Contains(stderr, "Not Found") {
		t.Fatalf("the error should carry GitHub's message:\n%s", stderr)
	}
}

// TestPRAllBotIsNoRelease: an all-bot PR — the routine fleet-sync shape — has no
// participating commits, so it is a no-release (exit 1), exactly as the local
// range walk treats it. This is the participation parity between git and the API,
// on the input the fleet actually produces every day.
func TestPRAllBotIsNoRelease(t *testing.T) {
	srv := prServer(t, 5, `[`+
		apiCommit("a1", "fleet-sync[bot]", ":robot: chore(fleet): sync a file")+`,`+
		apiCommit("b2", "dependabot[bot]", ":arrow_up: build(deps): bump a thing")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--pr", "5")
	if code != 1 {
		t.Fatalf("an all-bot PR exited %d, want 1 (no release)", code)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
}

// TestPRMalformedCommitIsLint: a participating commit that does not parse is a
// hard lint error (exit 3), whether it is read from git or from the API — a
// release job must not silently bump past a malformed commit.
func TestPRMalformedCommitIsLint(t *testing.T) {
	srv := prServer(t, 6, `[`+
		apiCommit("a1", "akira-toriyama", "no gitmoji leads this subject")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "6")
	if code != 3 {
		t.Fatalf("a malformed PR commit exited %d, want 3 (lint)", code)
	}
	if !strings.Contains(stderr, "a1") {
		t.Fatalf("the lint error should name the offending commit:\n%s", stderr)
	}
}

// TestPRMalformedRepoIsUsage: a --repo that is not owner/name is caller input,
// caught before any request — usage (2), not an API failure.
func TestPRMalformedRepoIsUsage(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)
	for _, spec := range []string{"notaslash", "a/b/c", "/leading", "trailing/"} {
		if code, _, _ := runGlyph(t, "bump", "--pr", "7", "--repo", spec); code != 2 {
			t.Fatalf("bump --pr --repo %q exited %d, want 2 (usage)", spec, code)
		}
	}
}

// TestPRUsesGHTokenFallback: with GITHUB_TOKEN empty the client falls back to
// GH_TOKEN (what the gh CLI exports) and sends it as the Bearer credential — the
// fallback resolveRepo/newGitHub advertise, proven on the wire.
func TestPRUsesGHTokenFallback(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `[`+apiCommit("a1", "akira-toriyama", ":bug: fix a crash")+`]`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph")
	t.Setenv("GITHUB_TOKEN", "") // empty → fall back
	t.Setenv("GH_TOKEN", "gh-fallback-token")
	dir, _ := testRepo(t)
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "bump", "--pr", "7"); code != 0 {
		t.Fatalf("bump --pr exited %d, want 0\n%s", code, stderr)
	}
	if gotAuth != "Bearer gh-fallback-token" {
		t.Fatalf("Authorization = %q, want the GH_TOKEN fallback as a Bearer credential", gotAuth)
	}
}

// TestBumpPRJSONVerdict: --pr produces the same machine verdict shape as --range,
// carrying the PR's own commits. The reason keeps its established contract — it
// names the commit that DECIDED the level ("why this bump"), not the input the
// caller already knows it asked for.
func TestBumpPRJSONVerdict(t *testing.T) {
	srv := prServer(t, 7, `[`+apiCommit("a1", "akira-toriyama", ":sparkles: add a menu")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("bump --pr --json exited %d, want 0", code)
	}
	var res struct {
		Level   string `json:"level"`
		Next    string `json:"next"`
		Commits []struct {
			SHA  string `json:"sha"`
			Code string `json:"code"`
		} `json:"commits"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if res.Level != "minor" || res.Next != "v0.2.0" {
		t.Fatalf("verdict = %+v, want minor → v0.2.0", res)
	}
	if len(res.Commits) != 1 || res.Commits[0].Code != ":sparkles:" || res.Commits[0].SHA != "a1" {
		t.Fatalf("commits = %+v, want the PR's one :sparkles: commit (sha a1)", res.Commits)
	}
	if !strings.Contains(res.Reason, ":sparkles:") || !strings.Contains(res.Reason, "minor") {
		t.Fatalf("reason %q should name the deciding commit and the level", res.Reason)
	}
}

// TestNoneVerdictNamesThePullRequest: on a no-release verdict there IS no deciding
// commit, so the reason falls back to naming what was read — and for --pr that must
// be the pull request, not an empty string left over from the --range plumbing.
func TestNoneVerdictNamesThePullRequest(t *testing.T) {
	srv := prServer(t, 12, `[`+apiCommit("a1", "akira-toriyama", ":memo: document the fold")+`]`)
	usePR(t, srv)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "12")
	if code != 1 {
		t.Fatalf("a docs-only PR exited %d, want 1 (soft no-release)", code)
	}
	if !strings.Contains(stderr, "akira-toriyama/glyph#12") {
		t.Fatalf("the no-release reason should name the pull request it read:\n%s", stderr)
	}
}
