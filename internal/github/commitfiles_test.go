package github

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestCommitFilesSinglePage: the files array of GET commits/{sha}, a rename
// under both of its names, and the path the walk's inner commits are asked
// on — the commit itself, not its pulls sub-resource.
func TestCommitFilesSinglePage(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/akira-toriyama/glyph/commits/deadbeef" {
			t.Errorf("path = %q, want the commits/{sha} path", got)
		}
		if r.URL.Query().Has("per_page") {
			t.Errorf("per_page was sent; the endpoint's own 300-file page is the point")
		}
		fmt.Fprint(w, `{"sha":"deadbeef","files":[
		  {"filename":"haiku/h.go","status":"modified"},
		  {"filename":"curry/h.go","previous_filename":"haiku/old.go","status":"renamed"},
		  {"filename":"README.md","status":"added"}
		]}`)
	})

	files, capped, err := c.CommitFiles(context.Background(), "akira-toriyama", "glyph", "deadbeef")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	want := []string{"haiku/h.go", "curry/h.go", "haiku/old.go", "README.md"}
	if !slices.Equal(files, want) {
		t.Fatalf("files = %q, want %q (a rename under both names, in listing order)", files, want)
	}
	if capped {
		t.Fatalf("four files reported as capped")
	}
}

// TestCommitFilesPaginates follows the Link header — each page carries the
// whole commit object with a different slice of files (measured 2026-09-10 on
// torvalds/linux's root commit: 300 per page, rel="next" up to page 10).
func TestCommitFilesPaginates(t *testing.T) {
	var srvURL string
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/s?page=2>; rel="next", <%s/repos/o/r/commits/s?page=2>; rel="last"`, srvURL, srvURL))
			fmt.Fprint(w, `{"sha":"s","files":[{"filename":"a"}]}`)
		case "2":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/s?page=1>; rel="prev", <%s/repos/o/r/commits/s?page=1>; rel="first"`, srvURL, srvURL))
			fmt.Fprint(w, `{"sha":"s","files":[{"filename":"b"}]}`)
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	})
	srvURL = c.baseURL

	files, _, err := c.CommitFiles(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if !slices.Equal(files, []string{"a", "b"}) {
		t.Fatalf("pagination = %q, want a then b", files)
	}
}

// TestCommitFilesCapIsReported: a listing of exactly CommitFilesCap files is
// one GitHub may have truncated (measured: page 11 of a 17k-file commit is
// empty), so it is reported capped — the caller records an incomplete walk.
// One file short of the cap is whole.
func TestCommitFilesCapIsReported(t *testing.T) {
	for _, n := range []int{CommitFilesCap - 1, CommitFilesCap} {
		entries := make([]string, 0, n)
		for i := range n {
			entries = append(entries, fmt.Sprintf(`{"filename":"f%d"}`, i))
		}
		c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"sha":"s","files":[`+strings.Join(entries, ",")+`]}`)
		})
		files, capped, err := c.CommitFiles(context.Background(), "o", "r", "s")
		if err != nil {
			t.Fatalf("CommitFiles(%d files): %v", n, err)
		}
		if len(files) != n {
			t.Fatalf("CommitFiles(%d files) returned %d", n, len(files))
		}
		if want := n == CommitFilesCap; capped != want {
			t.Fatalf("CommitFiles(%d files) capped = %v, want %v", n, capped, want)
		}
	}
}

// TestCommitFilesEmptyListingIsNotNil: a commit touching nothing (an empty
// commit) answers [] rather than nil, and is not capped — the attribution
// reads it as "under no package", never as "unknown".
func TestCommitFilesEmptyListingIsNotNil(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sha":"s","files":[]}`)
	})
	files, capped, err := c.CommitFiles(context.Background(), "o", "r", "s")
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if files == nil || len(files) != 0 || capped {
		t.Fatalf("CommitFiles on an empty listing = %v (capped %v), want [] uncapped", files, capped)
	}
}

// TestCommitFiles422IsCommitUnknown: the status is NOT flattened here — a 422
// from commits/{sha} means what it means on commits/{sha}/pulls, GitHub not
// knowing the sha — so IsCommitUnknown can read it. A 404 stays a plain
// failure, as on the pulls sub-resource.
func TestCommitFiles422IsCommitUnknown(t *testing.T) {
	for status, want := range map[int]bool{http.StatusUnprocessableEntity: true, http.StatusNotFound: false} {
		c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"No commit found for SHA"}`)
		})
		_, _, err := c.CommitFiles(context.Background(), "o", "r", "s")
		if err == nil {
			t.Fatalf("status %d: CommitFiles returned no error", status)
		}
		if got := IsCommitUnknown(err); got != want {
			t.Fatalf("status %d: IsCommitUnknown = %v, want %v (%v)", status, got, want, err)
		}
	}
}
