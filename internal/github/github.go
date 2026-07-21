// Package github reads the pull-request graph glyph needs for squash-safe
// releases out of the GitHub REST API. It is an I/O adapter, the remote twin of
// internal/gitsource: it moves bytes and classifies failures (every API failure
// is core.CodeAPI by contract, a canceled context is core.CodeInterrupted), and
// holds no commit semantics — parsing belongs to internal/parser, participation
// and folding to internal/bump. The base URL and HTTP client are injectable so
// an httptest.Server can stand in for api.github.com under test.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/internal/core"
)

// DefaultBaseURL is the public GitHub REST host. A GitHub Enterprise host or an
// httptest.Server overrides it with WithBaseURL.
const DefaultBaseURL = "https://api.github.com"

const (
	// apiVersion pins the REST schema so a future default bump on GitHub's side
	// cannot silently reshape a response glyph parses.
	apiVersion = "2022-11-28"
	acceptJSON = "application/vnd.github+json"
	// userAgent is mandatory — GitHub rejects a request without one.
	userAgent = "glyph"
	// perPage asks for the largest page so pagination is the exception; the two
	// endpoints glyph calls return single-digit pages in practice.
	perPage = "100"
	// maxPages guards a pathological server that keeps advertising a next link.
	// It sits far above any real response (pulls/{n}/commits caps at 250 total)
	// so it never fires in practice, only stops a runaway loop.
	maxPages = 1000
)

// PullCommitsCap is where GitHub truncates the pulls/{n}/commits listing, no
// matter how far the pagination follows — an API contract, so the adapter owns
// the number. A response of exactly this many commits may be missing some;
// the caller that renders warnings names the boundary through this constant.
const PullCommitsCap = 250

// defaultTimeout bounds each request when no client is injected —
// http.DefaultClient would hang on an unresponsive GitHub until the CI runner
// kills the whole job. Per-request (not per-walk): the release walk makes one
// round-trip per squash commit, and each deserves the full allowance. When it
// fires, the deadline classifies as an ordinary API failure (see failed).
const defaultTimeout = 30 * time.Second

// defaultRetryDelays is the backoff schedule for transient failures — one more
// attempt after each delay, so the default is 4 attempts over ~21s. The shape
// this absorbs is a real one: a GitHub minor outage answering 503 for tens of
// seconds took a fleet-wide verdict sweep down and cost several waves of
// `gh run rerun` (t-bjrv). 1s→4s→16s rides out that window without stretching
// a healthy run — a request that succeeds never waits at all.
var defaultRetryDelays = []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second}

// maxRetryAfter caps how long a server-sent Retry-After header may hold one
// retry. GitHub's secondary-rate-limit answers carry the header and are worth
// honoring precisely; an outage-mode gateway could name minutes, and sleeping
// that long per attempt would hang a CI job past any useful patience.
const maxRetryAfter = 30 * time.Second

// PullRef is the slice of a pull request the release walk needs to resolve a
// squash commit to the PR it came from: its number, whether/when it merged, and
// merge_commit_sha — a commit can be associated with more than one PR, so the
// caller (internal/bump) matches MergeCommitSHA == sha to pick the exact one.
type PullRef struct {
	Number         int
	State          string
	MergedAt       string // RFC3339, empty when the PR is not merged
	MergeCommitSHA string
}

