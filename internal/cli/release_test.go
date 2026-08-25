package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/github"
)

// releasesPath is the releases collection for the repository the tests query.
const releasesPath = "/repos/akira-toriyama/glyph/releases"

// apiWrite is one recorded write against the fake releases surface.
type apiWrite struct {
	method string
	path   string
	body   map[string]any
}

// releaseServer extends walkServer with the releases surface: GET serves the
// canned collection, writes (POST/PATCH/DELETE) are recorded into *writes and
// answered the way GitHub answers them — so a test asserts on the exact write
// sequence, and a dry-run test proves the sequence stayed empty.
func releaseServer(t *testing.T, walk map[string]string, releases string, writes *[]apiWrite) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(releaseHandler(t, walk, releases, writes))
	t.Cleanup(srv.Close)
	return srv
}

// releaseHandler is releaseServer's body, split out so a test can put another
// route in front of the same surface (releaseref_test.go serves the repository
// object ahead of it) without a second copy of the write recorder.
func releaseHandler(t *testing.T, walk map[string]string, releases string, writes *[]apiWrite) http.HandlerFunc {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == releasesPath:
			fmt.Fprint(w, releases)
		case r.Method == http.MethodGet:
			body, ok := walk[r.URL.Path]
			if !ok {
				t.Errorf("unexpected GET %q", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			if body == apiUnknownSHA {
				// walkServer's sentinel, honoured here too: a commit GitHub does
				// not know yet answers 422, and the release tests need that shape
				// beside the releases surface rather than only beside the walk.
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprint(w, `{"message":"No commit found for SHA"}`)
				return
			}
			fmt.Fprint(w, body)
		case r.Method == http.MethodPost && r.URL.Path == releasesPath:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("POST body is not JSON: %v", err)
			}
			*writes = append(*writes, apiWrite{"POST", r.URL.Path, body})
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":900,"tag_name":%q,"draft":true,"html_url":"https://github.example/releases/900"}`, body["tag_name"])
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, releasesPath+"/"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("PATCH body is not JSON: %v", err)
			}
			*writes = append(*writes, apiWrite{"PATCH", r.URL.Path, body})
			id := strings.TrimPrefix(r.URL.Path, releasesPath+"/")
			fmt.Fprintf(w, `{"id":%s,"tag_name":%q,"draft":true,"html_url":"https://github.example/releases/%s"}`, id, body["tag_name"], id)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, releasesPath+"/"):
			*writes = append(*writes, apiWrite{"DELETE", r.URL.Path, nil})
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %q", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

// draftJSON / publishedJSON render one release in the shape GET /releases
// returns.
func draftJSON(id int, tag string) string {
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"draft":true,"html_url":"https://github.example/releases/%d"}`, id, tag, id)
}

func publishedJSON(id int, tag string) string {
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"draft":false,"html_url":"https://github.example/releases/%d"}`, id, tag, id)
}

// oneFixWalk builds the standard one-PR walk (a single :bug:~ commit → patch
// verdict v0.1.1) and chdirs into the repository — the smallest release-worthy
// input, shared by the upsert tests so each can focus on the draft surface.
func oneFixWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Fix a crash", 8)
	t.Chdir(dir)
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	}
}

// dryServer is releaseServer with an empty collection plus the assertion that
// NO write ever lands — the default surface for --dry-run tests.
func dryServer(t *testing.T, walk map[string]string) *httptest.Server {
	t.Helper()
	writes := &[]apiWrite{}
	srv := releaseServer(t, walk, `[]`, writes)
	t.Cleanup(func() {
		if len(*writes) != 0 {
			t.Errorf("a dry run wrote to the API: %+v", *writes)
		}
	})
	return srv
}

// noneWalk builds an all-none walk (one :memo:= commit) and chdirs into it.
func noneWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Document the fold", 7)
	t.Chdir(dir)
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):   `[` + apiCommit("a1", "akira-toriyama", ":memo:= document the fold") + `]`,
	}
}

// TestReleaseComposesTagAndBody is the compose contract: ONE walk feeds both
// the version step and the notes body, so the tag and the body can never be
// computed from different ranges (calling bump and notes separately walks
// twice, and a merge landing between the walks would split them). With no
// --since-tag the walk defaults to auto — release has exactly one input
// source, so demanding a bare --since-tag would be ceremony.
func TestReleaseComposesTagAndBody(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "v0.2.0\n\n") {
		t.Fatalf("release stdout does not open with the tag line:\n%s", stdout)
	}
	body := strings.TrimPrefix(stdout, "v0.2.0\n\n")
	for _, want := range []string{"## Features", "add a menu", "## Fixes", "fix a crash"} {
		if !strings.Contains(body, want) {
			t.Errorf("release body is missing %q:\n%s", want, body)
		}
	}
}

// TestReleaseUpsertCreatesTheDraft is the ratified bare surface (t-skd3 Q2):
// glyph release upserts the rolling DRAFT — with no draft present, one POST
// carrying the intended tag, the notes body, draft:true, and the checkout's
// HEAD sha as target_commitish (Publish must tag the commit the verdict was
// computed at). The payload is the draft's URL; no tag is created here.
func TestReleaseUpsertCreatesTheDraft(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)
	head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")

	code, stdout, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "POST" {
		t.Fatalf("writes = %+v, want exactly one POST", writes)
	}
	body := writes[0].body
	if body["tag_name"] != "v0.1.1" || body["name"] != "v0.1.1" || body["draft"] != true {
		t.Errorf("POST body = %+v, want tag_name/name v0.1.1 and draft true", body)
	}
	if body["target_commitish"] != head {
		t.Errorf("target_commitish = %v, want the checkout HEAD %s", body["target_commitish"], head)
	}
	if notes, _ := body["body"].(string); !strings.Contains(notes, "fix a crash") {
		t.Errorf("draft body is missing the notes entry:\n%v", body["body"])
	}
	if stdout != "https://github.example/releases/900\n" {
		t.Errorf("stdout = %q, want the draft URL", stdout)
	}
}

