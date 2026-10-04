package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		t.Fatalf("every line's draft points at target, so it must be reported: %s", stdout)
	}
	if len(res.Packages) != 2 || res.Packages[0].Tag != "haiku/v0.2.0" || res.Packages[0].Action != "create" || res.Packages[0].URL == "" || res.Packages[1].Tag != "curry/v0.1.1" {
		t.Fatalf("packages = %+v", res.Packages)
	}
}

// TestReleasePackagesRealRunNoticeNamesTheTarget is the packages arm of
// TestReleaseRealRunNoticeNamesTheTarget (t-wzr5): every line's draft points
// at the one target, and each line's notice names it, as the dry run's
// per-line notice always did.
func TestReleasePackagesRealRunNoticeNamesTheTarget(t *testing.T) {
	realRunStderr := func(t *testing.T, args ...string) string {
		t.Helper()
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		var writes []apiWrite
		usePR(t, releaseServer(t, routes, `[]`, &writes))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, append([]string{"release"}, args...)...)
		if code != 0 {
			t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
		}
		if len(writes) != 2 {
			t.Fatalf("writes = %+v, want one POST per line", writes)
		}
		return stderr
	}

	t.Run("an explicit --target is the sha every notice names", func(t *testing.T) {
		stderr := realRunStderr(t, "--target", "cafe1234")
		for _, tag := range []string{"haiku/v0.2.0", "curry/v0.1.1"} {
			if !strings.Contains(stderr, "draft release "+tag+" created at cafe1234 ") {
				t.Errorf("%s's notice must name the cafe1234 the flag named:\n%s", tag, stderr)
			}
		}
	})
	t.Run("no flag names the checkout's HEAD", func(t *testing.T) {
		stderr := realRunStderr(t)
		head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")
		for _, tag := range []string{"haiku/v0.2.0", "curry/v0.1.1"} {
			if !strings.Contains(stderr, "draft release "+tag+" created at "+head+" ") {
				t.Errorf("%s's notice must name the checkout's HEAD %s:\n%s", tag, head, stderr)
			}
		}
	})
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
// first, then deletes — and a draft on a third, undeclared line is nobody's
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
// deleted, and the notice says the hand region goes with it, from whichever
// pass deleted it (a stray beside the lines' drafts, or the whole action when
// no line writes one). With a root package declared it is that package's
// draft and simply converges.
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
		if !claimsResidueGone(deletionClaims(stderr, 61)) {
			t.Fatalf("the notice must say what is deleted and that the hand region goes with it:\n%s", stderr)
		}
	})
	t.Run("no line writes a draft: the residue goes, and says so", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		walk := linesQuiet(t, dir)
		var writes []apiWrite
		usePR(t, releaseServer(t, walk, `[`+draftJSON(61, "v0.9.0")+`]`, &writes))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "release")
		if code != 1 {
			t.Fatalf("release exited %d, want 1 (every line none)\nstderr: %s", code, stderr)
		}
		if len(writes) != 1 || writes[0].method != "DELETE" || !strings.HasSuffix(writes[0].path, "/61") {
			t.Fatalf("writes = %+v, want the DELETE of the bare residue alone", writes)
		}
		if !claimsResidueGone(deletionClaims(stderr, 61)) {
			t.Fatalf("the loud pass must speak the residue's notice once its DELETE went too:\n%s", stderr)
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

// deletionClaims returns the stderr lines that name the draft with this
// release id as a real run does — every such line but a dry run's. A dry run
// must print none; claimsResidueGone is the positive control that the real
// runs above print the residue's deletion claim among them, so the pattern the
// dry run is held to is proven to see the claim it excludes.
func deletionClaims(stderr string, id int) []string {
	var claims []string
	for l := range strings.SplitSeq(stderr, "\n") {
		if strings.Contains(l, fmt.Sprintf("(release id %d)", id)) && !strings.HasPrefix(l, "::notice::glyph: dry run: ") {
			claims = append(claims, l)
		}
	}
	return claims
}

func claimsResidueGone(claims []string) bool {
	return slices.ContainsFunc(claims, func(l string) bool {
		return strings.Contains(l, "is the single line's residue") && strings.Contains(l, "a hand region it carried is gone with it")
	})
}

// TestReleasePackagesBareResidueNoticeWaitsForTheDelete (t-xz1z): the residue
// notice is the one warning that exists so a human can move a hand region's
// prose before it is destroyed, and --dry-run is how they get to read it in
// time. It used to be printed at plan time, above the dry-run fork and the
// writes, so a dry run (which writes nothing) and a run that died at an
// upsert (the residue untouched) both said "it is deleted". Now the dry run
// says it would be, and the real run speaks only once the DELETE went.
func TestReleasePackagesBareResidueNoticeWaitsForTheDelete(t *testing.T) {
	residue := `[` + draftJSON(61, "v0.9.0") + `]`
	serve := func(t *testing.T, fail func(*http.Request) int) (seq *[]string) {
		t.Helper()
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		seq = &[]string{}
		usePR(t, failingReleaseServer(t, routes, residue, fail, seq))
		t.Chdir(dir)
		return seq
	}
	never := func(*http.Request) int { return 0 }

	t.Run("a dry run says it would be deleted", func(t *testing.T) {
		seq := serve(t, never)
		code, _, stderr := runGlyph(t, "release", "--dry-run")
		if code != 0 {
			t.Fatalf("release --dry-run exited %d, want 0\nstderr: %s", code, stderr)
		}
		if len(*seq) != 0 {
			t.Fatalf("a dry run wrote: %v", *seq)
		}
		if claims := deletionClaims(stderr, 61); len(claims) > 0 {
			t.Errorf("a dry run deletes nothing, so every line naming the residue must be a dry run's:\n%s", strings.Join(claims, "\n"))
		}
		for _, want := range []string{"dry run: the bare draft v0.9.0 (release id 61) is the single line's residue", "would be deleted", "hand region"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the dry run must name the residue before anything is written (%q):\n%s", want, stderr)
			}
		}
	})
	t.Run("a run that died at an upsert deleted nothing and says so", func(t *testing.T) {
		seq := serve(t, func(r *http.Request) int {
			if r.Method == http.MethodPost {
				return http.StatusUnprocessableEntity
			}
			return 0
		})
		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("a failed upsert exited %d, want 4\nstderr: %s", code, stderr)
		}
		if slices.ContainsFunc(*seq, func(w string) bool { return strings.HasPrefix(w, "DELETE ") }) {
			t.Fatalf("write sequence = %v, want no DELETE after a failed upsert", *seq)
		}
		if strings.Contains(stderr, "single line's residue") || strings.Contains(stderr, "gone with it") {
			t.Errorf("the residue is untouched, so no notice may speak of its deletion:\n%s", stderr)
		}
	})
	t.Run("a DELETE that would not go leaves the warning alone", func(t *testing.T) {
		serve(t, func(r *http.Request) int {
			if r.Method == http.MethodDelete {
				return http.StatusServiceUnavailable
			}
			return 0
		})
		code, _, stderr := runGlyph(t, "release")
		if code != 0 {
			t.Fatalf("release exited %d, want 0 (the residue is a stray beside the drafts that landed)\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "release id 61") {
			t.Errorf("the residue that would not go must be warned about:\n%s", stderr)
		}
		if strings.Contains(stderr, "single line's residue") || strings.Contains(stderr, "gone with it") {
			t.Errorf("the residue still stands, so no notice may speak of its deletion:\n%s", stderr)
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
// deleted loudly (the deletes are the whole action), nothing created, and no
// target: target is the sha a draft's eventual tag points at, and the single
// line's none verdict carries none either (t-xz1z; the first cut reported
// HEAD here, on the real run and the dry run alike).
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
	if res.Target != "" {
		t.Fatalf("no line has a draft to write, so there is no target to report: %s", stdout)
	}

	writes = nil
	code, stdout, stderr = runGlyph(t, "release", "--dry-run", "--json")
	if code != 1 || len(writes) != 0 {
		t.Fatalf("release --dry-run exited %d with writes %+v, want 1 and none\nstderr: %s", code, writes, stderr)
	}
	if res := decodeReleaseLines(t, stdout); res.Target != "" {
		t.Fatalf("a dry run that would write no draft has no target to report: %s", stdout)
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

// failingReleaseServer serves the releases surface, answers a request with
// fail's status when it returns one (Retry-After 0, so a retried status is
// retried at once) and records every write in ORDER — the upsert-first and
// delete-order properties the tests below lean on.
func failingReleaseServer(t *testing.T, walk map[string]string, releases string, fail func(*http.Request) int, seq *[]string) *httptest.Server {
	t.Helper()
	var writes []apiWrite
	inner := releaseHandler(t, walk, releases, &writes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			*seq = append(*seq, r.Method+" "+r.URL.Path)
		}
		if code := fail(r); code != 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(code)
			fmt.Fprint(w, `{"message":"boom"}`)
			return
		}
		inner(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deleteFailureServer answers every DELETE 503.
func deleteFailureServer(t *testing.T, walk map[string]string, releases string, seq *[]string) *httptest.Server {
	t.Helper()
	return failingReleaseServer(t, walk, releases, func(r *http.Request) int {
		if r.Method == http.MethodDelete {
			return http.StatusServiceUnavailable
		}
		return 0
	}, seq)
}

// haikuMoves lands a ^ under haiku/ alone (haiku v0.1.0 → v0.2.0, curry none);
// linesQuiet lands a shared-only = (every line none).
func haikuMoves(t *testing.T, dir string) map[string]string {
	t.Helper()
	sha := touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	return map[string]string{commitPullsPath(sha): `[]`}
}

func linesQuiet(t *testing.T, dir string) map[string]string {
	t.Helper()
	sha := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	return map[string]string{commitPullsPath(sha): `[]`}
}

// TestReleasePackagesNoneDeleteFailureStillFailsLoud is the packages twin of
// TestReleaseNoneDeleteFailureStillFailsLoud (t-xz1z): for a line that folds
// to none with draft_on_none off, deleting its residual draft is the line's
// whole action, so a delete that will not go fails the run (4) — whatever the
// line's siblings did. Measured before the split: the same line, the same none
// verdict and the same failing DELETE exited 4 when every line was none and 0
// when a sibling line had written a draft, because the residual then rode
// convergeStrays' leniency; the verdict reported curry's action as delete with
// the draft still standing. The bare residue follows the run: with no draft
// written, its delete is the whole action too.
func TestReleasePackagesNoneDeleteFailureStillFailsLoud(t *testing.T) {
	for name, tc := range map[string]struct {
		walk     func(*testing.T, string) map[string]string
		releases string
		upserts  int
	}{
		"a none line beside a sibling's draft":         {haikuMoves, `[` + draftJSON(51, "curry/v0.1.1") + `]`, 1},
		"a none line when every line is none":          {linesQuiet, `[` + draftJSON(51, "curry/v0.1.1") + `]`, 0},
		"the bare residue when no line writes a draft": {linesQuiet, `[` + draftJSON(61, "v0.9.0") + `]`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			walk := tc.walk(t, dir)
			var seq []string
			usePR(t, deleteFailureServer(t, walk, tc.releases, &seq))
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "release")
			if code != 4 {
				t.Fatalf("a residual delete that would not go exited %d, want 4 — the delete is its line's "+
					"entire action, and a sibling's landed draft is no write of that line's to be lenient "+
					"about\nstderr: %s", code, stderr)
			}
			if len(seq) <= tc.upserts || !strings.HasPrefix(seq[tc.upserts], "DELETE ") {
				t.Fatalf("write sequence = %v, want %d upsert(s) and then the residual's DELETE", seq, tc.upserts)
			}
			for _, w := range seq[:tc.upserts] {
				if !strings.HasPrefix(w, "POST ") {
					t.Fatalf("write sequence = %v, want every upsert before any delete", seq)
				}
			}
		})
	}
}

// TestReleasePackagesStrayDeleteFailureKeepsTheNotes is the other half of the
// split above, the packages twin of TestReleaseStrayDeleteFailureKeepsTheNotes:
// a draft left over beside a line's landed draft — the line's second draft, or
// the bare residue once any line has written — is convergence bookkeeping, and
// one that will not go is a warning on a green run.
func TestReleasePackagesStrayDeleteFailureKeepsTheNotes(t *testing.T) {
	for name, tc := range map[string]struct {
		releases string
		id       string
	}{
		"a moving line's second draft":           {`[` + draftJSON(53, "haiku/v0.2.0") + `,` + draftJSON(52, "haiku/v0.1.5") + `]`, "release id 52"},
		"the bare residue beside a line's draft": {`[` + draftJSON(61, "v0.9.0") + `]`, "release id 61"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			walk := haikuMoves(t, dir)
			var seq []string
			usePR(t, deleteFailureServer(t, walk, tc.releases, &seq))
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "release")
			if code != 0 {
				t.Fatalf("a stray glyph could not delete exited %d, want 0 — haiku's notes landed\nstderr: %s", code, stderr)
			}
			if stdout == "" || len(seq) == 0 || strings.HasPrefix(seq[0], "DELETE ") {
				t.Fatalf("stdout = %q, write sequence = %v; haiku's draft must be written first and reported", stdout, seq)
			}
			if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, tc.id) {
				t.Errorf("the warning must name the draft that would not go (%s):\n%s", tc.id, stderr)
			}
		})
	}
}

// TestReleasePackagesDryRunCountsResidualAndStaleApart: the dry run names the
// two kinds of delete with the glossary's words, because their failures differ
// — a residual's fails the run, a stray's is a warning — and the real run
// already calls a none line's draft residual. One "stale" count over both told
// an all-none run, which has no upsert, of deletes "after the upserts".
func TestReleasePackagesDryRunCountsResidualAndStaleApart(t *testing.T) {
	for name, tc := range map[string]struct {
		walk     func(*testing.T, string) map[string]string
		releases string
		want     []string
		not      string
	}{
		"a none line beside a moving line's stray and the residue": {
			haikuMoves,
			`[` + draftJSON(53, "haiku/v0.2.0") + `,` + draftJSON(52, "haiku/v0.1.5") + `,` + draftJSON(51, "curry/v0.1.1") + `,` + draftJSON(61, "v0.9.0") + `]`,
			[]string{"dry run: 1 residual draft(s) to delete", "dry run: 2 stale draft(s) to delete after the upserts"},
			"",
		},
		"every line none": {
			linesQuiet,
			`[` + draftJSON(51, "curry/v0.1.1") + `,` + draftJSON(61, "v0.9.0") + `]`,
			[]string{"dry run: 2 residual draft(s) to delete"},
			"stale draft",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			walk := tc.walk(t, dir)
			var writes []apiWrite
			usePR(t, releaseServer(t, walk, tc.releases, &writes))
			t.Chdir(dir)

			_, _, stderr := runGlyph(t, "release", "--dry-run")
			if len(writes) != 0 {
				t.Fatalf("a dry run wrote: %+v", writes)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("the dry run must count the deletes by kind (%q):\n%s", want, stderr)
				}
			}
			if tc.not != "" && strings.Contains(stderr, tc.not) {
				t.Errorf("no line writes a draft, so nothing is a stray (%q):\n%s", tc.not, stderr)
			}
		})
	}
}