// Commit mirrors gitsource.RawCommit field-for-field — SHA, the author *name*
// (what *[bot] matching runs on: the Contents API author override the fleet-sync
// bot uses carries a name, not a login), parent count, and the verbatim message
// — so a PR's pre-squash commits feed the very same participation walk as local
// commits.
type Commit struct {
	SHA     string
	Author  string
	Parents int
	Message string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a different API host (Enterprise, or an
// httptest.Server). A trailing slash is trimmed so path joins stay clean.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient injects the HTTP client — an httptest.Server's Client() in
// tests, or a rate-limit/timeout-tuned client in production. It chooses how
// bytes move (transport, TLS, timeout); it does NOT choose who may receive the
// token, because New re-installs the origin gate over whatever is injected —
// see guardRedirects.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// Client talks to one GitHub REST host with one credential.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
	// retryDelays is the backoff schedule between attempts on a transient
	// failure; nil or empty means a single attempt. Tests shorten it to keep
	// retry paths fast; production always runs the default.
	retryDelays []time.Duration
}

// New builds a client. token is sent as a Bearer credential when non-empty; an
// empty token still reads public repos, at the anonymous rate limit.
//
// The origin gate is installed LAST, after every option has run, so it covers a
// client handed in by WithHTTPClient exactly as it covers the default one: the
// invariant "this client speaks its credential only to the origin baseURL
// names" has to hold for every Client this constructor returns, or it is not an
// invariant. See guardRedirects for why an injected client's own policy loses.
func New(token string, opts ...Option) *Client {
	c := &Client{baseURL: DefaultBaseURL, http: &http.Client{Timeout: defaultTimeout},
		token: token, retryDelays: defaultRetryDelays}
	for _, o := range opts {
		o(c)
	}
	c.http = c.guardRedirects(c.http)
	return c
}

// guardRedirects returns h with checkRedirect installed as its redirect policy.
//
// It works on a shallow COPY and never mutates h. The client passed to
// WithHTTPClient belongs to the caller and is routinely shared — every test in
// this package hands in httptest.Server.Client(), one object per server, and a
// production caller may well reuse one tuned client for several things —
// so reaching into it to rewrite how it follows redirects is a side effect a
// constructor has no business having. The copy carries Transport, Timeout, Jar
// and the rest over untouched: those are what WithHTTPClient exists to choose.
//
// An injected CheckRedirect is deliberately REPLACED, not chained to and not
// respected. The credential belongs to the Client, not to the transport: send
// attaches "Authorization: Bearer <token>" — contents: write in a release job —
// to every request, so where that header may travel is this package's invariant
// to keep and not a knob a caller widens by handing in a client. A caller that
// wants different redirect handling is asking for a different security
// contract, and would be silently granted one if this deferred to whatever it
// injected.
func (c *Client) guardRedirects(h *http.Client) *http.Client {
	guarded := *h
	guarded.CheckRedirect = c.checkRedirect
	return &guarded
}

// maxRedirects is how many hops one request may take. It restates net/http's
// own default of 10 because installing ANY CheckRedirect replaces the standard
// policy wholesale — the 10-hop stop included — so a hook that forgets to count
// turns a server-side redirect loop into an infinite one inside a CI job.
const maxRedirects = 10

// checkRedirect is the redirect half of the origin gate nextPage opens: every
// hop must land on the origin baseURL names, and one that leaves it fails the
// request instead of being followed.
//
// It exists because net/http's own protection is WEAKER than the Link-header
// gate. shouldCopyHeaderOnRedirect compares the HOSTNAME only — scheme and port
// are discarded, and a subdomain of the original host counts as the same host —
// so the standard client happily carries "Authorization: Bearer <token>" from
// https://ghe.corp to http://ghe.corp:8080, i.e. to another service, in
// plaintext. Verified empirically between two httptest servers on 127.0.0.1:
// the second port logged the bearer token. That is exactly the shape nextPage
// refuses one hop earlier, and a gate whose back door is wider than its front
// door is not a gate — which mattered the moment this package started claiming
// the invariant out loud.
//
// The verdict is REFUSAL, not "follow it with the header stripped". Stripping
// keeps the credential home but still lets a foreign host ANSWER, and its JSON
// becomes the pull list or the commit list a release verdict is computed from:
// that trades a leaked token for a silently corrupted release, which is the
// worse of the two. Refusing is also the same answer nextPage gives the same
// shape, so the package has one origin rule with one outcome. glyph talks to an
// API it was pointed at; a redirect off that origin is an anomaly, and the
// house rule is that anomalies are named rather than absorbed.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return &redirectRefused{err: core.APIf(
			"github: refusing to follow more than %d redirects — %s is redirecting in a loop",
			maxRedirects, originOf(req.URL))}
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return &redirectRefused{err: core.APIf("github: malformed base url %q: %v", c.baseURL, err)}
	}
	if !sameOrigin(base, req.URL) {
		return &redirectRefused{err: core.APIf(
			"github: refusing to follow a redirect to %s — this client is configured for %s, and its credential goes nowhere else",
			originOf(req.URL), originOf(base))}
	}
	return nil
}

