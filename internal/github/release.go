package github

// This file is the release CRUD the rolling-draft upsert drives — the write
// half of the adapter. Same contract as the reads: bytes move, failures
// classify at the source (CodeAPI, or CodeInterrupted on the user's own
// cancel), and no draft policy lives here — which draft to keep, retag, or
// delete is the caller's (internal/cli) decision.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/akira-toriyama/glyph/internal/core"
)

// Release is one GitHub release as the rolling-draft upsert reads it: the id
// (the ONLY safe update/delete key — tag-name resolution can hit a published
// release sharing a draft's intended tag, cli/cli#9367), the intended tag,
// whether it is still an unpublished draft, and the html_url a human reviews
// and publishes at.
type Release struct {
	ID      int64
	TagName string
	Draft   bool
	URL     string
}

// ReleaseParams is the writable slice of a release. Draft is deliberately a
// required, always-encoded field: an accidental draft:false would PUBLISH,
// which tags the repository and (under immutable releases) freezes the tag
// forever.
type ReleaseParams struct {
	TagName string
	Target  string // target_commitish — the sha the eventual tag will point at
	Name    string
	Body    string
	Draft   bool
}

// apiRelease is the wire shape; only the fields the upsert branches on are
// decoded.
type apiRelease struct {
	ID      int64  `json:"id"`
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	URL     string `json:"html_url"`
}

// wireParams carries ReleaseParams onto the wire; kept separate so the domain
// struct stays tagless like Release and PullRef.
type wireParams struct {
	TagName string `json:"tag_name"`
	Target  string `json:"target_commitish"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
}

// Releases lists the repository's releases, drafts included, following
// pagination (GET /repos/{owner}/{repo}/releases) — what the upsert scans for
// glyph-managed drafts and for the published floor.
func (c *Client) Releases(ctx context.Context, owner, repo string) ([]Release, error) {
	first := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), perPage)
	raw, err := getAll[apiRelease](ctx, c, first)
	if err != nil {
		return nil, flatten(err)
	}
	rels := make([]Release, len(raw))
	for i, r := range raw {
		rels[i] = Release(r)
	}
	return rels, nil
}

// CreateRelease creates a release (POST /repos/{owner}/{repo}/releases) —
// with Draft true, the rolling draft: no tag exists until a human publishes.
func (c *Client) CreateRelease(ctx context.Context, owner, repo string, p ReleaseParams) (Release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	return c.writeRelease(ctx, http.MethodPost, u, p)
}

// UpdateRelease rewrites a release by id (PATCH /repos/{owner}/{repo}/releases/{id})
// — how the rolling draft grows, and how its intended tag moves when the next
// version changes (never by creating a second draft).
func (c *Client) UpdateRelease(ctx context.Context, owner, repo string, id int64, p ReleaseParams) (Release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), id)
	return c.writeRelease(ctx, http.MethodPatch, u, p)
}

// DeleteRelease removes a release by id (DELETE /repos/{owner}/{repo}/releases/{id}).
// Deleting a DRAFT burns nothing — it has no tag yet; the caller's policy
// guarantees only glyph-managed drafts ever reach here.
func (c *Client) DeleteRelease(ctx context.Context, owner, repo string, id int64) error {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return core.APIf("github: malformed request url %q: %v", u, err)
	}
	// A 204 carries no body, and none is wanted — only the status matters.
	_, _, err = c.send(req)
	return flatten(err)
}

// writeRelease performs one JSON-bodied write and decodes the release GitHub
// answers with.
func (c *Client) writeRelease(ctx context.Context, method, u string, p ReleaseParams) (Release, error) {
	payload, err := json.Marshal(wireParams(p))
	if err != nil {
		return Release{}, core.APIf("github: encoding release params: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
	if err != nil {
		return Release{}, core.APIf("github: malformed request url %q: %v", u, err)
	}
	req.Header.Set("Content-Type", "application/json")
	body, _, err := c.send(req)
	if err != nil {
		return Release{}, flatten(err)
	}
	var raw apiRelease
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, core.APIf("github: decoding %s %s: %v", method, req.URL.Path, err)
	}
	return Release(raw), nil
}

// send performs one request with the standard headers and returns the 2xx
// body and response headers — the one funnel every read and write goes
// through. Every failure is classified at this source: a canceled context is
// the user's own abort (CodeInterrupted), everything else — a transport
// error, a non-2xx status, or a truncated body — is CodeAPI (behind a
// statusError carrier the public methods flatten on the way out).
func (c *Client) send(req *http.Request) ([]byte, http.Header, error) {
	req.Header.Set("Accept", acceptJSON)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	what := req.Method + " " + req.URL.Path
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, failed(req.Context(), what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, failed(req.Context(), "reading "+what, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &statusError{status: resp.StatusCode, err: core.APIf(
			"github: %s: %d %s", what, resp.StatusCode, distill(body))}
	}
	return body, resp.Header, nil
}
