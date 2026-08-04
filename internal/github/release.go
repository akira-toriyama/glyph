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
//
// The create is the one write here whose blind replay is not idempotent: a
// draft has no tag for GitHub to collide on, so every re-sent copy of a POST
// whose answer was lost mints another identical draft — one 503-answered
// create measured leaving two, and a spent schedule four, for a human to pick
// through (t-ph6p). So before each re-send, createLanded asks whether the
// earlier copy already reached its goal, and a found release is adopted as
// this create's answer instead of a sibling being minted. On a probe that
// finds nothing — or cannot look — the loop falls back to what it always did:
// re-send, and let the upsert's convergence delete a duplicate on the next
// run. The probe narrows that backstop to the runs where glyph could not
// observe its own work; it does not replace it.
func (c *Client) CreateRelease(ctx context.Context, owner, repo string, p ReleaseParams) (Release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	return c.writeRelease(ctx, http.MethodPost, u, p, func() ([]byte, bool) {
		return c.createLanded(ctx, owner, repo, p)
	})
}

// UpdateRelease rewrites a release by id (PATCH /repos/{owner}/{repo}/releases/{id})
// — how the rolling draft grows, and how its intended tag moves when the next
// version changes (never by creating a second draft).
func (c *Client) UpdateRelease(ctx context.Context, owner, repo string, id int64, p ReleaseParams) (Release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), id)
	return c.writeRelease(ctx, http.MethodPatch, u, p, nil)
}

// DeleteRelease removes a release by id (DELETE /repos/{owner}/{repo}/releases/{id}).
// Deleting a DRAFT burns nothing — it has no tag yet; the caller's policy
// guarantees only glyph-managed drafts ever reach here.
//
// alreadyGone is true when the release turned out to be absent on a RETRIED
// attempt: the delete reached its goal, but glyph never saw its own request
// succeed. The caller owns what to say about that — the two are not the same
// claim, and one of them is reported to a human.
func (c *Client) DeleteRelease(ctx context.Context, owner, repo string, id int64) (alreadyGone bool, err error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return false, core.APIf("github: malformed request url %q: %v", u, err)
	}
	// A 204 carries no body, and none is wanted — only the status matters.
	_, _, err = c.send(req)
	// A 404 answering a RETRIED delete is this delete's own goal, reported late.
	// send replays a DELETE whenever the first answer is lost (a 503 from a
	// gateway that had already forwarded it, a reset socket), and the replay is
	// told the release is gone — which is what was asked for. Failing there
	// aborted a whole draft upsert over work that had SUCCEEDED, leaving the
	// rolling draft without its new notes (t-yq7m).
	//
	// The ambiguity is real and deliberately accepted: 404 is also how GitHub
	// answers for a repository this credential can no longer see (IsRepoUnknown),
	// and a DELETE cannot tell the two apart. Hence alreadyGone rather than a
	// silent nil — the caller must not claim it deleted something it did not
	// watch disappear. A 404 on the FIRST attempt is untouched: nothing of
	// glyph's removed that id, so it stays the "it vanished under us" failure the
	// convergence claim depends on hearing.
	if goneOnRetry(err) {
		return true, nil
	}
	return false, flatten(err)
}

