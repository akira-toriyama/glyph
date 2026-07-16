package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	srv := walkServer(t, map[string]string{
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

// TestReleaseRequiresDryRunUntilUpsertLands: the ratified surface is "bare
// glyph release upserts the rolling draft" (t-skd3). Until that write path
// exists, bare release must fail loud and point at --dry-run — shipping it as
// a silent read-only preview would flip its meaning to a WRITE later, and a
// surface must never change sides quietly.
func TestReleaseRequiresDryRunUntilUpsertLands(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "release")
	if code != 2 {
		t.Fatalf("bare release exited %d, want 2 (not implemented yet)\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("bare release wrote a payload:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--dry-run") {
		t.Errorf("bare release does not point at --dry-run:\n%s", stderr)
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
	Commits []struct {
		SHA      string `json:"sha"`
		Code     string `json:"code"`
		Level    string `json:"level"`
		Breaking bool   `json:"breaking"`
		Subject  string `json:"subject"`
	} `json:"commits"`
	Reason string `json:"reason"`
}

// TestReleaseJSON: the machine verdict carries everything the rolling-draft
// step will need in one object — the tag to draft and the body to attach —
// plus the same audit trail bump --json emits.
func TestReleaseJSON(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	srv := walkServer(t, map[string]string{
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
	srv := walkServer(t, map[string]string{
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
	srv := walkServer(t, map[string]string{
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
	srv := walkServer(t, map[string]string{
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
	srv := walkServer(t, map[string]string{
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
	srv := walkServer(t, map[string]string{
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
