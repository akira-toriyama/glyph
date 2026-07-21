package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// TestReleasesLists decodes the fields the rolling-draft upsert branches on —
// id (the only safe delete/update key), tag_name, draft, html_url — and
// follows pagination like every other listing.
func TestReleasesLists(t *testing.T) {
	var srvURL string
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if got := r.URL.Path; got != "/repos/akira-toriyama/glyph/releases" {
			t.Errorf("path = %q, want the releases path", got)
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/akira-toriyama/glyph/releases?page=2>; rel="next"`, srvURL))
			fmt.Fprint(w, `[{"id":11,"tag_name":"v0.4.0","draft":true,"html_url":"https://github.com/x/releases/11"}]`)
		case "2":
			fmt.Fprint(w, `[{"id":7,"tag_name":"v0.3.0","draft":false,"html_url":"https://github.com/x/releases/7"}]`)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	})
	srvURL = c.baseURL

	rels, err := c.Releases(context.Background(), "akira-toriyama", "glyph")
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}
	want := []Release{
		{ID: 11, TagName: "v0.4.0", Draft: true, URL: "https://github.com/x/releases/11"},
		{ID: 7, TagName: "v0.3.0", Draft: false, URL: "https://github.com/x/releases/7"},
	}
	if len(rels) != 2 || rels[0] != want[0] || rels[1] != want[1] {
		t.Fatalf("releases = %+v, want %+v", rels, want)
	}
}

// TestCreateReleasePostsTheDraft pins the write wire shape: POST to the
// releases collection carrying tag_name, target_commitish, name, body and
// draft — the draft flag must actually be encoded (a missing field would
// PUBLISH the release, tagging the repo and freezing it immutable).
func TestCreateReleasePostsTheDraft(t *testing.T) {
	var got map[string]any
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/repos/akira-toriyama/glyph/releases" {
			t.Errorf("path = %q, want the releases collection", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":42,"tag_name":"v0.4.0","draft":true,"html_url":"https://github.com/x/releases/42"}`)
	})

	rel, err := c.CreateRelease(context.Background(), "akira-toriyama", "glyph", ReleaseParams{
		TagName: "v0.4.0", Target: "abc123", Name: "v0.4.0", Body: "## Fixes\n", Draft: true,
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	want := Release{ID: 42, TagName: "v0.4.0", Draft: true, URL: "https://github.com/x/releases/42"}
	if rel != want {
		t.Fatalf("release = %+v, want %+v", rel, want)
	}
	for k, v := range map[string]any{
		"tag_name": "v0.4.0", "target_commitish": "abc123", "name": "v0.4.0",
		"body": "## Fixes\n", "draft": true,
	} {
		if got[k] != v {
			t.Errorf("request body %s = %v, want %v", k, got[k], v)
		}
	}
}

// TestUpdateReleasePatchesByID: the update targets the release ID, never the
// tag — a draft has no real tag yet, and tag-name resolution is exactly the
// gh bug (cli/cli#9367) that can hit a published release sharing the name.
func TestUpdateReleasePatchesByID(t *testing.T) {
	var got map[string]any
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		if r.URL.Path != "/repos/akira-toriyama/glyph/releases/42" {
			t.Errorf("path = %q, want the release-by-id path", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		fmt.Fprint(w, `{"id":42,"tag_name":"v0.5.0","draft":true,"html_url":"https://github.com/x/releases/42"}`)
	})

	rel, err := c.UpdateRelease(context.Background(), "akira-toriyama", "glyph", 42, ReleaseParams{
		TagName: "v0.5.0", Target: "def456", Name: "v0.5.0", Body: "## Features\n", Draft: true,
	})
	if err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	if rel.ID != 42 || rel.TagName != "v0.5.0" {
		t.Fatalf("release = %+v, want id 42 retagged v0.5.0", rel)
	}
	if got["tag_name"] != "v0.5.0" || got["draft"] != true {
		t.Errorf("request body = %+v, want tag_name v0.5.0 and draft true", got)
	}
}

// TestDeleteReleaseDeletesByID: the delete also goes by ID (same cli/cli#9367
// reasoning), and GitHub answers 204 with no body — that must read as success,
// not as a decode failure.
func TestDeleteReleaseDeletesByID(t *testing.T) {
	deleted := false
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/repos/akira-toriyama/glyph/releases/11" {
			t.Errorf("path = %q, want the release-by-id path", r.URL.Path)
		}
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	})

	gone, err := c.DeleteRelease(context.Background(), "akira-toriyama", "glyph", 11)
	if err != nil {
		t.Fatalf("DeleteRelease: %v", err)
	}
	if gone {
		t.Error("a 204 is glyph watching its own delete succeed, so alreadyGone must be false")
	}
	if !deleted {
		t.Fatal("no DELETE request reached the server")
	}
}

// TestCreateReleaseFailureIsAPIError: a rejected write (422 — e.g. an
// already-existing tag) is an ordinary CodeAPI failure carrying GitHub's
// message, and it must NOT read as the walk's commit-unknown branch.
func TestCreateReleaseFailureIsAPIError(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message":"Validation Failed"}`)
	})
	_, err := c.CreateRelease(context.Background(), "o", "r", ReleaseParams{TagName: "v1.0.0", Draft: true})
	wantAPIError(t, err, "Validation Failed")
	if IsCommitUnknown(err) {
		t.Fatal("a 422 from a release write must NOT report IsCommitUnknown")
	}
}

// TestDeleteReleaseFailureIsAPIError: a failed delete (404 — the id vanished
// under us) surfaces, never a silent no-op: the upsert's convergence claim
// ("exactly one draft remains") would otherwise be a lie.
func TestDeleteReleaseFailureIsAPIError(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})
	gone, err := c.DeleteRelease(context.Background(), "o", "r", 99)
	wantAPIError(t, err, "Not Found")
	if gone {
		t.Error("a FIRST-attempt 404 is the id vanishing under us, never this delete's own success")
	}
}

// TestCreateReleaseCanceledIsInterrupted: the cancel/deadline split holds on
// the write path exactly as on the reads — a canceled context is the user's
// own Ctrl-C (130, silent), never an API failure.
func TestCreateReleaseCanceledIsInterrupted(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CreateRelease(ctx, "o", "r", ReleaseParams{TagName: "v1.0.0", Draft: true})
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("error = %v, want CodeInterrupted (%d)", err, core.CodeInterrupted)
	}
}