// writeRelease performs one JSON-bodied write and decodes the release GitHub
// answers with. recovered, when non-nil, is the write's recovery probe — see
// sendRecovering; nil for the idempotent verbs.
func (c *Client) writeRelease(ctx context.Context, method, u string, p ReleaseParams, recovered func() ([]byte, bool)) (Release, error) {
	payload, err := json.Marshal(wireParams(p))
	if err != nil {
		return Release{}, core.APIf("github: encoding release params: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
	if err != nil {
		return Release{}, core.APIf("github: malformed request url %q: %v", u, err)
	}
	req.Header.Set("Content-Type", "application/json")
	body, _, err := c.sendRecovering(req, recovered)
	if err != nil {
		return Release{}, flatten(err)
	}
	var raw apiRelease
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, core.APIf("github: decoding %s %s: %v", method, req.URL.Path, err)
	}
	return Release(raw), nil
}

// createLanded is CreateRelease's recovery probe: has a release matching what
// the create asked for — the intended tag, in the requested draft state —
// appeared? Listing is the only way to see a draft (it has no tag to GET by),
// and the client's retries are stripped so the probe costs ONE round trip: the
// calling loop already owns the pacing, and a probe walking its own backoff
// schedule would multiply an outage's wall clock by the schedule's length.
// Any failure to look is a miss — the caller re-sends, which is exactly the
// pre-probe behaviour.
//
// The match is deliberately just (tag, draft) and not the body: the upsert
// only creates when no such release existed, and a replay is byte-identical
// to the copy that landed (see send: GetBody rewinds the same payload), so a
// match is the earlier copy's own work — or a concurrent run's identical
// draft, which is the state the upsert converges on anyway.
func (c *Client) createLanded(ctx context.Context, owner, repo string, p ReleaseParams) ([]byte, bool) {
	single := *c
	single.retryDelays = nil
	rels, err := single.Releases(ctx, owner, repo)
	if err != nil {
		return nil, false
	}
	for _, r := range rels {
		if r.Draft == p.Draft && r.TagName == p.TagName {
			body, merr := json.Marshal(apiRelease(r))
			if merr != nil {
				return nil, false
			}
			return body, true
		}
	}
	return nil, false
}

// send performs one logical request with the standard headers and returns the
// 2xx body and response headers — the one funnel every read and write goes
// through. Transient failures (a 5xx, a rate-limit answer, a transport error)
// are retried on the client's backoff schedule before the failure escapes:
// GitHub's minor outages answer 503 for tens of seconds, and hard-failing a
// whole verdict job on one such answer traded a 16-second wait for a wave of
// manual reruns (t-bjrv). Retrying a write is safe here because every write
// names its key: a replayed PATCH rewrites the same release, and a replayed
// DELETE whose first copy already landed is read through goneOnRetry. The one
// verb that has no key to name — the create, whose key GitHub mints in the
// answer — recovers a lost answer through sendRecovering's probe instead of
// replaying blind. Every failure is classified at this source: a canceled
// context is the user's own abort (CodeInterrupted), everything else — a
// transport error, a non-2xx status, or a truncated body — is CodeAPI (behind
// a statusError carrier the public methods flatten on the way out).
func (c *Client) send(req *http.Request) ([]byte, http.Header, error) {
	return c.sendRecovering(req, nil)
}

// sendRecovering is send with a recovery probe for a write whose blind replay
// could apply twice. After an attempt fails and is judged worth another — and
// before any backoff is spent on it — recovered is asked whether the earlier
// copy, whose answer was lost, had in fact reached its goal; a body answered
// true IS the request's answer, no further copy is sent, and no response
// headers are carried (a recovered answer has none). The split mirrors
// statusError.retried / goneOnRetry: only this loop knows an earlier copy went
// out, and only a caller that knows what it was asking for can recognise the
// goal — here for the verb whose goal is the resource's presence, as
// goneOnRetry is for the verb whose goal is its absence (t-ph6p).
func (c *Client) sendRecovering(req *http.Request, recovered func() ([]byte, bool)) ([]byte, http.Header, error) {
	req.Header.Set("Accept", acceptJSON)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	what := req.Method + " " + req.URL.Path
	// A body can only be replayed through GetBody (http.NewRequest sets it for
	// the in-memory readers every write here uses); without it a retry would
	// send an empty body, which is worse than not retrying.
	replayable := req.Body == nil || req.GetBody != nil
	for attempt := 0; ; attempt++ {
		body, header, err := c.attempt(req, what, attempt)
		if err == nil {
			return body, header, nil
		}
		if attempt >= len(c.retryDelays) || !replayable || !retryable(err) {
			return nil, nil, noteAttempts(err, attempt+1)
		}
		if recovered != nil {
			if landed, ok := recovered(); ok {
				return landed, nil, nil
			}
		}
		if werr := waitRetry(req.Context(), retryWait(err, c.retryDelays[attempt])); werr != nil {
			// The context ended mid-backoff. A cancel is the user's abort and
			// carries out as such; a deadline means the caller's patience ran
			// out first, and the LAST real failure is the informative one.
			if ce := core.AsError(werr); ce != nil && ce.Code == core.CodeInterrupted {
				return nil, nil, werr
			}
			return nil, nil, noteAttempts(err, attempt+1)
		}
	}
}

// attempt performs one wire round-trip. On a retry the consumed body is
// rewound via GetBody onto a fresh clone; the standard headers set by send
// ride along on the clone.
func (c *Client) attempt(req *http.Request, what string, n int) ([]byte, http.Header, error) {
	r := req
	if n > 0 && req.GetBody != nil {
		fresh, err := req.GetBody()
		if err != nil {
			return nil, nil, core.APIf("github: %s: rewinding the request body for a retry: %v", what, err)
		}
		r = req.Clone(req.Context())
		r.Body = fresh
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, nil, failed(req.Context(), what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, failed(req.Context(), "reading "+what, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &statusError{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"),
			retried: n > 0, err: core.APIf(
				"github: %s: %d %s", what, resp.StatusCode, distill(body))}
	}
	return body, resp.Header, nil
}