// TestReleasePackagesFailedResidualStrandsNoOtherDelete pins how the two
// delete passes meet (t-xz1z): a none line's residual that will not go fails
// the run (4), but only once every other delete was tried — another line's
// residual, a moving line's stray, the bare residue — and the residuals go
// before the strays. The first cut returned at the failed residual: the
// stray and the residue beside it were neither deleted nor warned about,
// where the run before the split had deleted both (measured 2026-10-04:
// PATCH 53, DELETE 51:422 and exit 4, against PATCH 53, DELETE 52,
// DELETE 51:422, DELETE 61 and exit 0).
func TestReleasePackagesFailedResidualStrandsNoOtherDelete(t *testing.T) {
	t.Run("a moving line's stray and the bare residue still go", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		walk := haikuMoves(t, dir)
		releases := `[` + draftJSON(53, "haiku/v0.2.0") + `,` + draftJSON(52, "haiku/v0.1.5") + `,` +
			draftJSON(51, "curry/v0.1.1") + `,` + draftJSON(61, "v0.9.0") + `]`
		var seq []string
		usePR(t, failingReleaseServer(t, walk, releases, func(r *http.Request) int {
			if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/51") {
				return http.StatusUnprocessableEntity
			}
			return 0
		}, &seq))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("curry's residual would not go, so the run exited %d, want 4\nstderr: %s", code, stderr)
		}
		want := []string{
			"PATCH " + releasesPath + "/53",
			"DELETE " + releasesPath + "/51",
			"DELETE " + releasesPath + "/52",
			"DELETE " + releasesPath + "/61",
		}
		if !slices.Equal(seq, want) {
			t.Fatalf("write sequence = %v, want %v — every upsert, then the residuals, then the strays, "+
				"and a residual that will not go strands none of the deletes after it", seq, want)
		}
		for _, notice := range []string{
			"discarded the stale draft haiku/v0.1.5 (release id 52)",
			"the bare draft v0.9.0 (release id 61) is the single line's residue",
			releasesPath + "/51",
		} {
			if !strings.Contains(stderr, notice) {
				t.Errorf("stderr must report every delete's outcome (%q):\n%s", notice, stderr)
			}
		}
	})
	t.Run("every line's residual is tried, and each that would not go is named", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		walk := linesQuiet(t, dir)
		releases := `[` + draftJSON(51, "curry/v0.1.1") + `,` + draftJSON(61, "v0.9.0") + `]`
		var seq []string
		usePR(t, deleteFailureServer(t, walk, releases, &seq))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("no residual would go, so the run exited %d, want 4\nstderr: %s", code, stderr)
		}
		var tried []string
		for _, w := range seq {
			if !slices.Contains(tried, w) {
				tried = append(tried, w)
			}
		}
		if want := []string{"DELETE " + releasesPath + "/51", "DELETE " + releasesPath + "/61"}; !slices.Equal(tried, want) {
			t.Fatalf("deletes tried = %v, want %v — curry's failure must not strand the bare residue", tried, want)
		}
		if !strings.Contains(stderr, releasesPath+"/51") || !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "release id 61") {
			t.Errorf("the run fails on the first residual and must still name the second:\n%s", stderr)
		}
	})
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

