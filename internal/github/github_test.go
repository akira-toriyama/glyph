package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/internal/core"
)

// newClient spins an httptest.Server for handler and returns a Client pointed at
// it with the given token, using the server's own HTTP client.
func newClient(t *testing.T, token string, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(token, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
}

// wantAPIError asserts err is a *core.Error carrying CodeAPI (the whole package
// classifies every failure that way) and that its message mentions want.
func wantAPIError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error mentioning %q, got nil", want)
	}
	ce := core.AsError(err)
	if ce == nil {
		t.Fatalf("error %v is not a *core.Error", err)
	}
	if ce.Code != core.CodeAPI {
		t.Fatalf("error code = %d, want CodeAPI (%d)", ce.Code, core.CodeAPI)
	}
	if !strings.Contains(ce.Msg, want) {
		t.Fatalf("error %q does not mention %q", ce.Msg, want)
	}
}

// TestCommitPullsSinglePage parses a one-page response into PullRefs with every
// field the release walk reads.
func TestCommitPullsSinglePage(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/akira-toriyama/glyph/commits/deadbeef/pulls" {
			t.Errorf("path = %q, want the commits/{sha}/pulls path", got)
		}
		fmt.Fprint(w, `[{"number":7,"state":"closed","merged_at":"2026-07-12T00:00:00Z","merge_commit_sha":"deadbeef"}]`)
	})

	pulls, err := c.CommitPulls(context.Background(), "akira-toriyama", "glyph", "deadbeef")
	if err != nil {
		t.Fatalf("CommitPulls: %v", err)
	}
	if len(pulls) != 1 {
		t.Fatalf("got %d pulls, want 1: %+v", len(pulls), pulls)
	}
	want := PullRef{Number: 7, State: "closed", MergedAt: "2026-07-12T00:00:00Z", MergeCommitSHA: "deadbeef"}
	if pulls[0] != want {
		t.Fatalf("pull = %+v, want %+v", pulls[0], want)
	}
}

// TestCommitPullsPaginates follows the Link rel="next" header across pages and
// preserves order.
func TestCommitPullsPaginates(t *testing.T) {
	var srvURL string
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/s/pulls?page=2>; rel="next"`, srvURL))
			fmt.Fprint(w, `[{"number":1}]`)
		case "2":
			fmt.Fprint(w, `[{"number":2}]`)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	})
	srvURL = c.baseURL // the httptest server URL, so the next link points back at it

	pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("CommitPulls: %v", err)
	}
	if len(pulls) != 2 || pulls[0].Number != 1 || pulls[1].Number != 2 {
		t.Fatalf("pagination = %+v, want PRs 1 then 2", pulls)
	}
}

// TestPullCommitsSinglePage parses inner commits, flattening commit.author.name
// into Author and the parents array into a count.
func TestPullCommitsSinglePage(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/akira-toriyama/glyph/pulls/7/commits" {
			t.Errorf("path = %q, want the pulls/{n}/commits path", got)
		}
		fmt.Fprint(w, `[
		  {"sha":"aaa","commit":{"message":":sparkles: add a thing","author":{"name":"akira-toriyama"}},"parents":[{"sha":"p1"}]},
		  {"sha":"bbb","commit":{"message":":robot: chore(fleet): sync","author":{"name":"fleet-sync[bot]"}},"parents":[{"sha":"p1"},{"sha":"p2"}]}
		]`)
	})

	commits, err := c.PullCommits(context.Background(), "akira-toriyama", "glyph", 7)
	if err != nil {
		t.Fatalf("PullCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(commits), commits)
	}
	want0 := Commit{SHA: "aaa", Author: "akira-toriyama", Parents: 1, Message: ":sparkles: add a thing"}
	if commits[0] != want0 {
		t.Fatalf("commit 0 = %+v, want %+v", commits[0], want0)
	}
	// The bot author name (not a login) and the two-parent merge shape are exactly
	// what internal/bump.Excluded keys on downstream.
	want1 := Commit{SHA: "bbb", Author: "fleet-sync[bot]", Parents: 2, Message: ":robot: chore(fleet): sync"}
	if commits[1] != want1 {
		t.Fatalf("commit 1 = %+v, want %+v", commits[1], want1)
	}
}

// TestPullCommitsPaginates follows the Link header for inner commits too.
func TestPullCommitsPaginates(t *testing.T) {
	var srvURL string
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/x?page=2>; rel="next"`, srvURL))
			fmt.Fprint(w, `[{"sha":"a","commit":{"message":"m1","author":{"name":"x"}}}]`)
		case "2":
			fmt.Fprint(w, `[{"sha":"b","commit":{"message":"m2","author":{"name":"y"}}}]`)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	})
	srvURL = c.baseURL

	commits, err := c.PullCommits(context.Background(), "o", "r", 1)
	if err != nil {
		t.Fatalf("PullCommits: %v", err)
	}
	if len(commits) != 2 || commits[0].SHA != "a" || commits[1].SHA != "b" {
		t.Fatalf("pagination = %+v, want commits a then b", commits)
	}
}

