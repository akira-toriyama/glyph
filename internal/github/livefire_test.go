package github

// THE live-fire oracle (t-2e11). Every other test in this package speaks to
// an httptest fake, and a fake affirms glyph's assumptions for ever — it is
// glyph's model of GitHub, answering questions about itself. House rule
// (stated at internal/parser's `git stripspace` oracle): code that models an
// external system's behaviour carries one test that asks the real system.
// This is that one test for the GitHub adapter: the day it goes red is the
// day the fakes' shared premises drifted from the real API — the day the
// other ~50 tests here started proving the wrong thing while staying green.
//
// Gated on GLYPH_LIVE_REPO (an owner/name sandbox the token may write to —
// the fleet's is akira-toriyama/glyph-test) and skipped everywhere else, so
// `go test ./...` stays hermetic. No build tag: an env-gated t.Skip keeps the
// file compiling in every ordinary run, so it cannot rot the way tagged files
// do. It runs on a schedule from the sandbox repo, not from glyph's PR CI —
// its verdict dates GitHub's drift, not a diff.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// livefirePrefix names the throwaway drafts this test mints, and ONLY this
// test: the sweep below deletes every draft carrying it, so the prefix must
// never collide with a tag a human or the rolling-draft upsert would create
// (those are vX.Y.Z).
const livefirePrefix = "livefire-2e11-"

// liveClient builds the client the way internal/cli does — token from
// GITHUB_TOKEN else GH_TOKEN, base URL from GITHUB_API_URL — or skips.
func liveClient(t *testing.T) (*Client, string, string) {
	t.Helper()
	repo := os.Getenv("GLYPH_LIVE_REPO")
	if repo == "" {
		t.Skip("GLYPH_LIVE_REPO is unset; the live-fire oracle only runs against a designated sandbox")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		t.Fatalf("GLYPH_LIVE_REPO = %q, want owner/name", repo)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		t.Fatal("GLYPH_LIVE_REPO is set but no GITHUB_TOKEN/GH_TOKEN; the oracle cannot write anonymously")
	}
	var opts []Option
	if base := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); base != "" {
		opts = append(opts, WithBaseURL(base))
	}
	return New(token, opts...), owner, name
}

// listedDraft polls the releases listing until want(rels) holds or the
// deadline passes, returning the last listing either way. The real listing is
// read-after-write consistent almost always; the poll absorbs the "almost".
func listedDraft(ctx context.Context, t *testing.T, c *Client, owner, name string, want func([]Release) bool) []Release {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rels, err := c.Releases(ctx, owner, name)
		if err != nil {
			t.Fatalf("listing releases: %v", err)
		}
		if want(rels) || time.Now().After(deadline) {
			return rels
		}
		time.Sleep(2 * time.Second)
	}
}

// TestLiveFireDraftLifecycle is the contract the rolling-draft upsert stands
// on, asked of the real API: a draft create is answered with an id, the draft
// appears in the paginated listing with draft:true, a delete by id succeeds,
// a FRESH delete of the now-absent id fails as a 404 (the "it vanished under
// us" arm — t-yq7m's alreadyGone absorption is scoped to a retried attempt
// INSIDE one call, never to a first attempt), and the listing converges to
// not carrying it.
func TestLiveFireDraftLifecycle(t *testing.T) {
	c, owner, name := liveClient(t)
	ctx := context.Background()

	// Converge strays from a previous run that died mid-test: this oracle owns
	// every livefire- draft in the sandbox, so sweeping them is safe and makes
	// the test self-healing instead of wedged until a human tidies up.
	for _, r := range listedDraft(ctx, t, c, owner, name, func([]Release) bool { return true }) {
		if r.Draft && strings.HasPrefix(r.TagName, livefirePrefix) {
			if _, err := c.DeleteRelease(ctx, owner, name, r.ID); err != nil {
				t.Fatalf("sweeping stray %s (id %d): %v", r.TagName, r.ID, err)
			}
			t.Logf("swept stray %s (id %d) from a previous run", r.TagName, r.ID)
		}
	}

	tag := fmt.Sprintf("%s%d", livefirePrefix, time.Now().UnixNano())
	target := os.Getenv("GLYPH_LIVE_TARGET")
	if target == "" {
		// glyph release always sends a concrete sha; a branch name is equally
		// inside the documented target_commitish contract and spares the
		// oracle a second API surface just to learn one.
		target = "main"
	}

	rel, err := c.CreateRelease(ctx, owner, name, ReleaseParams{
		TagName: tag,
		Target:  target,
		Name:    tag,
		Body:    "glyph live-fire oracle (t-2e11): created and deleted by internal/github's contract test; delete freely if found.",
		Draft:   true,
	})
	if err != nil {
		t.Fatalf("creating the draft: %v", err)
	}
	// Whatever happens below, do not leave the draft standing.
	t.Cleanup(func() {
		_, _ = c.DeleteRelease(context.Background(), owner, name, rel.ID)
	})
	if rel.ID == 0 || !rel.Draft || rel.TagName != tag || rel.URL == "" {
		t.Fatalf("create answered %+v, want a draft id with tag %s and an html_url", rel, tag)
	}

	// The listing the upsert scans must carry the draft it just wrote.
	rels := listedDraft(ctx, t, c, owner, name, func(rels []Release) bool {
		for _, r := range rels {
			if r.ID == rel.ID {
				return true
			}
		}
		return false
	})
	found := false
	for _, r := range rels {
		if r.ID == rel.ID {
			found = true
			if !r.Draft || r.TagName != tag {
				t.Errorf("listing carries id %d as draft=%t tag=%q, want draft=true tag=%q "+
					"— the glyph-managed-draft scan reads exactly these fields", r.ID, r.Draft, r.TagName, tag)
			}
		}
	}
	if !found {
		t.Fatalf("the draft (id %d) never appeared in the releases listing; the upsert's whole "+
			"convergence scan reads that listing", rel.ID)
	}

	// Delete by id — the only safe key (cli/cli#9367).
	gone, err := c.DeleteRelease(ctx, owner, name, rel.ID)
	if err != nil {
		t.Fatalf("deleting the draft: %v", err)
	}
	if gone {
		t.Errorf("first delete of id %d reported already-gone; want a real deletion", rel.ID)
	}

	// A FRESH delete of the absent id must come back a 404 error, never
	// alreadyGone: t-yq7m's absorption is scoped to a retried attempt inside
	// ONE call (glyph re-asking about its own delete), and a first attempt
	// answered 404 is the "it vanished under us" failure the convergence claim
	// depends on hearing. This pins the real API's 404 flowing through that
	// arm — measured red here on 2026-08-08 when the oracle's first draft
	// asserted the opposite contract.
	gone, err = c.DeleteRelease(ctx, owner, name, rel.ID)
	if err == nil {
		t.Errorf("re-deleting the absent id %d succeeded (alreadyGone=%t); a 404 on a FIRST "+
			"attempt must stay an error, or convergence claims deletions it never watched happen", rel.ID, gone)
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("re-deleting the absent id %d failed with %v, want the API's 404 shape", rel.ID, err)
	}

	// And the listing converges to a world without it.
	rels = listedDraft(ctx, t, c, owner, name, func(rels []Release) bool {
		for _, r := range rels {
			if r.ID == rel.ID {
				return false
			}
		}
		return true
	})
	for _, r := range rels {
		if r.ID == rel.ID {
			t.Errorf("id %d still in the listing after a successful delete", rel.ID)
		}
	}
}
