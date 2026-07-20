package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/internal/core"
)

// retryClient is newClient with an instant retry schedule: the loop runs its
// full length, the test never sleeps.
func retryClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	c.retryDelays = []time.Duration{0, 0, 0}
	return c
}

// TestNewDefaultClientRetries: without an override, New must hand out a real
// backoff schedule — a client that never retries would hard-fail a whole
// verdict job on one 503 from a passing GitHub outage, which is the incident
// this schedule exists to absorb.
func TestNewDefaultClientRetries(t *testing.T) {
	c := New("")
	if len(c.retryDelays) == 0 {
		t.Fatal("New's default client has no retry schedule — one transient 503 would hard-fail the run")
	}
	for i := 1; i < len(c.retryDelays); i++ {
		if c.retryDelays[i] <= c.retryDelays[i-1] {
			t.Fatalf("retry schedule %v is not increasing — backoff must back off", c.retryDelays)
		}
	}
}

// TestRetries5xxThenSucceeds: a 503 that clears before the schedule runs out
// must be invisible to the caller — same result, no error, just later.
func TestRetries5xxThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"message":"Service Unavailable"}`)
			return
		}
		fmt.Fprint(w, `[]`)
	})

	pulls, err := c.CommitPulls(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("a 503 that clears within the schedule must succeed, got %v", err)
	}
	if pulls == nil || len(pulls) != 0 {
		t.Fatalf("pulls = %v, want the empty success answer", pulls)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("server saw %d request(s), want 3 (two 503s, then success)", got)
	}
}

// TestGivesUpAfterTheSchedule: a 5xx that never clears exhausts the schedule,
// fails as an ordinary API error (the exit-code contract is unchanged), and
// the message says the retries ran — otherwise a CI log leaves open whether
// the backoff ever happened.
func TestGivesUpAfterTheSchedule(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"Service Unavailable"}`)
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "503")
	wantAPIError(t, err, "gave up after 4 attempts")
	if got := hits.Load(); got != 4 {
		t.Fatalf("server saw %d request(s), want 4 (the schedule's one initial + three retries)", got)
	}
}

// Test429IsRetried: a primary rate-limit answer is transient by definition.
func Test429IsRetried(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
			return
		}
		fmt.Fprint(w, `[]`)
	})

	if _, err := c.CommitPulls(context.Background(), "o", "r", "s"); err != nil {
		t.Fatalf("a 429 that clears must succeed, got %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server saw %d request(s), want 2", got)
	}
}

// TestSecondaryRateLimit403IsRetried: GitHub's secondary rate limit answers
// 403 WITH a Retry-After header — that pair is transient and must retry.
func TestSecondaryRateLimit403IsRetried(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"You have exceeded a secondary rate limit"}`)
			return
		}
		fmt.Fprint(w, `[]`)
	})

	if _, err := c.CommitPulls(context.Background(), "o", "r", "s"); err != nil {
		t.Fatalf("a secondary-rate-limit 403 must retry and succeed, got %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server saw %d request(s), want 2", got)
	}
}

// TestBare403IsNotRetried: a 403 WITHOUT Retry-After is a permission failure —
// retrying cannot repair a bad credential, and hammering it would only burn
// more of the rate limit.
func TestBare403IsNotRetried(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by integration"}`)
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "Resource not accessible")
	if got := hits.Load(); got != 1 {
		t.Fatalf("server saw %d request(s), want exactly 1 (a permission failure must not be retried)", got)
	}
}

// Test404IsNotRetried: every other 4xx is the caller's input — the same answer
// would come back every time.
func Test404IsNotRetried(t *testing.T) {
	var hits atomic.Int32
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	_, err := c.CommitPulls(context.Background(), "o", "r", "s")
	wantAPIError(t, err, "Not Found")
	if got := hits.Load(); got != 1 {
		t.Fatalf("server saw %d request(s), want exactly 1", got)
	}
}

// TestTransportErrorIsRetried: an outage resets connections as often as it
// answers 503, so a transport failure with a live context walks the same
// schedule. Nothing listens on the target, so only the give-up message can
// witness the attempts.
func TestTransportErrorIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := srv.URL
	srv.Close()

	c := New("", WithBaseURL(dead))
	c.retryDelays = []time.Duration{0, 0, 0}
	_, err := c.PullCommits(context.Background(), "o", "r", 1)
	wantAPIError(t, err, "gave up after 4 attempts")
}

// TestWriteRetryReplaysTheBody: a retried write must carry the SAME payload —
// GetBody rewinds it onto the clone. A retry that sent an empty body would
// PATCH a release into an empty shell, which is corruption, not resilience.
func TestWriteRetryReplaysTheBody(t *testing.T) {
	var bodies []string
	c := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"id":7,"tag_name":"v1.2.3","draft":true,"html_url":"u"}`)
	})

	rel, err := c.UpdateRelease(context.Background(), "o", "r", 7, ReleaseParams{TagName: "v1.2.3", Draft: true})
	if err != nil {
		t.Fatalf("a 502 that clears must succeed, got %v", err)
	}
	if rel.ID != 7 {
		t.Fatalf("release = %+v, want the decoded answer", rel)
	}
	if len(bodies) != 2 {
		t.Fatalf("server saw %d request(s), want 2", len(bodies))
	}
	if bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("retried body diverged from the original:\nfirst:  %q\nsecond: %q", bodies[0], bodies[1])
	}
	if !strings.Contains(bodies[1], "v1.2.3") {
		t.Fatalf("retried body lost the payload: %q", bodies[1])
	}
}

// TestCancelDuringBackoffIsInterrupted: the user's Ctrl-C must cut a pending
// backoff short and carry out as the interrupt it is — not as an API failure,
// and not after the full wait.
func TestCancelDuringBackoffIsInterrupted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	c := New("", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	c.retryDelays = []time.Duration{time.Hour} // only a cut-short can finish this test

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	t.Cleanup(func() { timer.Stop(); cancel() })

	_, err := c.CommitPulls(ctx, "o", "r", "s")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("error = %v, want CodeInterrupted — a cancel mid-backoff is the user's own abort", err)
	}
}

// TestRetryWaitHonorsRetryAfter pins the wait arithmetic: the server's own
// Retry-After wins over the schedule when parseable, is capped so an
// outage-mode gateway naming minutes cannot hang a job, and anything
// unparseable falls back to the schedule.
func TestRetryWaitHonorsRetryAfter(t *testing.T) {
	fallback := 16 * time.Second
	cases := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{"absent falls back", "", fallback},
		{"seconds win", "2", 2 * time.Second},
		{"zero is honored", "0", 0},
		{"capped", "9999", maxRetryAfter},
		{"garbage falls back", "soon", fallback},
		{"negative falls back", "-3", fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &statusError{status: http.StatusForbidden, retryAfter: tc.retryAfter, err: core.APIf("x")}
			if got := retryWait(err, fallback); got != tc.want {
				t.Fatalf("retryWait(Retry-After %q) = %v, want %v", tc.retryAfter, got, tc.want)
			}
		})
	}
}
