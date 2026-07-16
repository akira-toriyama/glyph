package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	}))
	t.Cleanup(srv.Close)
	return srv
}

// draftJSON / publishedJSON render one release in the shape GET /releases
// returns.
func draftJSON(id int, tag string) string {
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"draft":true,"html_url":"https://github.example/releases/%d"}`, id, tag, id)
}

func publishedJSON(id int, tag string) string {
	return fmt.Sprintf(`{"id":%d,"tag_name":%q,"draft":false,"html_url":"https://github.example/releases/%d"}`, id, tag, id)
}

// oneFixWalk builds the standard one-PR walk (a single :bug: commit → patch
// verdict v0.1.1) and chdirs into the repository — the smallest release-worthy
// input, shared by the upsert tests so each can focus on the draft surface.
func oneFixWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Fix a crash", 8)
	t.Chdir(dir)
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
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

// noneWalk builds an all-none walk (one :memo: commit) and chdirs into it.
func noneWalk(t *testing.T) map[string]string {
	t.Helper()
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Document the fold", 7)
	t.Chdir(dir)
	return map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):   `[` + apiCommit("a1", "akira-toriyama", ":memo: document the fold") + `]`,
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
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
	Body    string `json:"body"`
	Action  string `json:"action"`
	URL     string `json:"url"`
	Commits []struct {
		SHA      string `json:"sha"`
		Code     string `json:"code"`
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
// walk resolved and how many participating commits each contributed. The
// Phase 6 shadow comparison branches on exactly this (ratified Q6): git-cliff
// reads only main's squash subjects, so a bump divergence is EXPECTED when
// some pull contributed 2+ commits (COMMIT_OR_PR_TITLE replaced its squash
// subject with the PR title) — and glyph-bug-suspect red otherwise. Doing the
// detection outside glyph would mean re-implementing the walk's exclusion
// rules in shell, which drifts. A direct push resolves to no pull and must
// not appear in the list.
func TestReleaseJSONReportsPullExpansion(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	testCommit(t, dir, "akira-toriyama", ":memo: note the direct push")
	direct := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1):   `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2):   `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		commitPullsPath(direct): `[]`,
		pullCommitsPath(7): `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui) add a menu") + `,` +
			apiCommit("a2", "akira-toriyama", ":white_check_mark: test the menu") + `]`,
		pullCommitsPath(8): `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
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
// emits pulls as [] — the same nil-slice normalization commits gets — so a
// shadow script indexes .pulls unconditionally, on the none verdict too.
func TestReleaseNoReleaseJSONPullsNormalized(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo: note the direct push")
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
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
	if len(v.Commits) != 1 || v.Commits[0].SHA != "a1" || v.Commits[0].Code != ":sparkles:" {
		t.Errorf("verdict commits = %+v, want the one :sparkles: a1", v.Commits)
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":memo: document the fold") + `]`,
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
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":memo: document the fold") + `]`,
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
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
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
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
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
// code majors the version AND hoists the entry into Breaking Changes — the two
// halves of the verdict must tell the same story because they come from the
// same classified set.
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
	if !strings.HasPrefix(stdout, "v1.0.0\n\n") {
		t.Fatalf("breaking commit did not major the tag:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Breaking Changes") {
		t.Fatalf("breaking entry was not hoisted into Breaking Changes:\n%s", stdout)
	}
}