// TestReleaseUpsertUpdatesTheExistingDraft: a glyph-managed draft already
// carrying the intended tag is grown in place — one PATCH by release id,
// never a POST (a second draft) and never a DELETE.
func TestReleaseUpsertUpdatesTheExistingDraft(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, "v0.1.1")+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "PATCH" || writes[0].path != releasesPath+"/11" {
		t.Fatalf("writes = %+v, want exactly one PATCH of release 11", writes)
	}
	if writes[0].body["tag_name"] != "v0.1.1" || writes[0].body["draft"] != true {
		t.Errorf("PATCH body = %+v, want tag_name v0.1.1 and draft true", writes[0].body)
	}
	if stdout != "https://github.example/releases/11\n" {
		t.Errorf("stdout = %q, want the draft URL", stdout)
	}
}

// wantMarker is the hand marker's exact bytes, duplicated here ON PURPOSE:
// the marker is a published contract (existing drafts carry it), so a change
// to the const must fail these tests rather than be silently followed.
const wantMarker = "<!-- glyph: notes written ABOVE this line survive every push; glyph rewrites everything BELOW it -->"

// draftJSONBody is draftJSON with the release's current body — the update
// path's input for the hand-region tests.
func draftJSONBody(id int, tag, body string) string {
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"draft":true,"html_url":"https://github.example/releases/%d","body":%s}`, id, tag, id, b)
}

// TestReleaseUpsertPreservesTheHandRegion pins the t-qgps contract: the
// rolling draft is the one place release prose can be written, and the upsert
// must carry everything ABOVE the hand marker across a rewrite byte-for-byte
// (CRLF included — a web edit stores CRLF), while everything below is
// regenerated. The mutation ledger names this test.
func TestReleaseUpsertPreservesTheHandRegion(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	existing := "the exit codes changed, fix your gates\r\n\r\n" + wantMarker + "\n\n## Fixes\n\n- stale machine text\n"
	srv := releaseServer(t, walk, `[`+draftJSONBody(11, "v0.1.1", existing)+`]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "PATCH" {
		t.Fatalf("writes = %+v, want exactly one PATCH", writes)
	}
	body, _ := writes[0].body["body"].(string)
	wantPrefix := "the exit codes changed, fix your gates\r\n\r\n" + wantMarker + "\n\n"
	if !strings.HasPrefix(body, wantPrefix) {
		t.Errorf("the hand region did not survive the upsert:\n got %q", body)
	}
	if strings.Contains(body, "stale machine text") {
		t.Errorf("the machine region was preserved instead of regenerated: %q", body)
	}
	if !strings.Contains(body, "fix a crash") {
		t.Errorf("the fresh machine region is missing: %q", body)
	}
}

// TestReleaseUpsertMarkerlessBodyGetsNoHandRegion: a pre-marker draft (or one
// whose human deleted the marker line) contributes nothing — glyph cannot
// tell that body's hand-written lines from its own stale output, and guessing
// would resurrect old machine text as if a human had written it.
func TestReleaseUpsertMarkerlessBodyGetsNoHandRegion(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[`+draftJSONBody(11, "v0.1.1", "## Fixes\n\n- old body with no marker\n")+`]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	body, _ := writes[0].body["body"].(string)
	if !strings.HasPrefix(body, wantMarker+"\n\n") {
		t.Errorf("a rewritten body must open with the hand marker: %q", body)
	}
	if strings.Contains(body, "old body with no marker") {
		t.Errorf("a marker-less body must not be preserved: %q", body)
	}
}

// TestReleaseUpsertCreateWritesTheMarker: the marker is the contract's only
// user-visible statement, so it must be present from the draft's FIRST byte —
// a human should never meet a draft that does not say where their notes live.
func TestReleaseUpsertCreateWritesTheMarker(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "POST" {
		t.Fatalf("writes = %+v, want exactly one POST", writes)
	}
	body, _ := writes[0].body["body"].(string)
	if !strings.HasPrefix(body, wantMarker+"\n\n") {
		t.Errorf("a created draft must open with the hand marker: %q", body)
	}
}

// TestReleaseUpsertMovesTheDraftTag: when the next version changed since the
// draft was cut (another merge landed), the existing draft's intended tag is
// UPDATED — ratified: never a second draft.
func TestReleaseUpsertMovesTheDraftTag(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, "v0.1.5")+`]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "PATCH" || writes[0].path != releasesPath+"/11" {
		t.Fatalf("writes = %+v, want exactly one PATCH of release 11 (no second draft)", writes)
	}
	if writes[0].body["tag_name"] != "v0.1.1" {
		t.Errorf("PATCH tag_name = %v, want the draft retagged to v0.1.1", writes[0].body["tag_name"])
	}
}

// TestReleaseUpsertDeletesDuplicateDrafts: with several glyph-managed drafts
// the one already carrying the intended tag is kept (even when it is not
// listed first) and every other one is deleted BY ID, so the upsert converges
// on exactly one draft.
func TestReleaseUpsertDeletesDuplicateDrafts(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk,
		`[`+draftJSON(12, "v0.1.5")+`,`+draftJSON(11, "v0.1.1")+`]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	var patched, deleted []string
	for _, w := range writes {
		switch w.method {
		case "PATCH":
			patched = append(patched, w.path)
		case "DELETE":
			deleted = append(deleted, w.path)
		default:
			t.Errorf("unexpected write %+v", w)
		}
	}
	if len(patched) != 1 || patched[0] != releasesPath+"/11" {
		t.Errorf("patched %v, want exactly release 11 (the tag match wins)", patched)
	}
	if len(deleted) != 1 || deleted[0] != releasesPath+"/12" {
		t.Errorf("deleted %v, want exactly release 12", deleted)
	}
}

