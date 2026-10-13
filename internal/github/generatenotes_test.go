package github

// GitHub's own release-notes composer, kept as an ORACLE and nothing else.
// glyph never ships this text — internal/notes owns what a release body says —
// but the squash→PR resolution the walk stands on (prior art, DESIGN §1)
// is a model of GitHub behaviour, and the house rule for such models is one
// test that asks the real system. generate-notes is that system answering the
// same question from its own side: which merged pulls does this range hold.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// NotesParams names the range generate-notes composes over. TagName may name
// a tag that does not exist yet — with Target set, GitHub computes as if that
// tag were about to be cut there, and creates NOTHING either way: the endpoint
// is a pure computation, which is what makes it safe to point at a sandbox
// from a read-only oracle.
type NotesParams struct {
	TagName         string
	Target          string // target_commitish; may be empty when TagName exists
	PreviousTagName string
}

// GenerateNotes asks the real GitHub to compose release notes for a range
// (POST /repos/{owner}/{repo}/releases/generate-notes) and returns the body.
// Live-fire oracle use only — see the file comment.
func (c *Client) GenerateNotes(ctx context.Context, owner, repo string, p NotesParams) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/generate-notes",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	payload, err := json.Marshal(struct {
		TagName         string `json:"tag_name"`
		Target          string `json:"target_commitish,omitempty"`
		PreviousTagName string `json:"previous_tag_name,omitempty"`
	}{p.TagName, p.Target, p.PreviousTagName})
	if err != nil {
		return "", core.APIf("github: encoding generate-notes params: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return "", core.APIf("github: malformed request url %q: %v", u, err)
	}
	req.Header.Set("Content-Type", "application/json")
	body, _, err := c.send(req)
	if err != nil {
		return "", flatten(err)
	}
	var out struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", core.APIf("github: generate-notes answered non-JSON: %v", err)
	}
	return out.Body, nil
}

// pullURLRE finds the pull a generated line cites: generate-notes writes each
// entry as "* <title> by @<login> in https://<host>/<owner>/<repo>/pull/<n>",
// so the /pull/<n> path segment is the machine-readable part of the prose.
var pullURLRE = regexp.MustCompile(`/pull/([0-9]+)\b`)

// pullsCited extracts the distinct pull numbers a generate-notes body cites,
// ascending. It reads URLs and not "#N" on purpose: the body legitimately
// carries #N-shaped text inside pull TITLES, and a title mentioning another
// pull must not count as GitHub citing that pull.
func pullsCited(body string) []int {
	seen := map[int]bool{}
	var out []int
	for _, m := range pullURLRE.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

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
