package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"
)

// TestGenerateNotesPostsTheRange: the oracle call posts the documented shape to
// the documented path and hands back the body verbatim — and an API failure
// classifies like every other call here (CodeAPI, message naming the endpoint's
// answer), so the live-fire's own plumbing failures cannot masquerade as drift.
func TestGenerateNotesPostsTheRange(t *testing.T) {
	var got struct {
		TagName         string `json:"tag_name"`
		Target          string `json:"target_commitish"`
		PreviousTagName string `json:"previous_tag_name"`
	}
	c := newClient(t, "tok", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/releases/generate-notes" {
			t.Errorf("got %s %s, want POST /repos/o/r/releases/generate-notes", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("payload is not JSON: %v", err)
		}
		w.Write([]byte(`{"name":"vNext","body":"* fix by @a in https://github.com/o/r/pull/7"}`))
	})
	body, err := c.GenerateNotes(context.Background(), "o", "r", NotesParams{
		TagName: "probe", Target: "abc123", PreviousTagName: "v1.0.0",
	})
	if err != nil {
		t.Fatalf("GenerateNotes: %v", err)
	}
	if got.TagName != "probe" || got.Target != "abc123" || got.PreviousTagName != "v1.0.0" {
		t.Fatalf("posted %+v, want the params verbatim", got)
	}
	if body != "* fix by @a in https://github.com/o/r/pull/7" {
		t.Fatalf("body = %q, want the API's body verbatim", body)
	}

	failing := newClient(t, "tok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})
	_, err = failing.GenerateNotes(context.Background(), "o", "r", NotesParams{TagName: "probe"})
	wantAPIError(t, err, "404")
}

// TestPullsCited pins what counts as GitHub citing a pull: the /pull/<n> URL
// path — never "#N" prose, which legitimately appears inside pull TITLES and
// must not read as a citation. Distinct, ascending.
func TestPullsCited(t *testing.T) {
	body := "## What's Changed\n" +
		"* fix the walk by @a in https://github.com/o/r/pull/12\n" +
		"* follow-up to #12 and #99 by @b in https://github.com/o/r/pull/7\n" +
		"* same pull cited twice in https://github.com/o/r/pull/7\n" +
		"**Full Changelog**: https://github.com/o/r/compare/v1.0.0...v1.1.0\n"
	if got, want := pullsCited(body), []int{7, 12}; !slices.Equal(got, want) {
		t.Fatalf("pullsCited = %v, want %v (URLs only — the #99 in a title is not a citation — deduped, ascending)", got, want)
	}
	if got := pullsCited(""); len(got) != 0 {
		t.Fatalf("an empty body cites nothing, got %v", got)
	}
}