// TestReleaseUpsertNoneConvergesDrafts is ratified Q3: on a none verdict the
// draft state must converge to the verdict — no release should exist, so
// residual glyph-managed drafts are deleted (by id) and the run still exits 1
// (soft no-release), uniform with bump and notes.
func TestReleaseUpsertNoneConvergesDrafts(t *testing.T) {
	var writes []apiWrite
	walk := noneWalk(t)
	srv := releaseServer(t, walk,
		`[`+draftJSON(11, "v0.1.1")+`,`+draftJSON(12, "v0.1.2")+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release")
	if code != 1 {
		t.Fatalf("none upsert exited %d, want 1 (soft no-release)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("none upsert wrote a payload:\n%s", stdout)
	}
	var deleted []string
	for _, w := range writes {
		if w.method != "DELETE" {
			t.Errorf("unexpected write %+v (none may only delete)", w)
			continue
		}
		deleted = append(deleted, w.path)
	}
	if len(deleted) != 2 {
		t.Errorf("deleted %v, want both residual drafts (11 and 12)", deleted)
	}
}

// TestReleaseUpsertNeverTouchesForeignReleases: published releases and
// non-semver drafts (a human's hand-made "nightly") are not glyph's to manage
// — a none verdict with only those present converges by touching NOTHING.
func TestReleaseUpsertNeverTouchesForeignReleases(t *testing.T) {
	var writes []apiWrite
	walk := noneWalk(t)
	// "0.9.9" is a parseable version but not the house tag shape (no v) —
	// glyph never created it, so glyph never deletes it.
	srv := releaseServer(t, walk,
		`[`+publishedJSON(3, "v0.1.0")+`,`+draftJSON(21, "nightly")+`,`+draftJSON(22, "0.9.9")+`]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 1 {
		t.Fatalf("none upsert exited %d, want 1\nstderr: %s", code, stderr)
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %+v, want none (published releases and foreign drafts are untouchable)", writes)
	}
}

// TestReleaseUpsertNeverAdoptsUnparseableVDrafts closes the gap beside
// TestReleaseUpsertNeverTouchesForeignReleases: that test's foreign drafts
// ("nightly", "0.9.9") both fall to glyphDrafts' v-prefix arm, so its second
// arm — bump.ParseVersion rejecting a v-prefixed tag — was reachable by no
// test in the suite (measured: the `continue` is uncovered across every
// package, and deleting it stays green everywhere). A draft a human named
// vNext / v1.2 / v2026.08 wears the v but is not the house tag shape, so it
// is not glyph's to touch: a none verdict must not DELETE it, a release
// verdict must POST a fresh draft rather than PATCH it into glyph's, and the
// dry run — which reads the same listing — must preview exactly that.
//
// bite-exempt: ratifies the behaviour the tree already has, so it cannot fail
// against pre-PR source; the mutation ledger row is its defender instead.
func TestReleaseUpsertNeverAdoptsUnparseableVDrafts(t *testing.T) {
	foreign := `[` + draftJSON(31, "vNext") + `,` + draftJSON(32, "v1.2") + `,` + draftJSON(33, "v2026.08") + `]`

	t.Run("none-verdict-deletes-nothing", func(t *testing.T) {
		var writes []apiWrite
		srv := releaseServer(t, noneWalk(t), foreign, &writes)
		usePR(t, srv)

		code, _, stderr := runGlyph(t, "release")
		if code != 1 {
			t.Fatalf("none upsert exited %d, want 1\nstderr: %s", code, stderr)
		}
		if len(writes) != 0 {
			t.Fatalf("writes = %+v, want none — a v-prefixed tag ParseVersion rejects is a "+
				"human's draft, and a none verdict discarding it destroys work glyph never made", writes)
		}
	})

	t.Run("release-verdict-creates-fresh-never-patches", func(t *testing.T) {
		var writes []apiWrite
		srv := releaseServer(t, oneFixWalk(t), foreign, &writes)
		usePR(t, srv)

		code, _, stderr := runGlyph(t, "release")
		if code != 0 {
			t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
		}
		if len(writes) != 1 || writes[0].method != "POST" || writes[0].path != releasesPath {
			t.Fatalf("writes = %+v, want exactly one POST %s — adopting the foreign draft would "+
				"publish glyph's notes under a human's tag name and delete the rest as strays",
				writes, releasesPath)
		}
	})

	t.Run("dry-run-previews-the-same-restraint", func(t *testing.T) {
		var writes []apiWrite
		srv := releaseServer(t, oneFixWalk(t), foreign, &writes)
		usePR(t, srv)

		code, _, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 {
			t.Fatalf("dry run exited %d, want 0\nstderr: %s", code, stderr)
		}
		if len(writes) != 0 {
			t.Fatalf("a dry run wrote to the API: %+v", writes)
		}
		if !strings.Contains(stderr, "would create the rolling draft v0.1.1") {
			t.Errorf("the dry run does not preview a CREATE — it would adopt the foreign draft:\n%s", stderr)
		}
		if !strings.Contains(stderr, "(0 stale draft(s) to delete)") {
			t.Errorf("the dry run counts a human's drafts as stale:\n%s", stderr)
		}
	})
}

// TestReleaseUpsertJSON: the machine verdict gains the write outcome — the
// action taken and the draft's URL — on top of the audit trail the dry run
// already carries, so release.yml@v2 reads one object for the whole step.
func TestReleaseUpsertJSON(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 0 {
		t.Fatalf("release --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("release --json stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if v.Action != "create" || v.URL != "https://github.example/releases/900" {
		t.Errorf("verdict action/url = %q/%q, want create + the draft URL", v.Action, v.URL)
	}
	if v.Tag != "v0.1.1" || len(v.Commits) != 1 {
		t.Errorf("verdict = %+v, want the v0.1.1 audit trail intact", v)
	}
}

// TestReleaseTargetOverride: --target pins the draft's target_commitish
// explicitly, outranking the checkout HEAD — the escape hatch when the
// release job runs on a checkout that is not the commit to tag.
func TestReleaseTargetOverride(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release", "--target", "cafe1234")
	if code != 0 {
		t.Fatalf("release --target exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].body["target_commitish"] != "cafe1234" {
		t.Fatalf("writes = %+v, want one POST targeting cafe1234", writes)
	}
}

// TestReleaseRefusesToRegressBelowPublished is the deadlock guard (immutable
// releases): a next version that is not STRICTLY greater than the latest
// published release can never be published — its tag is taken (or burned, if
// the release was deleted), so equality refuses exactly like regression. The
// floor is the HIGHEST published version, wherever it sits in the listing.
// Fail loud (4), on the dry run too, naming both versions; never create the
// unpublishable draft.
func TestReleaseRefusesToRegressBelowPublished(t *testing.T) {
	for name, releases := range map[string]struct {
		json  string
		floor string
	}{
		// A lower published release is listed FIRST both times: a floor that
		// took the first parseable entry instead of the maximum would wave
		// these through.
		"below": {`[` + publishedJSON(2, "v0.0.9") + `,` + publishedJSON(3, "v0.5.0") + `]`, "v0.5.0"},
		"equal": {`[` + publishedJSON(2, "v0.0.9") + `,` + publishedJSON(3, "v0.1.1") + `]`, "v0.1.1"},
	} {
		for _, mode := range [][]string{{"release"}, {"release", "--dry-run"}} {
			t.Run(name+" "+strings.Join(mode, " "), func(t *testing.T) {
				var writes []apiWrite
				walk := oneFixWalk(t)
				srv := releaseServer(t, walk, releases.json, &writes)
				usePR(t, srv)

				code, stdout, stderr := runGlyph(t, mode...)
				if code != 4 {
					t.Fatalf("%v against published %s exited %d, want 4\nstderr: %s", mode, releases.floor, code, stderr)
				}
				if stdout != "" {
					t.Errorf("a refused release wrote a payload:\n%s", stdout)
				}
				if !strings.Contains(stderr, "v0.1.1") || !strings.Contains(stderr, releases.floor) {
					t.Errorf("the refusal must name the computed and the published versions:\n%s", stderr)
				}
				if len(writes) != 0 {
					t.Errorf("writes = %+v, want none", writes)
				}
			})
		}
	}
}

// TestReleaseDryRunAction is ratified Q4: the dry run computes EVERYTHING —
// verdict plus the draft-convergence decision — and only skips the writes, so
// the machine verdict carries the action the real run would take.
func TestReleaseDryRunAction(t *testing.T) {
	for name, tc := range map[string]struct {
		walk     func(*testing.T) map[string]string
		releases string
		want     string
		wantCode int
	}{
		"create": {oneFixWalk, `[]`, "create", 0},
		"update": {oneFixWalk, `[` + draftJSON(11, "v0.1.5") + `]`, "update", 0},
		"delete": {noneWalk, `[` + draftJSON(11, "v0.1.1") + `]`, "delete", 1},
		"none":   {noneWalk, `[]`, "none", 1},
	} {
		t.Run(name, func(t *testing.T) {
			var writes []apiWrite
			walk := tc.walk(t)
			srv := releaseServer(t, walk, tc.releases, &writes)
			usePR(t, srv)

			code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json")
			if code != tc.wantCode {
				t.Fatalf("exited %d, want %d\nstderr: %s", code, tc.wantCode, stderr)
			}
			var v releaseVerdict
			if err := json.Unmarshal([]byte(stdout), &v); err != nil {
				t.Fatalf("not one JSON object: %v\n%s", err, stdout)
			}
			if v.Action != tc.want {
				t.Errorf("action = %q, want %q", v.Action, tc.want)
			}
			if v.URL != "" {
				t.Errorf("a dry run has no write to point at, got url %q", v.URL)
			}
			if len(writes) != 0 {
				t.Errorf("a dry run wrote to the API: %+v", writes)
			}
		})
	}
}

// TestReleaseDryRunResolvesTheTarget: the target is part of "EVERYTHING" in
// Q4 — the dry run resolves it (the checkout's HEAD when the flag is absent)
// and reports it in the verdict, instead of silently ignoring the one flag
// that names which commit the eventual tag points at. Measured before the
// fix: dry-run output was byte-identical with and without --target, on
// stdout and stderr alike, so a typo'd sha surfaced only on the real run.
func TestReleaseDryRunResolvesTheTarget(t *testing.T) {
	dryRunVerdict := func(t *testing.T, args ...string) releaseVerdict {
		t.Helper()
		var writes []apiWrite
		walk := oneFixWalk(t)
		srv := releaseServer(t, walk, `[]`, &writes)
		usePR(t, srv)

		code, stdout, stderr := runGlyph(t, append([]string{"release", "--dry-run", "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("exited %d, want 0\nstderr: %s", code, stderr)
		}
		var v releaseVerdict
		if err := json.Unmarshal([]byte(stdout), &v); err != nil {
			t.Fatalf("not one JSON object: %v\n%s", err, stdout)
		}
		if len(writes) != 0 {
			t.Fatalf("a dry run wrote to the API: %+v", writes)
		}
		return v
	}

	t.Run("an explicit --target reaches the verdict", func(t *testing.T) {
		if v := dryRunVerdict(t, "--target", "cafe1234"); v.Target != "cafe1234" {
			t.Errorf("target = %q, want the cafe1234 the flag named", v.Target)
		}
	})
	t.Run("no flag resolves the checkout's HEAD", func(t *testing.T) {
		v := dryRunVerdict(t)
		if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(v.Target) {
			t.Errorf("target = %q, want the checkout's full HEAD sha", v.Target)
		}
	})
}

// TestReleaseFooterFile is ratified Q11: --footer-file appends the file's
// content verbatim after the notes, separated by one `---` line — composed by
// glyph so the dry run previews the EXACT body the draft will carry, and the
// caller never string-concatenates markdown in shell.
func TestReleaseFooterFile(t *testing.T) {
	footer := filepath.Join(t.TempDir(), "install.md")
	if err := os.WriteFile(footer, []byte("## Install\n\n`brew install x`\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	walk := oneFixWalk(t)
	srv := dryServer(t, walk)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", footer)
	if code != 0 {
		t.Fatalf("release --footer-file exited %d, want 0\nstderr: %s", code, stderr)
	}
	i := strings.Index(stdout, "\n---\n")
	if i < 0 {
		t.Fatalf("the body is missing the one-line --- separator:\n%s", stdout)
	}
	if !strings.Contains(stdout[i:], "## Install") || !strings.Contains(stdout[i:], "`brew install x`") {
		t.Errorf("the footer is not appended verbatim after the separator:\n%s", stdout)
	}
	if !strings.Contains(stdout[:i], "fix a crash") {
		t.Errorf("the notes must precede the separator:\n%s", stdout)
	}
}

// TestReleaseFooterFileMissingIsUsage: the path is the caller's input; a file
// that cannot be read is usage (2), caught before any request goes out.
func TestReleaseFooterFileMissingIsUsage(t *testing.T) {
	code, _, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", "/nonexistent/install.md")
	if code != 2 {
		t.Fatalf("a missing --footer-file exited %d, want 2 (usage)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "footer-file") || !strings.Contains(stderr, "/nonexistent/install.md") {
		t.Errorf("the error should name the flag and the path it could not read:\n%s", stderr)
	}
}

// releaseVerdict decodes a --json release verdict; the struct mirrors
// releaseResult field-for-field so a silently renamed key fails the decode
// assertions rather than zero-filling.
type releaseVerdict struct {
	Current string `json:"current"`
	Level   string `json:"level"`
	Tag     string `json:"tag"`
	Target  string `json:"target"`
	Body    string `json:"body"`
	Action  string `json:"action"`
	URL     string `json:"url"`
	Commits []struct {
		SHA      string `json:"sha"`
		Sigil    string `json:"sigil"`
		Level    string `json:"level"`
		Breaking bool   `json:"breaking"`
		Subject  string `json:"subject"`
	} `json:"commits"`
	Pulls []struct {
		Number  int `json:"number"`
		Commits int `json:"commits"`
	} `json:"pulls"`
	Reason string `json:"reason"`
}

// TestReleaseJSONReportsPullExpansion: the verdict names every merged pull the
// walk resolved and how many participating commits each contributed — the
// provenance that makes a verdict auditable afterwards, without
// re-implementing the walk's exclusion rules somewhere else. A direct push
// resolves to no pull and must
// not appear in the list.
func TestReleaseJSONReportsPullExpansion(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	testCommit(t, dir, "akira-toriyama", ":memo:= note the direct push")
	direct := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1):   `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2):   `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		commitPullsPath(direct): `[]`,
		pullCommitsPath(7): `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `,` +
			apiCommit("a2", "akira-toriyama", ":white_check_mark:= test the menu") + `]`,
		pullCommitsPath(8): `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("release --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("release --json stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if len(v.Pulls) != 2 ||
		v.Pulls[0].Number != 7 || v.Pulls[0].Commits != 2 ||
		v.Pulls[1].Number != 8 || v.Pulls[1].Commits != 1 {
		t.Errorf("verdict pulls = %+v, want [{7 2} {8 1}] in walk order", v.Pulls)
	}
}

// TestReleaseNoReleaseJSONPullsNormalized: a walk that resolved no pull still
// emits pulls as [] — the same nil-slice normalization commits gets — so
// .pulls is indexable on the none verdict too, with no null-check.
func TestReleaseNoReleaseJSONPullsNormalized(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo:= note the direct push")
	direct := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := dryServer(t, map[string]string{
		commitPullsPath(direct): `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json")
	if code != 1 {
		t.Fatalf("all-none release --json exited %d, want 1\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"pulls":[]`) {
		t.Errorf("no-release verdict does not normalize pulls to []:\n%s", stdout)
	}
}

// TestReleaseJSON: the machine verdict carries everything the rolling-draft
// step will need in one object — the tag to draft and the body to attach —
// plus the same audit trail bump --json emits.
func TestReleaseJSON(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui)^ add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("release --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("release --json stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if v.Current != "v0.1.0" || v.Level != "minor" || v.Tag != "v0.2.0" {
		t.Errorf("verdict = current %q level %q tag %q, want v0.1.0/minor/v0.2.0", v.Current, v.Level, v.Tag)
	}
	if !strings.Contains(v.Body, "add a menu") {
		t.Errorf("verdict body is missing the entry:\n%s", v.Body)
	}
	if len(v.Commits) != 1 || v.Commits[0].SHA != "a1" || v.Commits[0].Sigil != "^" {
		t.Errorf("verdict commits = %+v, want the one :sparkles:^ a1", v.Commits)
	}
	if v.Reason == "" {
		t.Error("verdict reason is empty")
	}
}

// TestReleaseNoRelease: an all-none walk is the soft no-release outcome — no
// payload, exit 1, the reason on the diagnostic stream — exactly the contract
// bump and notes keep, so a release job branches on the code alone.
func TestReleaseNoRelease(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Document the fold", 7)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":memo:= document the fold") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run")
	if code != 1 {
		t.Fatalf("all-none release exited %d, want 1\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("all-none release wrote a payload:\n%s", stdout)
	}
	if !strings.Contains(stderr, "no release") {
		t.Errorf("stderr does not name the no-release reason:\n%s", stderr)
	}
}

// TestReleaseNoReleaseJSON: with --json the verdict still prints (current,
// level none, commits, reason — no tag, no body) and the exit stays 1, so a
// machine caller reads one object and one code, never an error envelope.
func TestReleaseNoReleaseJSON(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Document the fold", 7)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":memo:= document the fold") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json")
	if code != 1 {
		t.Fatalf("all-none release --json exited %d, want 1\nstderr: %s", code, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("release --json stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if v.Level != "none" || v.Tag != "" || v.Body != "" {
		t.Errorf("verdict = level %q tag %q body %q, want none and no tag/body", v.Level, v.Tag, v.Body)
	}
	if strings.Contains(stderr, `"error"`) {
		t.Errorf("no-release --json must not add an error envelope over the verdict:\n%s", stderr)
	}
}

// TestReleaseExplicitSinceTag: naming a tag names the release being redone —
// the walk base and the step base are the same tag by construction, so the
// verdict is reproducible whatever tags were cut since.
func TestReleaseExplicitSinceTag(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Fix a crash", 8)
	testGit(t, dir, "akira-toriyama", "tag", "v0.5.0") // a later tag that must NOT become the base
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("release --since-tag=v0.1.0 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "v0.1.1\n\n") {
		t.Fatalf("release did not step from the NAMED tag (want v0.1.1):\n%s", stdout)
	}
}

// TestReleaseCurrentOverride: --current outranks the walked tag, mirroring
// bump — a redo can restate the base without moving tags.
func TestReleaseCurrentOverride(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Fix a crash", 8)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--current", "v2.3.4")
	if code != 0 {
		t.Fatalf("release --current exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "v2.3.5\n\n") {
		t.Fatalf("release did not step from --current (want v2.3.5):\n%s", stdout)
	}
}

// TestReleaseSpaceFormSinceTag: `release --since-tag v0.1.0` is the space form
// of the optional value — pflag reads a bare --since-tag plus a stray
// positional, and walking the WRONG range silently is the worst outcome, so
// the shared Args guard turns it into the same usage error bump and notes give.
func TestReleaseSpaceFormSinceTag(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "release", "--since-tag", "v0.1.0")
	if code != 2 {
		t.Fatalf("space-form --since-tag exited %d, want 2\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("usage error wrote a payload:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--since-tag=v0.1.0") {
		t.Errorf("usage error does not spell out the = form:\n%s", stderr)
	}
}

// TestReleaseHasNoRangeOrPRSource pins the grammar difference from bump and
// notes: release IS the walk, so the local --range and --pr sources do not
// exist on it and land as usage errors, not as silently ignored input.
func TestReleaseHasNoRangeOrPRSource(t *testing.T) {
	for _, flag := range []string{"--range", "--pr"} {
		code, _, _ := runGlyph(t, "release", flag, "x")
		if code != 2 {
			t.Errorf("release %s exited %d, want 2 (unknown flag)", flag, code)
		}
	}
}

// TestReleaseBreakingComposesConsistently: a breaking marker on a none-rung
// code moves the version AND hoists the entry into Breaking Changes — the two
// halves of the verdict must tell the same story because they come from the
// same classified set.
//
// The repository is at 0.x, so the step is a minor (v0.1.0 -> v0.2.0) while the
// classification stays major. That pairing is the point of the 0.x rule and is
// asserted here on purpose: the clamp shortens the step, it does not make the
// commit less breaking, and the release notes must keep saying so.
func TestReleaseBreakingComposesConsistently(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Drop the legacy config", 9)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(9, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(9):    `[` + apiCommit("c1", "akira-toriyama", ":coffin:! drop the legacy config") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 {
		t.Fatalf("breaking release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "v0.2.0\n\n") {
		t.Fatalf("breaking commit did not move the tag:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Breaking Changes") {
		t.Fatalf("breaking entry was not hoisted into Breaking Changes:\n%s", stdout)
	}
}

func TestHighestPublishedIgnoresUnparseableTags(t *testing.T) {
	releases := []github.Release{
		{ID: 1, TagName: "nightly", Draft: false},
		{ID: 2, TagName: "0.9.9", Draft: false},       // no v prefix — rejected before ParseVersion
		{ID: 6, TagName: "v2", Draft: false},          // v-prefixed, unparseable — reaches ParseVersion
		{ID: 7, TagName: "v1.0.0-rc.1", Draft: false}, // v-prefixed pre-release — likewise
		{ID: 3, TagName: "v0.5.0", Draft: false},
		{ID: 4, TagName: "v0.4.0", Draft: false},
		{ID: 5, TagName: "v9.9.9", Draft: true}, // a draft is no floor
		{ID: 8, TagName: "v8.8", Draft: true},   // ...and an unparseable one is doubly not
	}
	floor, ok := highestPublished(releases)
	if !ok || floor.String() != "v0.5.0" {
		t.Fatalf("floor = %v ok=%v, want v0.5.0 over the parseable published set", floor, ok)
	}
	if err := checkPublishedFloor(bump.Version{Minor: 5}, releases); err == nil {
		t.Fatalf("v0.5.0 equals the floor and must be refused (STRICTLY greater)")
	}
	if err := checkPublishedFloor(bump.Version{Minor: 5, Patch: 1}, releases); err != nil {
		t.Fatalf("v0.5.1 clears the floor, got %v", err)
	}

	// Named rather than sliced by index: this set must keep meaning "every
	// published release here is unparseable", through both rejection steps, and
	// a releases[:N] would silently start including a house tag the moment
	// somebody inserts one above the cut.
	foreignOnly := []github.Release{
		{ID: 1, TagName: "nightly", Draft: false},
		{ID: 2, TagName: "0.9.9", Draft: false},
		{ID: 6, TagName: "v2", Draft: false},
		{ID: 7, TagName: "v1.0.0-rc.1", Draft: false},
	}
	if _, ok := highestPublished(foreignOnly); ok {
		t.Fatalf("foreign-only published releases must yield no floor")
	}
	if err := checkPublishedFloor(bump.Version{Patch: 1}, foreignOnly); err != nil {
		t.Fatalf("with no floor any version passes, got %v", err)
	}
}

// TestReleaseUpsertUpdateJSONCarriesTheURL: the update path's --json verdict
// carries the PATCHed draft's URL (the create path is pinned by
// TestReleaseUpsertJSON) — release.yml surfaces this URL in the job summary.
func TestReleaseUpsertUpdateJSONCarriesTheURL(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, "v0.1.1")+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 0 {
		t.Fatalf("release --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("release --json stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if v.Action != "update" || v.URL != "https://github.example/releases/11" {
		t.Errorf("verdict action/url = %q/%q, want update + the PATCHed draft's URL", v.Action, v.URL)
	}
}

// strayServer serves the two-glyph-managed-draft shape the stray-convergence
// tests need — id 11 at the verdict's tag (kept, PATCHed) and id 12 at an older
// tag (stale, DELETEd) — and records every WRITE in ORDER.
//
// The order is the whole point of these tests and is why this does not reuse
// releaseServer: that helper records writes per-method, which cannot tell
// "PATCH then DELETE" from "DELETE then PATCH". fail decides what every DELETE
// answers, so one helper covers both the converging and the refusing case.
func strayServer(t *testing.T, seq *[]string, fail func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	walk := oneFixWalk(t)
	releases := `[` + draftJSON(11, "v0.1.1") + `,` + draftJSON(12, "v0.1.0") + `]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == releasesPath:
			fmt.Fprint(w, releases)
		case r.Method == http.MethodGet:
			body, ok := walk[r.URL.Path]
			if !ok {
				t.Errorf("unexpected GET %q", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		case r.Method == http.MethodDelete:
			*seq = append(*seq, "DELETE "+r.URL.Path)
			if fail != nil {
				fail(w)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPatch:
			*seq = append(*seq, "PATCH "+r.URL.Path)
			fmt.Fprint(w, draftJSON(11, "v0.1.1"))
		default:
			t.Errorf("unexpected %s %q", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestReleaseWritesTheDraftBeforeConvergingStrays pins the ORDER on the path
// where everything succeeds, and says nothing about failure — so it keeps
// biting even if the severity below is ever revisited.
//
// The notes must land first because the write is the run's only chance to land
// them: convergence is bookkeeping over a draft, and a delete placed ahead of
// the write can spend the whole run on it (measured: 21s of the shipped
// backoff, then exit 4, PATCH never sent).
func TestReleaseWritesTheDraftBeforeConvergingStrays(t *testing.T) {
	var seq []string
	usePR(t, strayServer(t, &seq, nil))

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := []string{
		"PATCH /repos/akira-toriyama/glyph/releases/11",
		"DELETE /repos/akira-toriyama/glyph/releases/12",
	}
	if !slices.Equal(seq, want) {
		t.Fatalf("write sequence = %v, want %v — the rolling draft is written BEFORE the stale "+
			"drafts are converged", seq, want)
	}
}

// TestReleaseStrayDeleteFailureKeepsTheNotes is t-rncn itself: a PERMANENT 5xx
// on the stale draft's DELETE.
//
// Measured on the pre-reorder tree with this exact scenario: exit 4, stdout
// empty, PATCH count 0 — the run burned the shipped 1s/4s/16s schedule on a
// delete and never wrote the release notes it had just computed. t-yq7m
// absorbed only the lost-answer 404; the harm it named is general.
//
// Now the verdict lands and the stray is a warning. That is a severity change
// on a documented path (4 -> 0) and it is bounded: no tag exists, nothing is
// published, and no new draft is created while one exists, so the stray set is
// self-limiting and the failure repeats identically next run.
func TestReleaseStrayDeleteFailureKeepsTheNotes(t *testing.T) {
	var seq []string
	usePR(t, strayServer(t, &seq, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"Service Unavailable"}`)
	}))

	code, stdout, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("a stale draft glyph could not delete exited %d, want 0 — the notes landed, and "+
			"the exit code of `release` answers whether the verdict did\nstderr: %s", code, stderr)
	}
	if stdout != "https://github.example/releases/11\n" {
		t.Errorf("stdout = %q, want the PATCHed draft's URL — the verdict must still be reported", stdout)
	}
	if len(seq) == 0 || seq[0] != "PATCH /repos/akira-toriyama/glyph/releases/11" {
		t.Fatalf("write sequence = %v; the PATCH must come first and must have happened", seq)
	}
	if n := strings.Count(strings.Join(seq, "\n"), "PATCH "); n != 1 {
		t.Errorf("the draft was written %d times, want exactly 1", n)
	}
	for _, want := range []string{"::warning::", "release id 12", "v0.1.0"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the warning does not carry %q — a stray nobody is told about is a stray a "+
				"human publishes:\n%s", want, stderr)
		}
	}
}