// TestSendsAuthAndHeaders asserts a non-empty token becomes a Bearer credential
// and the REST version / Accept / User-Agent headers are set on every request.
func TestSendsAuthAndHeaders(t *testing.T) {
	c := newClient(t, "s3cret", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer s3cret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer s3cret")
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want application/vnd.github+json", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			t.Errorf("X-GitHub-Api-Version = %q, want 2022-11-28", got)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent must be set (GitHub rejects requests without one)")
		}
		fmt.Fprint(w, `[]`)
	})
	if _, err := c.CommitPulls(context.Background(), "o", "r", "s"); err != nil {
		t.Fatalf("CommitPulls: %v", err)
	}
}

// TestOmitsAuthWhenTokenEmpty: an empty token sends no Authorization header
// (anonymous access), never "Bearer ".
func TestOmitsAuthWhenTokenEmpty(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Errorf("Authorization header present with an empty token: %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `[]`)
	})
	if _, err := c.CommitPulls(context.Background(), "o", "r", "s"); err != nil {
		t.Fatalf("CommitPulls: %v", err)
	}
}

// TestPerPageIsMaxed asks for the biggest page (100) so pagination is the
// exception, not the rule.
func TestPerPageIsMaxed(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		fmt.Fprint(w, `[]`)
	})
	if _, err := c.PullCommits(context.Background(), "o", "r", 1); err != nil {
		t.Fatalf("PullCommits: %v", err)
	}
}

// TestNon2xxIsAPIError turns a 404 into a CodeAPI error that carries the status
// and GitHub's own message field.
func TestNon2xxIsAPIError(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com"}`)
	})
	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "Not Found")
	if ce := core.AsError(err); ce != nil && !strings.Contains(ce.Msg, "404") {
		t.Fatalf("error %q should name the 404 status", ce.Msg)
	}
}

