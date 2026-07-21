package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/internal/core"
)

// newClient spins an httptest.Server for handler and returns a Client pointed at
// it with the given token, using the server's own HTTP client. Retries are
// disabled so every failure test exercises exactly one attempt — the retry
// loop has its own tests (retry_test.go), which set a schedule explicitly.
func newClient(t *testing.T, token string, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(token, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	c.retryDelays = nil
	return c
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

// TestCommitPulls422IsCommitUnknown: commits/{sha}/pulls answers 422 when
// GitHub does not (yet) know the SHA — the release walk's API-lag case, which
// must be branchable (IsCommitUnknown) so the walk can fall back instead of
// hard-failing the release. It still reads as an ordinary CodeAPI failure to
// every layer that does not branch.
func TestCommitPulls422IsCommitUnknown(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"No commit found for SHA: abc"}`)
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "abc")
	if !IsCommitUnknown(err) {
		t.Fatalf("a 422 from commits/{sha}/pulls must report IsCommitUnknown, got %v", err)
	}
	wantAPIError(t, err, "No commit found")
}

// TestCommitPulls404IsNotCommitUnknown: a 404 is how GitHub answers a bad
// credential against a private repository — for EVERY commit. Treating it as
// commit-unknown would silently degrade the whole walk to fallbacks on an auth
// failure, so only the specific 422 may branch.
func TestCommitPulls404IsNotCommitUnknown(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "abc")
	if IsCommitUnknown(err) {
		t.Fatal("a 404 must NOT report IsCommitUnknown — it is how an auth failure looks")
	}
	wantAPIError(t, err, "Not Found")
}

// TestPullCommits422IsNotCommitUnknown: the commit-unknown branch belongs to
// CommitPulls alone — a 422 from pulls/{n}/commits means a validation problem,
// and letting it read as "API lag" would convert a hard failure into a silent
// fallback. Every other method flattens the status away.
func TestPullCommits422IsNotCommitUnknown(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed"}`)
	})

	_, err := c.PullCommits(context.Background(), "o", "r", 7)
	if IsCommitUnknown(err) {
		t.Fatal("a 422 from pulls/{n}/commits must NOT report IsCommitUnknown")
	}
	wantAPIError(t, err, "Validation Failed")
}

// TestNewDefaultClientHasATimeout: without WithHTTPClient, New must not hand
// out http.DefaultClient — it has no timeout, so a hung GitHub would block a
// release job until the runner kills it, and the deadline→CodeAPI
// classification above would never be reachable in production. The release
// walk makes one request per squash commit, so the timeout must be
// per-request (http.Client.Timeout), not per-walk.
func TestNewDefaultClientHasATimeout(t *testing.T) {
	c := New("")
	if c.http == http.DefaultClient {
		t.Fatal("New's default client is http.DefaultClient, which never times out")
	}
	if c.http.Timeout <= 0 {
		t.Fatalf("New's default client Timeout = %v, want > 0", c.http.Timeout)
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
	c.retryDelays = nil // classification is under test, not the retry loop
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

// TestPaginationRunawayGuard: a pathological server that keeps advertising a
// rel="next" link must be stopped by the maxPages guard as a named API error —
// never an unbounded loop. The guard sits far above any real response, so this
// is the only way to reach it.
func TestPaginationRunawayGuard(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		// Every page points at itself as the next page.
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, r.URL.Path))
		fmt.Fprint(w, `[]`)
	})
	_, err := c.CommitPulls(context.Background(), "o", "r", "abc")
	wantAPIError(t, err, "pagination exceeded")
}

