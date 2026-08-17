package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/akira-toriyama/glyph/internal/draftplan"
)

// enableDraftOnNone flips the fixture's glyph.toml to draft_on_none = true.
// The file is rewritten in the working tree and deliberately NOT committed:
// the engine reads the checkout's config, and a commit for it would join the
// walk the test is measuring.
//
// The flag's only tests were draftplan's, which are pure — they cannot see
// the wiring here (which tag the placeholder takes, which write it becomes,
// the silent exit-1 with a URL on stdout), and that wiring had no test at any
// level.
func enableDraftOnNone(t *testing.T) {
	t.Helper()
	const off, on = "draft_on_none = false", "draft_on_none = true"
	b, err := os.ReadFile("glyph.toml")
	if err != nil {
		t.Fatalf("read the fixture config: %v", err)
	}
	if !bytes.Contains(b, []byte(off)) {
		t.Fatalf("the fixture config does not carry %q — the preset default moved", off)
	}
	if werr := os.WriteFile("glyph.toml", bytes.Replace(b, []byte(off), []byte(on), 1), 0o600); werr != nil {
		t.Fatalf("rewrite the fixture config: %v", werr)
	}
}

// TestReleaseDraftOnNoneCreatesThePlaceholder: with the flag on and nothing
// drafted, a none verdict CREATES the placeholder — one POST carrying
// draftplan.PlaceholderTag, drafted like every other glyph write. The verdict
// is still "no release is due" (exit 1, silent), and the draft's URL is the
// payload: draft_on_none keeps a door open, it does not release.
func TestReleaseDraftOnNoneCreatesThePlaceholder(t *testing.T) {
	var writes []apiWrite
	walk := noneWalk(t)
	enableDraftOnNone(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release")
	if code != 1 {
		t.Fatalf("release exited %d, want 1 (no release is due)\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "POST" {
		t.Fatalf("writes = %+v, want exactly one POST", writes)
	}
	body := writes[0].body
	if body["tag_name"] != draftplan.PlaceholderTag || body["name"] != draftplan.PlaceholderTag {
		t.Errorf("POST body = %+v, want tag_name and name %q", body, draftplan.PlaceholderTag)
	}
	if body["draft"] != true {
		t.Errorf("the placeholder must be a DRAFT: %+v", body)
	}
	if stdout != "https://github.example/releases/900\n" {
		t.Errorf("stdout = %q, want the placeholder draft's URL", stdout)
	}
}

// TestReleaseDraftOnNoneKeepsThePlaceholderByID: a second quiet merge grows
// the SAME placeholder — one PATCH by release id, never a second POST. The id
// is the identity because tag-name resolution can hit a published release
// sharing the tag (cli/cli#9367).
func TestReleaseDraftOnNoneKeepsThePlaceholderByID(t *testing.T) {
	var writes []apiWrite
	walk := noneWalk(t)
	enableDraftOnNone(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, draftplan.PlaceholderTag)+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 1 {
		t.Fatalf("release exited %d, want 1\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "PATCH" || writes[0].path != releasesPath+"/11" {
		t.Fatalf("writes = %+v, want exactly one PATCH of release 11", writes)
	}
	if writes[0].body["tag_name"] != draftplan.PlaceholderTag {
		t.Errorf("PATCH body = %+v, want the placeholder tag", writes[0].body)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout)
	}
	if v.Action != "update" || v.Tag != draftplan.PlaceholderTag || v.Level != "none" {
		t.Errorf("verdict = %+v, want action update, tag %q, level none", v, draftplan.PlaceholderTag)
	}
}

// TestReleaseDraftOnNoneRetagsThePlaceholder is the whole promise of the
// flag: the draft a quiet merge kept alive becomes the real release. A patch
// verdict retags that same draft id to v0.1.1 — one PATCH, no second draft
// (POST) and no throwing the placeholder away first (DELETE).
func TestReleaseDraftOnNoneRetagsThePlaceholder(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	enableDraftOnNone(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, draftplan.PlaceholderTag)+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0 (a patch verdict is a release)\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "PATCH" || writes[0].path != releasesPath+"/11" {
		t.Fatalf("writes = %+v, want exactly one PATCH of release 11 — the placeholder is retagged in place", writes)
	}
	if writes[0].body["tag_name"] != "v0.1.1" || writes[0].body["draft"] != true {
		t.Errorf("PATCH body = %+v, want tag_name v0.1.1 and draft true", writes[0].body)
	}
	if stdout != "https://github.example/releases/11\n" {
		t.Errorf("stdout = %q, want the retagged draft's URL", stdout)
	}
}

// TestReleaseDraftOnNoneOffConvergesThePlaceholderAway: with the flag off —
// the shipped default — a none verdict deletes the placeholder rather than
// leaving it as an orphan glyph pretends not to know, and the verdict carries
// NO tag: nothing converges to a tag, so a consumer reading one would be
// reading a version this run never claimed.
func TestReleaseDraftOnNoneOffConvergesThePlaceholderAway(t *testing.T) {
	var writes []apiWrite
	walk := noneWalk(t)
	srv := releaseServer(t, walk, `[`+draftJSON(11, draftplan.PlaceholderTag)+`]`, &writes)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "release", "--json")
	if code != 1 {
		t.Fatalf("release exited %d, want 1\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 || writes[0].method != "DELETE" || writes[0].path != releasesPath+"/11" {
		t.Fatalf("writes = %+v, want exactly one DELETE of release 11", writes)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout)
	}
	if raw["action"] != "delete" {
		t.Errorf("action = %v, want delete", raw["action"])
	}
	if _, ok := raw["tag"]; ok {
		t.Errorf("a converged-away none verdict must carry no tag key: %s", stdout)
	}
}
