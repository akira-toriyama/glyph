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
	"io"
	"net/http"
	"net/url"
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

// defaultTimeout bounds each request when no client is injected —
// http.DefaultClient would hang on an unresponsive GitHub until the CI runner
// kills the whole job. Per-request (not per-walk): the release walk makes one
// round-trip per squash commit, and each deserves the full allowance. When it
// fires, the deadline classifies as an ordinary API failure (see failed).
const defaultTimeout = 30 * time.Second

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
// tests, or a rate-limit/timeout-tuned client in production.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// Client talks to one GitHub REST host with one credential.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

// New builds a client. token is sent as a Bearer credential when non-empty; an
// empty token still reads public repos, at the anonymous rate limit.
func New(token string, opts ...Option) *Client {
	c := &Client{baseURL: DefaultBaseURL, http: &http.Client{Timeout: defaultTimeout}, token: token}
	for _, o := range opts {
		o(c)
	}
	return c
}

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
		return nil, err
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

// get performs one GET, decodes a 2xx body into into, and returns the next-page
// URL from the Link header ("" when there is none). Every failure is classified
// at this source: a canceled context is CodeInterrupted, everything else — a
// transport error, a non-2xx status, or a decode failure — is CodeAPI.
func (c *Client) get(ctx context.Context, u string, into any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", core.APIf("github: malformed request url %q: %v", u, err)
	}
	req.Header.Set("Accept", acceptJSON)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", failed(ctx, "GET "+req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", failed(ctx, "reading "+req.URL.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", core.APIf("github: GET %s: %d %s", req.URL.Path, resp.StatusCode, distill(body))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return "", core.APIf("github: decoding %s: %v", req.URL.Path, err)
	}
	return nextLink(resp.Header.Get("Link")), nil
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
	return core.APIf("github: %s: %v", what, err)
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
	for _, entry := range strings.Split(header, ",") {
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