// TestPaginationNeverCarriesTheTokenOffHost is the security property, end to
// end: send attaches "Authorization: Bearer …" to whatever URL it is handed,
// and in a release job that credential carries contents: write. net/http drops
// the header when a *redirect* crosses hosts, but the next-page hop is
// hand-written and sits outside that protection — so a response whose Link
// header names a foreign host must stop the walk before a single byte leaves
// for it. The foreign server here records every request it sees; the assertion
// is that it saw NONE (stronger than "it saw no Authorization header").
//
// The refusal must also be a HARD failure: a silently truncated page walk
// would hand internal/bump a short commit list and corrupt a release verdict,
// which is worse than failing the job.
func TestPaginationNeverCarriesTheTokenOffHost(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%s %s (Authorization: %q)", r.Method, r.URL, r.Header.Get("Authorization")))
		mu.Unlock()
		fmt.Fprint(w, `[{"number":99}]`)
	}))
	t.Cleanup(foreign.Close)

	c := newClient(t, "s3cret", func(w http.ResponseWriter, r *http.Request) {
		// Page 1 answers normally and points page 2 at another origin.
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/s/pulls?page=2>; rel="next"`, foreign.URL))
		fmt.Fprint(w, `[{"number":1}]`)
	})

	pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("the foreign host received %d request(s) — the credential left the configured host: %q", len(got), got)
	}
	if err == nil {
		t.Fatalf("a cross-origin next link must hard-fail; got pulls %+v and no error", pulls)
	}
	if pulls != nil {
		t.Fatalf("a refused walk must return no partial result, got %+v", pulls)
	}
	// Both origins are named so the misconfiguration is diagnosable from a CI log.
	wantAPIError(t, err, foreign.URL)
	wantAPIError(t, err, "configured for "+c.baseURL)
}

// TestNextPageAdmitsOnlyTheConfiguredOrigin pins the origin gate case by case.
// It runs against nextPage rather than a live server because a refusal makes no
// request at all — the network is exactly what must not happen — and because
// the configured base here is the real api.github.com, which is what makes the
// prefix-lookalike and default-port cases mean anything. The end-to-end proof
// that the token stays home is TestPaginationNeverCarriesTheTokenOffHost above.
func TestNextPageAdmitsOnlyTheConfiguredOrigin(t *testing.T) {
	// The base is the public API host, not an httptest server: the check must
	// compare against whatever WithBaseURL was given (Enterprise, an
	// httptest.Server), and this table reads as the fleet's real deployment.
	c := New("s3cret", WithBaseURL(DefaultBaseURL))

	tests := []struct {
		name string
		link string
		// want is the URL the walk may follow; "" means the walk stops here.
		want string
		// refuse are substrings a hard refusal must name; empty means the link
		// is admitted (want) rather than refused.
		refuse []string
	}{
		{
			name: "same origin is followed — real GitHub rewrites the path to /repositories/{id}",
			link: `<https://api.github.com/repositories/9001/pulls?page=2>; rel="next"`,
			want: "https://api.github.com/repositories/9001/pulls?page=2",
		},
		{
			name: "host case is insignificant (RFC 3986 §3.2.2)",
			link: `<https://API.GitHub.COM/x?page=2>; rel="next"`,
			want: "https://API.GitHub.COM/x?page=2",
		},
		{
			name: "an explicit default port is the same origin, not a mismatch",
			link: `<https://api.github.com:443/x?page=2>; rel="next"`,
			want: "https://api.github.com:443/x?page=2",
		},
		{
			name:   "another host is refused",
			link:   `<https://evil.test/x?page=2>; rel="next"`,
			refuse: []string{"to https://evil.test", "configured for https://api.github.com"},
		},
		{
			name: "a prefix lookalike is refused — the check is on parsed components, not a string prefix",
			link: `<https://api.github.com.evil.test/x?page=2>; rel="next"`,
			refuse: []string{
				"to https://api.github.com.evil.test",
				"configured for https://api.github.com,",
			},
		},
		{
			name:   "a different port on the same host is refused",
			link:   `<https://api.github.com:8443/x?page=2>; rel="next"`,
			refuse: []string{"to https://api.github.com:8443", "configured for https://api.github.com,"},
		},
		{
			name:   "a scheme downgrade is refused",
			link:   `<http://api.github.com/x?page=2>; rel="next"`,
			refuse: []string{"to http://api.github.com", "configured for https://api.github.com"},
		},
		{
			name:   "a userinfo-smuggled host is refused",
			link:   `<https://api.github.com@evil.test/x?page=2>; rel="next"`,
			refuse: []string{"evil.test", "configured for https://api.github.com"},
		},
		{
			name:   "a relative link is refused, never resolved against the base",
			link:   `</repositories/9001/pulls?page=2>; rel="next"`,
			refuse: []string{"relative next-page link", "/repositories/9001/pulls?page=2"},
		},
		{
			name:   "a non-http scheme is refused",
			link:   `<file:///etc/passwd>; rel="next"`,
			refuse: []string{"file://", "configured for https://api.github.com"},
		},
		{
			name:   "an unparsable target is refused, not silently dropped",
			link:   "<https://api.github.com/x\x7f>; rel=\"next\"",
			refuse: []string{"malformed next-page link"},
		},
		{
			name: "no rel=next stops the walk without an error",
			link: `<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=9>; rel="last"`,
			want: "",
		},
		{
			name: "an absent Link header stops the walk without an error",
			link: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.nextPage(tt.link)
			if len(tt.refuse) == 0 {
				if err != nil {
					t.Fatalf("nextPage(%q) = %v, want the link admitted", tt.link, err)
				}
				if got != tt.want {
					t.Fatalf("nextPage(%q) = %q, want %q", tt.link, got, tt.want)
				}
				return
			}
			if got != "" {
				t.Fatalf("nextPage(%q) admitted %q — a refused link must yield no url to follow", tt.link, got)
			}
			for _, want := range tt.refuse {
				wantAPIError(t, err, want)
			}
		})
	}
}

