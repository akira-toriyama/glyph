package github

// This file is the single-pull read `glyph lint --pr` judges a squash title
// through. Same contract as the rest of the adapter: bytes move, failures
// classify at the source, and no policy lives here — what a title must look
// like, and whose titles are excluded, is the caller's (internal/cli, via
// internal/parser and internal/bump) decision.

import (
	"context"
	"fmt"
	"net/url"
)

// Pull is the slice of GET /repos/{owner}/{repo}/pulls/{number} the squash-title
// lint reads: the title a squash merge will record as the landed commit's
// subject, and the author whose bot-ness excludes it from classification.
type Pull struct {
	Title  string
	Author string
}

// apiOnePull is the wire shape; only the fields the title lint reads are
// decoded. Unlike apiPull/PullRef this is NOT field-for-field convertible to
// its domain type — the author sits nested under user.login — so it flattens,
// the way apiCommit→Commit does.
type apiOnePull struct {
	Title string `json:"title"`
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
}

// PullRequest reads one pull request (GET /repos/{owner}/{repo}/pulls/{number})
// — one request, no pagination — and returns its authoring surface.
func (c *Client) PullRequest(ctx context.Context, owner, repo string, number int) (Pull, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), number)
	var raw apiOnePull
	if _, err := c.get(ctx, u, &raw); err != nil {
		return Pull{}, err
	}
	return Pull{Title: raw.Title, Author: raw.User.Login}, nil
}