// redirectRefused carries a checkRedirect verdict back out through net/http,
// which wraps whatever CheckRedirect returns in a *url.Error. It marks the
// failure as glyph's own policy rather than a wire hiccup, so send's retry loop
// does not replay a decision that cannot change: the t-bjrv backoff exists to
// ride out a GitHub outage, and spending ~21s re-refusing the same redirect
// before failing anyway would only delay the CI log the operator needs.
// Unwrap keeps the wrapped *core.Error reachable, so to everything outside this
// package (core.AsError, core.ExitCode) it is an ordinary CodeAPI failure.
type redirectRefused struct{ err *core.Error }

func (e *redirectRefused) Error() string { return e.err.Error() }
func (e *redirectRefused) Unwrap() error { return e.err }

// apiPull is the pull-request shape as GitHub reports it; only the fields the
// release walk reads are decoded.
type apiPull struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	MergedAt       string `json:"merged_at"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

// apiCommit is the nested commit shape from pulls/{n}/commits; it is flattened
// into the package's own Commit so callers never touch the API's structure.
type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commit"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// CommitPulls returns the pull requests associated with a commit
// (GET /repos/{owner}/{repo}/commits/{sha}/pulls), following pagination. The
// squash-safe walk picks the PR whose MergeCommitSHA equals sha; returning all
// of them keeps that policy with the caller.
func (c *Client) CommitPulls(ctx context.Context, owner, repo, sha string) ([]PullRef, error) {
	first := fmt.Sprintf("%s/repos/%s/%s/commits/%s/pulls?per_page=%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha), perPage)
	raw, err := getAll[apiPull](ctx, c, first)
	if err != nil {
		return nil, err
	}
	// apiPull carries the wire json tags; PullRef is the tagless domain mirror.
	// The two are structurally identical, so a direct conversion maps them (and
	// a future PullRef-only field turns this into a compile error, not a silent
	// drop). apiCommit→Commit below cannot convert — it flattens a nested shape.
	pulls := make([]PullRef, len(raw))
	for i, p := range raw {
		pulls[i] = PullRef(p)
	}
	return pulls, nil
}

// PullCommits returns a pull request's individual commits oldest first
// (GET /repos/{owner}/{repo}/pulls/{number}/commits), following pagination —
// the pre-squash commits whose gitmoji drive the bump and the notes.
func (c *Client) PullCommits(ctx context.Context, owner, repo string, number int) ([]Commit, error) {
	first := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/commits?per_page=%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), number, perPage)
	raw, err := getAll[apiCommit](ctx, c, first)
	if err != nil {
		return nil, flatten(err)
	}
	commits := make([]Commit, len(raw))
	for i, ac := range raw {
		commits[i] = Commit{
			SHA:     ac.SHA,
			Author:  ac.Commit.Author.Name,
			Parents: len(ac.Parents),
			Message: ac.Commit.Message,
		}
	}
	return commits, nil
}

// getAll fetches every page of a paginated collection starting at u, following
// the Link rel="next" header, and concatenates the decoded pages in order.
func getAll[T any](ctx context.Context, c *Client, u string) ([]T, error) {
	var all []T
	for pages := 0; u != ""; pages++ {
		if pages >= maxPages {
			return nil, core.APIf("github: pagination exceeded %d pages", maxPages)
		}
		var page []T
		next, err := c.get(ctx, u, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		u = next
	}
	return all, nil
}

// get performs one GET via send, decodes the 2xx body into into, and returns
// the next-page URL from the Link header ("" when there is none) — admitted
// only when it addresses the client's own origin, see nextPage.
func (c *Client) get(ctx context.Context, u string, into any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", core.APIf("github: malformed request url %q: %v", u, err)
	}
	body, header, err := c.send(req)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return "", core.APIf("github: decoding %s: %v", req.URL.Path, err)
	}
	return c.nextPage(header.Get("Link"))
}

// nextPage reads the next-page URL out of a response's Link header and admits
// it only when it addresses the client's OWN origin — the scheme, host and
// port baseURL names. "" (no next page) passes through as the walk's stop.
//
// Why the gate exists: send attaches "Authorization: Bearer <token>" to
// whatever URL it is handed, and in a release job that credential carries
// contents: write. net/http drops the header when a *redirect* crosses hosts,
// but this hop is hand-written and sits outside that protection, so without
// the check a response that merely *says* "your next page is over there" would
// hand the token to an arbitrary host. (net/http's redirect rule is also too
// coarse to lean on even where it does apply — it compares hostnames only —
// which is why checkRedirect re-applies THIS rule to every hop; the two are one
// invariant with two entry points.) Reaching that requires already
// controlling the API host's answers (an Enterprise install, a proxy) — an
// actor who by then holds the token anyway — so this is defense in depth, not
// a live exploit. It is here because "this client speaks its credential only
// to the host it was pointed at" is an invariant worth stating in code rather
// than in a comment about who we happen to talk to today.
//
// It sits HERE, in get, rather than in getAll: get is what turns a Link header
// into a return value a caller will follow, so validating on the way out means
// no unvalidated next link ever escapes the funnel that owns the token. A
// check in getAll would guard today's three listings and leave the next caller
// of get to reopen the hole. nextLink stays what it is — a pure header parser
// with no client, no network and no notion of origin, whose fuzz property (it
// only ever returns a substring of the header it was given) is what keeps it
// honest.
//
// The refusal is a hard CodeAPI error, never a quiet stop: silently truncating
// a page walk would hand internal/bump a short commit list and turn a wrong
// release verdict loose, which is far worse than failing the job. The message
// names both origins, because the realistic cause is a misconfigured
// Enterprise host and a CI log is all the operator gets.
func (c *Client) nextPage(header string) (string, error) {
	next := nextLink(header)
	if next == "" {
		return "", nil
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", core.APIf("github: malformed base url %q: %v", c.baseURL, err)
	}
	u, err := url.Parse(next)
	if err != nil {
		return "", core.APIf("github: malformed next-page link %q: %v", next, err)
	}
	// A relative reference is refused, not resolved. Resolving it would be safe
	// for the credential (a relative reference cannot change origin), so this is
	// a correctness call: it would mean CHOOSING a base — the configured baseURL
	// or the URL of the page that carried the header, which stop agreeing as
	// soon as the path is more than a root — i.e. guessing which page the server
	// meant. A wrong guess walks the wrong page and quietly changes a release
	// verdict. GitHub always sends absolute next links, so this shape is an
	// anomaly, and naming an anomaly beats guessing at it.
	if !u.IsAbs() {
		return "", core.APIf(
			"github: refusing to follow the relative next-page link %q — a Link header from %s must carry an absolute url",
			next, c.baseURL)
	}
	if !sameOrigin(base, u) {
		return "", core.APIf(
			"github: refusing to follow a next-page link to %s — this client is configured for %s, and its credential goes nowhere else",
			originOf(u), originOf(base))
	}
	return next, nil
}

// sameOrigin reports whether two absolute URLs address the same origin — the
// single rule both gates apply, nextPage to a Link header and checkRedirect to
// a Location, so the two can never drift into disagreeing about what "the
// configured host" means. The
// comparison is on PARSED components, never a string prefix: the whole point is
// to reject api.github.com.evil.test, which has https://api.github.com as a
// prefix. Scheme and host compare case-insensitively (RFC 3986 §3.1 and
// §3.2.2 — url.Parse already lowercases the scheme, the host it leaves
// verbatim). PATH is deliberately not compared: GitHub's own Link headers
// rewrite a /repos/{owner}/{repo}/… request into the /repositories/{id}/… form
// on later pages, so anything stricter than origin would break real pagination
// — and origin is exactly the line that decides who receives the credential.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

// effectivePort is a URL's port with the scheme's default filled in.
// https://h and https://h:443 are one origin (RFC 3986 §6.2.3), and treating
// them as two would hard-fail a legitimate walk against a host that happens to
// spell its default port out. A scheme with no known default keeps "", which
// only ever compares equal to another URL of that same scheme.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// originOf renders scheme://host[:port] verbatim for a refusal message, so the
// log names both sides of the mismatch instead of a bare "rejected".
func originOf(u *url.URL) string { return u.Scheme + "://" + u.Host }

// statusError carries a non-2xx response's status alongside the flattened
// *core.Error, so a public method can classify by status (the 422 below)
// before the failure leaves the package, and the retry loop can classify a
// rate-limit answer by its Retry-After header. Unwrap keeps it an ordinary
// CodeAPI failure to everything else — core.AsError and core.ExitCode see the
// wrapped *core.Error through the chain.
type statusError struct {
	status     int
	retryAfter string // the Retry-After header, "" when absent
	// retried marks an answer to an attempt that was NOT the first: an earlier
	// copy of this exact request already went out, so it may have been applied
	// even though its answer never came back. Only the retry loop can know that
	// (see attempt), and only a method that knows what it was ASKING for can say
	// what it means — see goneOnRetry. Read it precisely: it says an earlier
	// attempt was MADE, not that an earlier copy provably landed. A first attempt
	// that died at the socket (connection refused, DNS, TLS) never reached
	// GitHub and still sets it.
	retried bool
	err     *core.Error
}

func (e *statusError) Error() string { return e.err.Error() }
func (e *statusError) Unwrap() error { return e.err }

// flatten strips the status carrier off a failure. Every method except
// CommitPulls and Repository flattens on the way out, so a status can only ever
// be observed on a call whose contract documents it — a 422 from another
// endpoint (a pulls/{n}/commits validation failure) must never read as "commit
// unknown" and silently become a release fallback. The two exceptions each
// export one predicate over their own status (IsCommitUnknown, IsRepoUnknown),
// and nothing else OUTSIDE this package may read a status. DeleteRelease
// consults its own 404 through goneOnRetry before flattening — inside the
// package, on the one call whose contract gives that status a meaning — so no
// status escapes.
func flatten(err error) error {
	var se *statusError
	if errors.As(err, &se) {
		return se.err
	}
	return err
}

// goneOnRetry reports whether err is a 404 answering an attempt this client had
// already sent at least once — "it is not there" about a resource an earlier
// copy of the very same request may itself have removed. It is meaningful only
// to an idempotent verb whose goal IS the resource's absence (DELETE); for every
// other verb the resource was wanted, so its absence stays a failure. Kept
// unexported and opt-in for that reason: a future endpoint that deletes
// something whose absence is NOT the goal simply does not ask.
func goneOnRetry(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.retried && se.status == http.StatusNotFound
}

// IsCommitUnknown reports whether err is commits/{sha}/pulls answering 422 —
// how GitHub says it does not (yet) know the SHA. That is the release walk's
// API-lag case (the walk runs moments after a push), so the walk branches here
// to fall back instead of hard-failing the release. Deliberately NOT true for
// a 404: that is how a bad credential against a private repository answers for
// every commit, and degrading a whole walk to fallbacks on an auth failure
// would be silent corruption.
func IsCommitUnknown(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusUnprocessableEntity
}

// IsRepoUnknown reports whether err is Repository answering 404 — GitHub saying
// there is no such repository FOR THIS CREDENTIAL (it answers 404 rather than
// 403 for a private repository a credential cannot see, so the existence of the
// repository is never disclosed). It is the one failure of that read which is an
// answer ABOUT the repository rather than a failure to obtain one, which is
// exactly the line `glyph doctor` draws: a 404 is a finding the repository owns
// (exit 3), everything else — a 403 rate limit, a 5xx that outlived the retry
// schedule, a dead socket, an unparseable body — is glyph failing to observe
// anything (exit 4).
//
// The polarity is deliberate and is the fix for a real inversion: doctor used to
// call ANY error from this read a failed check, so a transient GitHub 503 exited
// 3 — "this repository is misconfigured" — at a fleet CI wrapper that branches on
// `.error.code == 3` to hard-fail and treats everything else as a retryable infra
// failure. Naming the single answering status and degrading everything else keeps
// an unclassifiable failure on the "we learned nothing" side, which is the only
// safe direction for a diagnostic.
func IsRepoUnknown(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusNotFound
}

// failed classifies a request that never produced a usable response. Only a
// *canceled* context is the user's own abort (CodeInterrupted, silent, per the
// core contract: SIGINT/SIGTERM). A deadline — which only a caller-imposed
// timeout produces — is a hung or unreachable GitHub, i.e. an ordinary API
// failure: classifying it as an interrupt would exit 130 silently and a release
// job would read that as "the operator stopped me" instead of "the API died".
func failed(ctx context.Context, what string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true}
	}
	// A redirect verdict comes back through this same door (http.Client.Do wraps
	// CheckRedirect's error in a *url.Error), and it is already a finished,
	// classified message naming both origins. Carry it out whole: re-wrapping
	// would bury the operator's answer inside `github: GET /x: Get "…": github:
	// refusing…` and, worse, flatten the carrier that tells send not to retry it.
	var rr *redirectRefused
	if errors.As(err, &rr) {
		return rr
	}
	return core.APIf("github: %s: %v", what, err)
}