// TestReleasePackagesACandidateTagConvergesOneLineAlone is the candidate
// shape of the test above — the consequence t-gt9n named: with every line
// walking from haiku's candidate, this run rewrote curry's rolling draft
// from a range nobody asked about. A candidate names its line, and that
// line alone is converged.
func TestReleasePackagesACandidateTagConvergesOneLineAlone(t *testing.T) {
	dir, _ := packagesRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0-rc.1")
	_, routes := squashAcrossLines(t, dir, 7)
	var writes []apiWrite
	usePR(t, releaseServer(t, routes, `[`+draftJSON(91, "curry/v0.5.0")+`]`, &writes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--since-tag=haiku/v0.2.0-rc.1", "--json")
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

// TestReleasePackagesExcludedAuthorBumpStaysOnItsLine: the draft is where the
// leak was user-visible — the first cut wrote a haiku-only dependabot bump
// into every line's rolling draft (measured 2026-09-11 on
// glyph-monorepo-test: curry/v0.1.1's body carried it under Fixes and
// Dependencies). Placed by its files, the bump is in haiku's draft alone.
func TestReleasePackagesExcludedAuthorBumpStaysOnItsLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	touch(t, dir, "dependabot[bot]", ":arrow_up:(haiku)~ bump a haiku-only dependency", "haiku/poem.go")
	var writes []apiWrite
	usePR(t, releaseServer(t, routes, `[]`, &writes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 || writes[0].body["tag_name"] != "haiku/v0.2.0" || writes[1].body["tag_name"] != "curry/v0.1.1" {
		t.Fatalf("writes = %+v, want haiku/v0.2.0 then curry/v0.1.1", writes)
	}
	h, _ := writes[0].body["body"].(string)
	c, _ := writes[1].body["body"].(string)
	if !strings.Contains(h, "bump a haiku-only dependency") {
		t.Fatalf("haiku's draft must carry the bump its files placed on haiku:\n%s", h)
	}
	if strings.Contains(c, "bump a haiku-only dependency") {
		t.Fatalf("curry's draft must not carry a haiku-only bump:\n%s", c)
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
// answers only when every line is none, and the verdict reports the target
// the placeholders point at.
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

	// A placeholder is a draft and points at target, so the verdict reports
	// it although no line moves: the gate is the draft count, never the
	// moving-line count.
	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 1 {
		t.Fatalf("release --json exited %d, want 1\nstderr: %s", code, stderr)
	}
	head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")
	if res := decodeReleaseLines(t, stdout); res.Target != head {
		t.Fatalf("target = %q, want %s — the placeholders point at it: %s", res.Target, head, stdout)
	}
}
