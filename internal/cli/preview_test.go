package cli

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// testRepoUntagged is testRepo without its v0.1.0 tag — a repository before its
// first release, which is the only state the release-floor guard is about. It
// lives here rather than in gitrepo_test.go because it is the negative case of
// that helper's central assumption, and reading the two side by side is the
// point.
func testRepoUntagged(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	testGit(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", ":tada: begin the project")
	return dir
}

// TestPreviewUntaggedSkipsTheWalk is the release-floor guard, and it is a COST
// test as much as a correctness one. walkServer fails the test on any path it
// was not given, so serving ONLY the PR's commits proves the pending walk never
// ran: on a repo with no v* tag that walk would resolve the entire history at
// one API round-trip per commit — the thing that makes fleet-wide distribution
// unsafe — for an answer that cannot matter, since nothing is unreleased when
// nothing was ever released.
func TestPreviewUntaggedSkipsTheWalk(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+
		apiCommit("aaa1111", "akira-toriyama", ":sparkles: add the palette")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "the first release here would be **v0.1.0**") {
		t.Errorf("untagged headline missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no v* release tag yet") {
		t.Errorf("body does not explain the skipped fold:\n%s", stdout)
	}
	// The skipped walk must be named, not silent — the house rule is that an
	// input glyph declines to read is a fact the log states.
	if !strings.Contains(stderr, "no v* release tag here") {
		t.Errorf("skipped walk not warned about: %s", stderr)
	}
}

// TestPreviewNoneExitsZero: a none verdict is a real answer here, unlike bump
// and notes where it is the soft no-release signal. "This PR moves nothing" is
// exactly what the reviewer asked, and a caller that must post the comment
// cannot have the renderer exit non-zero on the commonest outcome.
func TestPreviewNoneExitsZero(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":memo: document it")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("a none preview must exit 0, got %d: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "<!-- glyph-pr-verdict -->") {
		t.Errorf("body must still render for a none verdict:\n%s", stdout)
	}
	if !strings.Contains(stdout, "moves nothing") {
		t.Errorf("none headline missing:\n%s", stdout)
	}
}

// TestPreviewJSONFoldsTheLevels pins the machine surface: level/next are the
// FOLDED answer, so a caller never re-derives the winner from the two sides —
// which is the shell arithmetic this command exists to absorb.
func TestPreviewJSONFoldsTheLevels(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":bug: fix a crash")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview --json exited %d: %s", code, stderr)
	}
	var got struct {
		Current  string `json:"current"`
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Level != "patch" || got.Next != "v0.0.1" {
		t.Errorf("folded verdict = %s/%s, want patch/v0.0.1", got.Level, got.Next)
	}
	if !got.Untagged {
		t.Error("untagged not reported for a repo with no v* tag")
	}
	// pending is uncomputed here (the walk was skipped) and must serialize as
	// the level "none", never as the empty zero value of the Bump type.
	if got.Pending != "none" {
		t.Errorf("pending = %q, want %q", got.Pending, "none")
	}
	if got.PR != "patch" {
		t.Errorf("pr = %q, want patch", got.PR)
	}
	if !strings.HasPrefix(got.Body, "<!-- glyph-pr-verdict -->") {
		t.Errorf("body missing its marker: %q", got.Body)
	}
}

// TestPreviewNotes: --notes folds the rendered release notes into the comment.
// Off by default — the headline is the message and the notes are for whoever
// asks.
func TestPreviewNotes(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	body := `[` + apiCommit("aaa1111", "akira-toriyama", ":sparkles:(ui) add the palette") + `]`
	usePR(t, prServer(t, 7, body))

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--notes")
	if code != 0 {
		t.Fatalf("preview --notes exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "<summary>Release notes preview</summary>") {
		t.Errorf("notes not folded in:\n%s", stdout)
	}
	if !strings.Contains(stdout, "add the palette") {
		t.Errorf("notes body missing:\n%s", stdout)
	}
}

// TestPreviewNotesOffByDefault guards the default: the same input without
// --notes must carry no disclosure block.
func TestPreviewNotesOffByDefault(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	usePR(t, prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":sparkles: add it")+`]`))

	code, stdout, _ := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d", code)
	}
	if strings.Contains(stdout, "<details>") {
		t.Errorf("notes rendered without --notes:\n%s", stdout)
	}
}

// TestPreviewFoldsPendingOverPR is the command's reason to exist, on the path
// that actually walks: a tagged repo with a minor already merged-but-unreleased
// and a PR that only fixes a bug. The PR's own level is patch, but the version
// it lands on is the pending minor — the fold no caller should be doing in
// shell. The walk resolves the squash commit to PR 3 and re-expands it, which
// is also what makes the pending side squash-safe.
func TestPreviewFoldsPendingOverPR(t *testing.T) {
	dir, _ := testRepo(t) // tagged v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles: land a feature (#3)")
	t.Chdir(dir)
	head := strings.TrimSpace(testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD"))

	srv := walkServer(t, map[string]string{
		// The PR under preview: a lone bug fix.
		pullCommitsPath(7): `[` + apiCommit("bbb2222", "akira-toriyama", ":bug: fix a crash") + `]`,
		// The walk: main's squash commit resolves to merged PR 3 …
		commitPullsPath(head): `[{"number":3,"merged_at":"2026-07-17T00:00:00Z","merge_commit_sha":"` + head + `"}]`,
		// … which re-expands into the feature that makes the pending level minor.
		pullCommitsPath(3): `[` + apiCommit("ccc3333", "akira-toriyama", ":sparkles: land a feature") + `]`,
	})
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	var got struct {
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Untagged {
		t.Error("a tagged repo must not report untagged")
	}
	if got.PR != "patch" || got.Pending != "minor" {
		t.Errorf("sides = pr:%s pending:%s, want pr:patch pending:minor", got.PR, got.Pending)
	}
	// The fold: pending (minor) outranks the PR (patch), so the version lands on
	// the minor and the PR does not move it.
	if got.Level != "minor" || got.Next != "v0.2.0" {
		t.Errorf("fold = %s/%s, want minor/v0.2.0", got.Level, got.Next)
	}
	if !strings.Contains(got.Body, "the next release stays **v0.2.0** (a **minor** bump is already pending)") {
		t.Errorf("headline does not report the pending winner:\n%s", got.Body)
	}
}

// TestPreviewRequiresPR: without --pr there is no pull request to preview.
// Caller input, so usage (2) — never a walk over an empty input.
func TestPreviewRequiresPR(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	code, _, stderr := runGlyph(t, "preview")
	if code != 2 {
		t.Fatalf("missing --pr exited %d, want 2 (usage): %s", code, stderr)
	}
}