// retryable reports whether one attempt's failure is worth another try: a 5xx
// (an outage or a gateway hiccup), a 429, or a 403 carrying Retry-After —
// GitHub's secondary-rate-limit signature; a bare 403 is a permission failure
// and retrying it would only burn more of the limit. A transport failure with
// a live context also qualifies (an outage resets connections as often as it
// answers 503). An interrupt never does — the user asked to stop — and every
// other 4xx is the caller's input, which a retry cannot repair.
func retryable(err error) bool {
	// A refused redirect is this package's own verdict, not the wire's: the same
	// Location would be refused three more times. Checked first, because the
	// fallback below says yes to every CodeAPI failure and this one is one.
	var rr *redirectRefused
	if errors.As(err, &rr) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.status >= 500 || se.status == http.StatusTooManyRequests ||
			(se.status == http.StatusForbidden && se.retryAfter != "")
	}
	ce := core.AsError(err)
	return ce != nil && ce.Code == core.CodeAPI
}

// retryWait picks the pause before the next attempt: the server's own
// Retry-After (whole seconds — the shape GitHub sends), capped at
// maxRetryAfter, else the schedule's delay.
func retryWait(err error, fallback time.Duration) time.Duration {
	var se *statusError
	if errors.As(err, &se) && se.retryAfter != "" {
		if n, perr := strconv.Atoi(se.retryAfter); perr == nil && n >= 0 {
			return min(time.Duration(n)*time.Second, maxRetryAfter)
		}
	}
	return fallback
}