// recorder is an httptest handler that logs the Authorization header of every
// request it receives and answers a plausible pull listing. The redirect tests
// assert on what the SECOND hop saw, because that — not an error string — is
// the security property: the token must not arrive there.
func recorder(mu *sync.Mutex, seen *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = append(*seen, fmt.Sprintf("%s %s (Authorization: %q)", r.Method, r.URL.Path, r.Header.Get("Authorization")))
		mu.Unlock()
		fmt.Fprint(w, `[{"number":99}]`)
	}
}

// TestRedirectOffTheConfiguredOriginIsRefused is the redirect half of the
// origin gate, and the reason it had to be written: net/http's own rule
// (shouldCopyHeaderOnRedirect) compares the HOSTNAME only, so with the default
// policy a 302 from the configured host to the SAME hostname on another port —
// or another scheme — still arrives carrying "Authorization: Bearer s3cret".
// Reviewer-verified before the gate existed: the second server logged the
// bearer token. Both shapes are exactly what nextPage refuses one hop earlier,
// so leaving them open made the gate's own doc comment untrue.
//
// The assertion is at the RECEIVING server (it must see nothing at all), not on
// the error text, and the origin server's hit count is checked with a retry
// schedule armed: a refusal is glyph's verdict, not a transient wire failure,
// so replaying it on the t-bjrv backoff would burn ~21s to reach the same
// answer.
func TestRedirectOffTheConfiguredOriginIsRefused(t *testing.T) {
	tests := []struct {
		name string
		// serve stands the redirect target up; the scheme it picks is what makes
		// the case a cross-port one (http, differing only in port) or a
		// cross-scheme one (https).
		serve func(http.Handler) *httptest.Server
	}{
		{
			name:  "another port on the same hostname — net/http alone copies the token here",
			serve: httptest.NewServer,
		},
		{
			name:  "another scheme on the same hostname — a plaintext/TLS swap is still not this origin",
			serve: httptest.NewTLSServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu     sync.Mutex
				seen   []string
				origin int
			)
			target := tt.serve(recorder(&mu, &seen))
			t.Cleanup(target.Close)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				origin++
				mu.Unlock()
				http.Redirect(w, r, target.URL+"/collect", http.StatusFound)
			}))
			t.Cleanup(srv.Close)

			// The TARGET's client is injected, not the origin's: for the TLS case
			// it is the only one that trusts the target's certificate, so without
			// the gate the request really does land there (a handshake failure
			// would have hidden the leak this test is about). It reaches the plain
			// origin server just as well.
			c := New("s3cret", WithBaseURL(srv.URL), WithHTTPClient(target.Client()))
			c.retryDelays = []time.Duration{0, 0, 0} // armed, and must go unused

			pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")

			mu.Lock()
			got, hits := append([]string(nil), seen...), origin
			mu.Unlock()

			if len(got) != 0 {
				t.Fatalf("the redirect target received %d request(s) — the credential left the configured origin: %q", len(got), got)
			}
			if err == nil {
				t.Fatalf("an off-origin redirect must hard-fail; got pulls %+v and no error", pulls)
			}
			if pulls != nil {
				t.Fatalf("a refused walk must return no partial result, got %+v", pulls)
			}
			if hits != 1 {
				t.Fatalf("the origin was asked %d times, want 1 — a policy refusal must not ride the retry schedule", hits)
			}
			// Both origins are named so a misconfigured Enterprise host is
			// diagnosable from a CI log alone.
			wantAPIError(t, err, "redirect to "+target.URL)
			wantAPIError(t, err, "configured for "+srv.URL)
		})
	}
}

// TestSameOriginRedirectIsFollowedWithTheCredential is the other side of the
// gate: GitHub redirects on its own origin for ordinary reasons (a renamed
// repository answers 301), so the policy must follow those hops AND keep
// sending the credential — a gate that broke them would be traded away the
// first time a repo was renamed. Note the Location here is absolute and
// on-origin; net/http resolves a relative Location against the request URL
// itself, which is unambiguous, unlike the Link header nextPage refuses to
// resolve against two candidate bases.
func TestSameOriginRedirectIsFollowedWithTheCredential(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			recorder(&mu, &seen)(w, r)
			return
		}
		http.Redirect(w, r, srvURL+"/moved", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c := New("s3cret", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	c.retryDelays = nil

	pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("a same-origin redirect must be followed: %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 99 {
		t.Fatalf("pulls = %+v, want the redirected response's PR 99", pulls)
	}
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	want := `GET /moved (Authorization: "Bearer s3cret")`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the redirect target saw %q, want exactly [%q]", got, want)
	}
}