// TestMalformedJSONIsAPIError: a 200 with a body that is not the expected array
// is a decode failure, classified as API like any other transport fault.
func TestMalformedJSONIsAPIError(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{ not json`)
	})
	_, err := c.PullCommits(context.Background(), "o", "r", 1)
	wantAPIError(t, err, "")
}

// TestContextCancelIsInterrupted: a canceled context surfaces as CodeInterrupted
// (the user's own Ctrl-C), not as an API failure.
func TestContextCancelIsInterrupted(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CommitPulls(ctx, "o", "r", "s")
	if err == nil {
		t.Fatal("want an error on a canceled context")
	}
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("error = %v, want CodeInterrupted (%d)", err, core.CodeInterrupted)
	}
}

// TestContextDeadlineIsAPI: a *deadline* is not an interrupt. CodeInterrupted
// means the user aborted the run (SIGINT/SIGTERM, per the core contract); a
// caller-imposed timeout firing against a hung GitHub is a network failure and
// must exit CodeAPI, not vanish silently as a 130 the release job reads as
// "the operator stopped me".
func TestContextDeadlineIsAPI(t *testing.T) {
	release := make(chan struct{})
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the request open past the deadline
		fmt.Fprint(w, `[]`)
	})
	// Registered after newClient's srv.Close, so it runs first (LIFO): the
	// handler unblocks before the server waits on it.
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.CommitPulls(ctx, "o", "r", "s")
	if err == nil {
		t.Fatal("want an error when the deadline fires")
	}
	ce := core.AsError(err)
	if ce == nil {
		t.Fatalf("error %v is not a *core.Error", err)
	}
	if ce.Code == core.CodeInterrupted {
		t.Fatalf("a deadline was classified as CodeInterrupted (%d) — that code means the user aborted; want CodeAPI (%d)", core.CodeInterrupted, core.CodeAPI)
	}
	if ce.Code != core.CodeAPI {
		t.Fatalf("error code = %d, want CodeAPI (%d)", ce.Code, core.CodeAPI)
	}
}

// TestTransportErrorIsAPI: a request that never reaches a server (connection
// refused) fails with a healthy context, so it must classify as CodeAPI — the
// other side of the cancel/deadline split.
func TestTransportErrorIsAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := srv.URL
	srv.Close() // nothing is listening there any more

	c := New("", WithBaseURL(dead))
	_, err := c.PullCommits(context.Background(), "o", "r", 1)
	wantAPIError(t, err, "")
}

// TestDistillTruncatesOnARuneBoundary: a long non-JSON error body is snipped for
// the message, and the snip must not cut a multi-byte rune in half — an invalid
// UTF-8 tail would surface as U+FFFD garbage in the JSON error envelope.
func TestDistillTruncatesOnARuneBoundary(t *testing.T) {
	// 100 × 3-byte runes = 300 bytes; a byte-index cut at 200 lands mid-rune.
	body := strings.Repeat("あ", 100)
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, body) // not JSON: distill falls back to the snippet branch
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	ce := core.AsError(err)
	if ce == nil {
		t.Fatalf("error %v is not a *core.Error", err)
	}
	if !utf8.ValidString(ce.Msg) {
		t.Fatalf("error message is not valid UTF-8 — the snippet cut through a rune: %q", ce.Msg)
	}
}

// TestDistillFallsBackToBodySnippet: GitHub's gateway errors are not always the
// standard {"message":...} JSON. A plain-text/HTML body must still reach the
// operator, not be swallowed into a bare status.
func TestDistillFallsBackToBodySnippet(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>upstream is on fire</html>")
	})
	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "upstream is on fire")
}

// TestEmptyBodyIsAPIError: a 2xx with no body (a 204, or a truncated response)
// cannot decode — that is an API failure, never a silent empty result the walk
// would mistake for "this PR has no commits".
func TestEmptyBodyIsAPIError(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 2xx, zero bytes
	})
	_, err := c.PullCommits(context.Background(), "o", "r", 1)
	wantAPIError(t, err, "")
}

// TestNextLinkPicksNextAmongRealGitHubRels is the regression pin for the only
// nontrivial parser in the package. Real GitHub sends several rels at once and
// rel="next" is NOT the first entry, so a nextLink that returned the first
// target, or matched "next" as a substring of another rel, would fetch the wrong
// page — while the single-entry pagination tests stayed green. The terminal page
// carries prev/last but no next, which must STOP the walk (not loop, not follow
// "last").
func TestNextLinkPicksNextAmongRealGitHubRels(t *testing.T) {
	var srvURL string
	var seen []string
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		seen = append(seen, page)
		switch page {
		case "", "1":
			// next is deliberately in the middle, and "last" points elsewhere.
			w.Header().Set("Link", fmt.Sprintf(
				`<%[1]s/x?page=1>; rel="prev", <%[1]s/x?page=2>; rel="next", <%[1]s/x?page=9>; rel="last"`, srvURL))
			fmt.Fprint(w, `[{"number":1}]`)
		case "2":
			// Terminal page: prev/last only, no next — the walk must stop here.
			w.Header().Set("Link", fmt.Sprintf(
				`<%[1]s/x?page=1>; rel="prev", <%[1]s/x?page=9>; rel="last"`, srvURL))
			fmt.Fprint(w, `[{"number":2}]`)
		default:
			t.Errorf("followed the wrong rel — fetched page %q", page)
			fmt.Fprint(w, `[]`)
		}
	})
	srvURL = c.baseURL

	pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("CommitPulls: %v", err)
	}
	if len(pulls) != 2 || pulls[0].Number != 1 || pulls[1].Number != 2 {
		t.Fatalf("pulls = %+v, want PRs 1 then 2", pulls)
	}
	if len(seen) != 2 || seen[0] != "" || seen[1] != "2" {
		t.Fatalf("fetched pages %q, want the initial page then page 2 and a stop", seen)
	}
}