// waitRetry sleeps d unless the request's context ends first, in which case it
// returns the context's classification (a canceled context is the user's own
// abort, a deadline an API failure — same split as failed).
func waitRetry(ctx context.Context, d time.Duration) error {
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			return nil
		}
	}
	if ctx.Err() != nil {
		return failed(ctx, "waiting to retry", ctx.Err())
	}
	return nil
}

// noteAttempts stamps a failure that survived the whole retry schedule, so a
// CI log reads "this was retried and still failed" instead of leaving open
// whether the backoff ever ran. A first-attempt failure passes untouched.
func noteAttempts(err error, attempts int) error {
	if attempts <= 1 {
		return err
	}
	if ce := core.AsError(err); ce != nil && ce.Code == core.CodeAPI {
		ce.Msg = fmt.Sprintf("%s (gave up after %d attempts)", ce.Msg, attempts)
	}
	return err
}

// maxSnippet bounds the error-body excerpt carried into a message.
const maxSnippet = 200

// distill pulls the most useful line out of a GitHub error body: its "message"
// field when the body is the standard JSON error, else a trimmed snippet. The
// snippet is cut on a rune boundary — a bare byte slice could split a multi-byte
// character, and the invalid tail would surface as U+FFFD garbage once the error
// envelope is marshaled.
func distill(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > maxSnippet {
		cut := maxSnippet
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

// nextLink extracts the rel="next" target from a GitHub Link header, or "" when
// there is no next page. Link entries look like `<url>; rel="next", <url>;
// rel="last"`.
func nextLink(header string) string {
	for entry := range strings.SplitSeq(header, ",") {
		segs := strings.Split(entry, ";")
		if len(segs) < 2 {
			continue
		}
		target := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for _, param := range segs[1:] {
			if strings.TrimSpace(param) == `rel="next"` {
				return target[1 : len(target)-1]
			}
		}
	}
	return ""
}