// TestReleaseNoneDeleteFailureStillFailsLoud is the boundary of the leniency
// above, and the guard against a later tidy-up that routes releaseNone through
// convergeStrays too.
//
// The two paths differ in exactly one thing: whether a write already succeeded.
// On a none verdict the delete IS the whole action, so absorbing its failure
// would mean the run did nothing and reported fine.
func TestReleaseNoneDeleteFailureStillFailsLoud(t *testing.T) {
	walk := noneWalk(t)
	releases := `[` + draftJSON(11, "v0.1.1") + `]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == releasesPath:
			fmt.Fprint(w, releases)
		case r.Method == http.MethodGet:
			body, ok := walk[r.URL.Path]
			if !ok {
				t.Errorf("unexpected GET %q", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		case r.Method == http.MethodDelete:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"message":"Service Unavailable"}`)
		default:
			t.Errorf("unexpected %s %q", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 4 {
		t.Fatalf("a residual delete that would not go exited %d, want 4 — on a none verdict the "+
			"delete is the entire action, so there is no landed write to be lenient "+
			"about\nstderr: %s", code, stderr)
	}
}

// TestReleaseNoneReportsAnUnwitnessedDeleteHonestly: the none verdict's delete
// is the one write with nothing downstream to contradict it. When its answer is
// lost and the retry is told 404, the release IS gone — glyph proceeds rather
// than aborting the run (t-yq7m) — but it never SAW its own request succeed,
// and a 404 is also how GitHub answers for a repository the credential can no
// longer see. So the notice reports what was observed instead of claiming a
// deletion, and says the claim is unconfirmed.
//
// The run takes ~1s: it exercises the SHIPPED retry schedule deliberately, so
// the wait is the real backoff a release job would spend rather than a test
// double's idea of one.
func TestReleaseNoneReportsAnUnwitnessedDeleteHonestly(t *testing.T) {
	walk := noneWalk(t)
	var deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == releasesPath:
			fmt.Fprint(w, `[`+draftJSON(11, "v0.1.1")+`]`)
		case r.Method == http.MethodGet:
			body, ok := walk[r.URL.Path]
			if !ok {
				t.Errorf("unexpected GET %q", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		case r.Method == http.MethodDelete:
			// The first delete reaches GitHub and is applied; its answer is lost
			// (the gateway says 503), so send replays it and is told the release
			// is already gone.
			deletes++
			if deletes == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"message":"Service Unavailable"}`)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		default:
			t.Errorf("unexpected %s %q", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 1 {
		t.Fatalf("a none verdict whose delete was answered 404 on the retry exited %d, want 1 "+
			"(soft no-release) — the draft is gone, which is what the run asked for\nstderr: %s", code, stderr)
	}
	if deletes != 2 {
		t.Fatalf("the server saw %d delete(s), want 2 (the lost answer and its replay)", deletes)
	}
	if !strings.Contains(stderr, "already gone") || !strings.Contains(stderr, "unconfirmed") {
		t.Errorf("the notice must report what was observed, not claim a deletion glyph never watched "+
			"happen — there is no later write on this path to catch a 404 that was really the "+
			"credential losing sight of the repository:\nstderr: %s", stderr)
	}
}

// unreadWalk builds the walk that comes back having read nothing: every walked
// commit is unknown to the queried repository (422), which is what a wrong
// --repo — or an inherited $GITHUB_REPOSITORY in a fork or reusable-workflow
// context — looks like from inside the walk. The commit is a squash subject, so
// the fallback cannot classify it either and the run reaches a none verdict.
func unreadWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Fix a crash", 8)
	t.Chdir(dir)
	return map[string]string{commitPullsPath(sha): apiUnknownSHA}
}

// lostPullWalk builds the other incomplete shape — DESIGN §4's ledger: a
// merge-merged pull whose branch commits resolve as COVERED by #7 while #7's own
// merge point is not on the API yet, so every one of them stands aside for a
// merge point that never turns up and the walk ends short by the whole pull.
// It returns the repository directory too, so a caller can land a further
// commit that DOES resolve — the shape where the verdict is not none and the
// run therefore never reaches the none path at all.
func lostPullWalk(t *testing.T, messages ...string) (map[string]string, string) {
	t.Helper()
	dir, _ := testRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, messages...)
	t.Chdir(dir)
	ref := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	routes := map[string]string{commitPullsPath(mp.Merge): apiUnknownSHA}
	for _, sha := range mp.Branch {
		routes[commitPullsPath(sha)] = ref
	}
	return routes, dir
}

// resolvablePatch lands one squash commit that DOES resolve, so a walk carrying
// it folds to a patch instead of none.
func resolvablePatch(t *testing.T, dir string, routes map[string]string) {
	t.Helper()
	sha := squashCommit(t, dir, "Fix a crash", 8)
	routes[commitPullsPath(sha)] = `[` + apiPullRef(8, "2026-07-21T00:00:00Z", sha) + `]`
	routes[pullCommitsPath(8)] = `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`
}

// TestReleaseIncompleteWalkFailsLoud: a verdict is a claim about the range only
// when the walk READ the range, and release is the command that acts on its
// verdict irreversibly — so a walk that came back short stops the release at
// exit 4 before the releases listing is even fetched (ratified t-pysg,
// replacing #66's warn-and-refuse-to-destroy).
//
// The shapes are the family walkFacts records, and each carried a measured
// defect when glyph still handed down a verdict on it: the none verdict deleted
// the rolling draft on the very reading it had just told the operator to re-run
// (t-441z); a `:boom:` pull lost to an unresolved merge point beside one
// ordinary `:bug:` took the CONSTRUCTIVE path — the shape the old refusal to
// destroy never reached — and retagged an existing v1.0.0 draft down to v0.1.1
// at exit 0, where a human publishing it burns the tag forever; a listing
// GitHub truncated at its 250 cap read "unreachable" as "absent"; and a shallow
// checkout graded itself on a truncated history (actions/checkout's default
// fetch-depth: 1, so the shape a release workflow produces by LOSING one line).
//
// The dry run takes the same gate: --dry-run skips the writes, not the reading,
// and a preview of a verdict no real run would hand down is not a preview.
func TestReleaseIncompleteWalkFailsLoud(t *testing.T) {
	cases := []struct {
		name string
		walk func(*testing.T) map[string]string
		want []string // what the failure must name, so the operator can act
	}{
		{"every commit unknown to the repository", unreadWalk, []string{"unknown to"}},
		{"a pull's merge point never resolved", func(t *testing.T) map[string]string {
			routes, _ := lostPullWalk(t, ":memo:= document the menu", ":memo:= document the fold")
			return routes
		}, []string{"#7"}},
		{"a lost boom pull beside a commit that classifies", func(t *testing.T) map[string]string {
			routes, dir := lostPullWalk(t, ":boom:(api)! drop the legacy endpoint", ":memo:= document the removal")
			// A resolvable commit of its own, so the fold would NOT be none —
			// the t-441z lowering shape.
			resolvablePatch(t, dir, routes)
			return routes
		}, []string{"#7"}},
		{"a truncated pull listing", truncatedPullWalk, []string{"truncated", "250", "#7"}},
		{"a shallow checkout", shallowCheckout, []string{"shallow"}},
	}
	for _, tc := range cases {
		for _, mode := range [][]string{{"release"}, {"release", "--dry-run"}} {
			t.Run(tc.name+" "+strings.Join(mode, " "), func(t *testing.T) {
				var writes []apiWrite
				srv := releaseServer(t, tc.walk(t), `[`+draftJSON(50, "v1.0.0")+`]`, &writes)
				usePR(t, srv)

				code, stdout, stderr := runGlyph(t, mode...)
				if code != 4 {
					t.Fatalf("exited %d, want 4 — a walk that could not read its range hands down no verdict\nstderr: %s", code, stderr)
				}
				if len(writes) != 0 {
					t.Errorf("writes = %+v, want none — the run must stop before it touches anything", writes)
				}
				if stdout != "" {
					t.Errorf("a failed walk wrote a payload:\n%s", stdout)
				}
				for _, want := range tc.want {
					if !strings.Contains(stderr, want) {
						t.Errorf("the failure must name what the walk could not read (%q):\nstderr: %s", want, stderr)
					}
				}
			})
		}
	}
}

// truncatedPullWalk builds the third incomplete shape: a squash-merged pull
// whose commit listing comes back at GitHub's hard cap. The API stops at
// PullCommitsCap however far the pagination follows, so the listing is not the
// pull — it is as much of the pull as can be obtained, and the rest is
// unreachable rather than absent.
//
// Every commit is a :memo:, so the fold — were the walk entitled to one —
// would be NONE: "nothing release-worthy" is exactly the conclusion a
// truncated listing is not entitled to reach. The 251st commit could be the
// :boom:.
func truncatedPullWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Document everything", 7)
	t.Chdir(dir)

	listing := make([]string, github.PullCommitsCap)
	for i := range listing {
		listing[i] = apiCommit(fmt.Sprintf("c%03d", i), "akira-toriyama", fmt.Sprintf(":memo:= document part %d", i))
	}
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-22T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):   `[` + strings.Join(listing, ",") + `]`,
	}
}

// shallowCheckout makes a REAL `git clone --depth 1` of a repository holding one
// squash-merged pull, chdirs into the clone, and returns the walk routes for it.
//
// A real clone rather than a stubbed answer, because the whole defect is about
// what git can and cannot see: in a depth-1 clone the parent commits are absent
// objects, so mainFootprint's `Have` says no for every listed sha and the walk
// falls back to expanding whole listings — the exact behaviour the footprint
// rule was built to end, reachable by deleting one line from a workflow. Faking
// IsShallow would test the plumbing and leave the premise unexamined.
func shallowCheckout(t *testing.T) map[string]string {
	t.Helper()
	origin, _ := testRepo(t)
	sha := squashCommit(t, origin, "Fix a crash", 8)

	clone := filepath.Join(t.TempDir(), "shallow")
	testGit(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", "1", "file://"+origin, clone)
	if got := testGit(t, clone, "akira-toriyama", "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("the fixture clone is not shallow (--is-shallow-repository = %q); the test would prove nothing", got)
	}
	t.Chdir(clone)
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-23T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	}
}
