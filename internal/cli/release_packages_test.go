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

// This file pins `glyph release` for a packages repository (DESIGN §4.1,
// "Drafts, one per line"): one rolling draft per line, converged on that
// line's own prefix, written before any stray is touched.

// releaseVerdictLines decodes release --json in packages mode.
type releaseVerdictLines struct {
	Current  string `json:"current"`
	Level    string `json:"level"`
	Tag      string `json:"tag"`
	Target   string `json:"target"`
	Action   string `json:"action"`
	URL      string `json:"url"`
	Packages []struct {
		Path   string `json:"path"`
		Next   string `json:"next"`
		Tag    string `json:"tag"`
		Body   string `json:"body"`
		Action string `json:"action"`
		URL    string `json:"url"`
		Level  string `json:"level"`
	} `json:"packages"`
	Reason string `json:"reason"`
}

func decodeReleaseLines(t *testing.T, stdout string) releaseVerdictLines {
	t.Helper()
	var res releaseVerdictLines
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("release --json stdout is not JSON: %v\n%s", err, stdout)
	}
	return res
}

// TestReleasePackagesWritesOneDraftPerLine: the defining probe's release —
// two POSTs in config order, haiku/v0.2.0 and curry/v0.1.1, each a DRAFT
// carrying its own line's notes and the hand marker, no delete; stdout is
// one URL per draft, the JSON's scalars are empty and packages carries each
// line's tag, action and URL.
func TestReleasePackagesWritesOneDraftPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	var writes []apiWrite
	usePR(t, releaseServer(t, routes, `[]`, &writes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 || writes[0].method != "POST" || writes[1].method != "POST" {
		t.Fatalf("writes = %+v, want exactly two POSTs", writes)
	}
	if writes[0].body["tag_name"] != "haiku/v0.2.0" || writes[1].body["tag_name"] != "curry/v0.1.1" {
		t.Fatalf("drafts = %v / %v, want haiku/v0.2.0 then curry/v0.1.1", writes[0].body["tag_name"], writes[1].body["tag_name"])
	}
	for _, wr := range writes {
		if wr.body["draft"] != true {
			t.Fatalf("every write must be a DRAFT: %+v", wr.body)
		}
		if body, _ := wr.body["body"].(string); !strings.Contains(body, handMarker) {
			t.Fatalf("draft %v carries no hand marker:\n%s", wr.body["tag_name"], body)
		}
	}
	if h, _ := writes[0].body["body"].(string); !strings.Contains(h, "add a season") || strings.Contains(h, "swap an ingredient") {
		t.Fatalf("haiku's draft must carry haiku's notes alone:\n%s", h)
	}
	if strings.Count(stdout, "\n") != 2 {
		t.Fatalf("stdout = %q, want one URL per draft", stdout)
	}

	writes = nil
	code, stdout, stderr = runGlyph(t, "release", "--json")
	if code != 0 {
		t.Fatalf("release --json exited %d\nstderr: %s", code, stderr)
	}
	res := decodeReleaseLines(t, stdout)
	if res.Current != "" || res.Level != "" || res.Tag != "" || res.Action != "" || res.URL != "" {
		t.Fatalf("scalars must be empty under packages: %s", stdout)
	}
	if res.Target == "" {
		t.Fatalf("target is one checkout's HEAD and must be reported: %s", stdout)
	}
	if len(res.Packages) != 2 || res.Packages[0].Tag != "haiku/v0.2.0" || res.Packages[0].Action != "create" || res.Packages[0].URL == "" || res.Packages[1].Tag != "curry/v0.1.1" {
		t.Fatalf("packages = %+v", res.Packages)
	}
}

// TestReleasePackagesUpdatesEachLinesOwnDraft: each line converges its OWN
// draft — haiku's is updated by id with its hand region carried across,
// curry's is retagged in place from v0.1.0 to v0.1.1 — and neither line's
// draft is ever a stray of the other (no DELETE).
func TestReleasePackagesUpdatesEachLinesOwnDraft(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	var writes []apiWrite
	releases := `[` + draftJSONBody(41, "haiku/v0.2.0", "hand-written above\n"+handMarker+"\n\nold machine text") + `,` + draftJSON(42, "curry/v0.1.0") + `]`
	usePR(t, releaseServer(t, routes, releases, &writes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 || writes[0].method != "PATCH" || writes[1].method != "PATCH" {
		t.Fatalf("writes = %+v, want two PATCHes and no DELETE", writes)
	}
	if !strings.HasSuffix(writes[0].path, "/41") || !strings.HasSuffix(writes[1].path, "/42") {
		t.Fatalf("drafts must be updated by id: %s / %s", writes[0].path, writes[1].path)
	}
	if body, _ := writes[0].body["body"].(string); !strings.HasPrefix(body, "hand-written above\n"+handMarker) || strings.Contains(body, "old machine text") {
		t.Fatalf("haiku's hand region must survive and its machine region be rewritten:\n%s", body)
	}
	if writes[1].body["tag_name"] != "curry/v0.1.1" {
		t.Fatalf("curry's draft must be retagged in place to v0.1.1, got %v", writes[1].body["tag_name"])
	}
}

// TestReleasePackagesNoneLineConvergesOnlyItsOwnDraft: a line that folds to
// none deletes ITS residual draft after the moving line's upsert — write
// first, then strays — and a draft on a third, undeclared line is nobody's
// stray.
func TestReleasePackagesNoneLineConvergesOnlyItsOwnDraft(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	var writes []apiWrite
	releases := `[` + draftJSON(51, "curry/v0.1.1") + `,` + draftJSON(52, "fish/v1.0.0") + `]`
	usePR(t, releaseServer(t, map[string]string{commitPullsPath(sha): `[]`}, releases, &writes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 0 {
		t.Fatalf("release exited %d, want 0 (haiku moves)\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 || writes[0].method != "POST" || writes[1].method != "DELETE" || !strings.HasSuffix(writes[1].path, "/51") {
		t.Fatalf("writes = %+v, want haiku's POST then the DELETE of curry's residual draft 51, and fish's untouched", writes)
	}
	res := decodeReleaseLines(t, stdout)
	if res.Packages[1].Path != "curry" || res.Packages[1].Action != "delete" || res.Packages[1].Level != "none" {
		t.Fatalf("curry = %+v, want a none verdict whose action is delete", res.Packages[1])
	}
}

// TestReleasePackagesBareResidueIsDeletedWithNotice: with packages declared
// and no root package, a bare vX.Y.Z draft is the single line's residue —
// deleted, and the notice says the hand region goes with it. With a root
// package declared it is that package's draft and simply converges.
func TestReleasePackagesBareResidueIsDeletedWithNotice(t *testing.T) {
	t.Run("no root package: the residue goes", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		var writes []apiWrite
		usePR(t, releaseServer(t, routes, `[`+draftJSON(61, "v0.9.0")+`]`, &writes))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "release")
		if code != 0 {
			t.Fatalf("release exited %d\nstderr: %s", code, stderr)
		}
		if len(writes) != 3 || writes[2].method != "DELETE" || !strings.HasSuffix(writes[2].path, "/61") {
			t.Fatalf("writes = %+v, want two POSTs then the DELETE of the bare residue", writes)
		}
		if !strings.Contains(stderr, "single line's residue") || !strings.Contains(stderr, "hand region") {
			t.Fatalf("the notice must say what is deleted and that the hand region goes with it:\n%s", stderr)
		}
	})
	t.Run("a root package adopts it", func(t *testing.T) {
		dir, declared := packagesRepoWithRoot(t)
		sha := touch(t, dir, "akira-toriyama", ":bug:~ fix the workspace", "go.work")
		var writes []apiWrite
		usePR(t, releaseServer(t, map[string]string{commitPullsPath(sha): `[]`, commitPullsPath(declared): `[]`}, `[`+draftJSON(62, "v0.9.0")+`]`, &writes))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "release")
		if code != 0 {
			t.Fatalf("release exited %d\nstderr: %s", code, stderr)
		}
		if len(writes) != 1 || writes[0].method != "PATCH" || !strings.HasSuffix(writes[0].path, "/62") || writes[0].body["tag_name"] != "v0.1.1" {
			t.Fatalf("writes = %+v, want the root line to update draft 62 in place to v0.1.1", writes)
		}
	})
}

// packagesRepoWithRoot declares haiku, curry AND the root package (path
// "."), with a bare v0.1.0 on the declaring commit beside the two line tags;
// declared is that commit, which the union walk visits.
func packagesRepoWithRoot(t *testing.T) (dir, declared string) {
	t.Helper()
	dir, _ = packagesRepo(t)
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \".\"\nname = \"root\"\n")
	testGit(t, dir, "akira-toriyama", "add", "glyph.toml")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":wrench:(root)= declare the root line")
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestReleasePackagesAllNoneExitsOne: every line none, no draft to write —
// exit 1 with the per-line reasons, the residual drafts of every line
// deleted loudly (the deletes are the whole action), nothing created.
func TestReleasePackagesAllNoneExitsOne(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	var writes []apiWrite
	usePR(t, releaseServer(t, map[string]string{commitPullsPath(sha): `[]`}, `[`+draftJSON(71, "haiku/v0.2.0")+`]`, &writes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 1 {
		t.Fatalf("release exited %d, want 1\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "DELETE" {
		t.Fatalf("writes = %+v, want only the DELETE of haiku's residual draft", writes)
	}
	res := decodeReleaseLines(t, stdout)
	if !strings.Contains(res.Reason, "every line folds to none") || res.Packages[0].Action != "delete" || res.Packages[1].Action != "none" {
		t.Fatalf("verdict = %+v", res)
	}
}

// TestReleasePackagesSecondWriteFailureLeavesTheFirst pins the write order
// (§4's write-first, extended): every line's upsert lands before any stray
// goes, and a write that fails on the second line exits 4 with the first
// line's draft standing and NO stray deleted — nothing destroyed, the next
// run heals the rest (mutation row release-packages-strays-go-before-the-upserts).
func TestReleasePackagesSecondWriteFailureLeavesTheFirst(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	var writes []apiWrite
	inner := releaseHandler(t, routes, `[`+draftJSON(81, "v0.9.0")+`]`, &writes)
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			if posts >= 2 {
				// 422 is not retried, so the failure is immediate rather than
				// the end of the retry schedule.
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprint(w, `{"message":"boom"}`)
				return
			}
		}
		inner(w, r)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "release")
	if code != 4 {
		t.Fatalf("a failed second write exited %d, want 4\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "POST" || writes[0].body["tag_name"] != "haiku/v0.2.0" {
		t.Fatalf("writes = %+v, want haiku's POST alone — no DELETE before the upserts, no second draft", writes)
	}
	if !strings.Contains(stderr, "1 line(s) were written before this failure and stand") {
		t.Fatalf("the failure must say what stands:\n%s", stderr)
	}
}

// TestReleasePackagesATagConvergesOneLineAlone: --since-tag=<prefix>vX.Y.Z
// selects that line — its draft is written and the OTHER line's drafts are
// not this run's to touch (mutation row release-packages-selected-line-converges-the-others).
func TestReleasePackagesATagConvergesOneLineAlone(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	var writes []apiWrite
	usePR(t, releaseServer(t, routes, `[`+draftJSON(91, "curry/v0.5.0")+`]`, &writes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--since-tag=haiku/v0.1.0", "--json")
	if code != 0 {
		t.Fatalf("release exited %d\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "POST" || writes[0].body["tag_name"] != "haiku/v0.2.0" {
		t.Fatalf("writes = %+v, want haiku's POST alone; curry's draft 91 is not this run's", writes)
	}
	if res := decodeReleaseLines(t, stdout); len(res.Packages) != 1 || res.Packages[0].Path != "haiku" {
		t.Fatalf("a selected line is the only verdict: %s", stdout)
	}
}

// TestReleasePackagesDryRunGolden pins the composed dry-run output over two
// lines — each draft's tag line, blank line, marker, sections and the footer
// appended to EVERY draft — as bytes, the way the single line's golden does.
// Regenerate with `go test ./internal/cli -run Golden -update`, and read the
// diff as the spec change it is (the commit needs a Golden-change trailer).
func TestReleasePackagesDryRunGolden(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("testdata", "release_dry_run_packages.golden.md"))
	if err != nil {
		t.Fatal(err)
	}
	footer := filepath.Join(t.TempDir(), "install.md")
	if werr := os.WriteFile(footer, []byte("## Install\n\n`go get`\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, dryServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", footer)
	if code != 0 {
		t.Fatalf("release --dry-run exited %d, want 0\nstderr: %s", code, stderr)
	}
	if *update {
		if werr := os.WriteFile(golden, []byte(stdout), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	want, rerr := os.ReadFile(golden)
	if rerr != nil {
		t.Fatalf("reading the golden (run with -update once to create it): %v", rerr)
	}
	if stdout != string(want) {
		t.Errorf("dry-run output does not match the golden spec\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestReleasePackagesDraftOnNoneMaintainsAPlaceholderPerLine: with the flag
// on, a none line keeps <path>/Unreleased alive — created on the bare
// collection — while the moving line gets its real draft; exit 1 still
// answers only when every line is none.
func TestReleasePackagesDraftOnNoneMaintainsAPlaceholderPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	t.Chdir(dir)
	enableDraftOnNone(t)
	var writes []apiWrite
	usePR(t, releaseServer(t, map[string]string{commitPullsPath(sha): `[]`}, `[]`, &writes))

	code, _, stderr := runGlyph(t, "release")
	if code != 1 {
		t.Fatalf("release exited %d, want 1 (every line none, placeholders maintained)\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 || writes[0].body["tag_name"] != "haiku/Unreleased" || writes[1].body["tag_name"] != "curry/Unreleased" {
		t.Fatalf("writes = %+v, want one placeholder POST per line", writes)
	}
}