// TestRedirectLimitTerminates: installing a CheckRedirect REPLACES net/http's
// standard policy wholesale, its own 10-hop stop included, so the hop counter
// has to be restated or a server-side loop becomes an infinite one inside a CI
// job — a hang, which is the one failure mode worse than a wrong answer. Every
// hop here is same-origin, so nothing but that counter can stop the walk.
func TestRedirectLimitTerminates(t *testing.T) {
	var (
		mu   sync.Mutex
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		http.Redirect(w, r, fmt.Sprintf("/hop%d", n), http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := New("s3cret", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	c.retryDelays = nil

	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "refusing to follow more than 10 redirects")

	mu.Lock()
	got := hits
	mu.Unlock()
	if got != maxRedirects {
		t.Fatalf("the server was asked %d times, want %d — the hop counter must stop the loop where net/http's did", got, maxRedirects)
	}
}

// TestGuardRedirectsCopiesTheInjectedClient pins the precedence WithHTTPClient
// gets: the gate is installed on a COPY, so glyph's policy always wins for its
// own requests (the credential is the Client's, not the transport's) while the
// caller's client object — routinely shared, one per httptest.Server here — is
// left exactly as it was handed in.
func TestGuardRedirectsCopiesTheInjectedClient(t *testing.T) {
	injected := &http.Client{Timeout: 7 * time.Second}
	c := New("s3cret", WithHTTPClient(injected))

	if c.http == injected {
		t.Fatal("New installed its redirect policy on the caller's own client instead of a copy")
	}
	if injected.CheckRedirect != nil {
		t.Fatal("New mutated the injected client's CheckRedirect — a shared client must come back untouched")
	}
	if c.http.CheckRedirect == nil {
		t.Fatal("an injected client escaped the origin gate: its redirects follow net/http's hostname-only rule")
	}
	if c.http.Timeout != injected.Timeout {
		t.Fatalf("the copy's Timeout = %v, want the injected %v — WithHTTPClient still chooses how bytes move", c.http.Timeout, injected.Timeout)
	}
}

// FuzzNextPageOrigin machine-checks the security invariant itself against
// arbitrary headers: whatever nextPage hands back for the walk to fetch
// addresses the configured origin. The assertion spells the expected origin out
// literally instead of calling sameOrigin, so a bug inside the comparison
// cannot make the property agree with itself.
func FuzzNextPageOrigin(f *testing.F) {
	c := New("s3cret", WithBaseURL(DefaultBaseURL))
	f.Add(`<https://api.github.com/x?page=2>; rel="next"`)
	f.Add(`<https://api.github.com.evil.test/x?page=2>; rel="next"`)
	f.Add(`<https://api.github.com:8443/x>; rel="next"`)
	f.Add(`<http://api.github.com/x>; rel="next"`)
	f.Add(`</x?page=2>; rel="next"`)
	f.Add(`<https://api.github.com@evil.test/x>; rel="next"`)
	f.Fuzz(func(t *testing.T, header string) {
		got, err := c.nextPage(header)
		if err != nil {
			if got != "" {
				t.Fatalf("nextPage(%q) refused with %v yet returned %q", header, err, got)
			}
			return
		}
		if got == "" {
			return // no next page: the walk stops, nothing is fetched
		}
		u, perr := url.Parse(got)
		if perr != nil {
			t.Fatalf("nextPage(%q) admitted the unparsable url %q: %v", header, got, perr)
		}
		if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "api.github.com") ||
			(u.Port() != "" && u.Port() != "443") {
			t.Fatalf("nextPage(%q) admitted %q, which is not on %s", header, got, DefaultBaseURL)
		}
	})
}

// FuzzNextLink: the Link-header parser is the one hand-rolled header parser in
// the package — it must never panic, and anything it returns must be a
// substring of the header it was given (it extracts, never fabricates).
func FuzzNextLink(f *testing.F) {
	f.Add(`<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=9>; rel="last"`)
	f.Add(`<u>; rel="prev"`)
	f.Add(``)
	f.Add(`<>; rel="next"`)
	f.Add(`garbage; ; ;, <a`)
	f.Fuzz(func(t *testing.T, header string) {
		got := nextLink(header)
		if got == "" {
			return
		}
		if !strings.Contains(header, "<"+got+">") {
			t.Fatalf("nextLink(%q) = %q, which the header never carried", header, got)
		}
	})
}
