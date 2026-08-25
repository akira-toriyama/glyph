package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBumpMinor: the fold over a mixed range takes the max — stdout is the
// bare next version (pipe it straight into a tag step).
func TestBumpMinor(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	testCommit(t, dir, "akira-toriyama", ":memo:= document it")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.2.0\n" {
		t.Fatalf("bump stdout = %q, want %q", stdout, "v0.2.0\n")
	}
}

// TestBumpPatchFromTag: the current version comes from the highest parseable
// v* tag when --current is not given.
func TestBumpPatchFromTag(t *testing.T) {
	dir, base := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "not-a-version") // skipped by the picker
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump = exit %d stdout %q, want 0 / v0.1.1", code, stdout)
	}
}

// TestBumpNoneHuman: a docs-only range is the soft no-release exit: nothing on
// stdout, exit 1, the reason in the stderr envelope.
func TestBumpNoneHuman(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo:= document the bump model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 1 {
		t.Fatalf("docs-only bump exited %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("docs-only bump must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no release") {
		t.Fatalf("stderr should say why nothing releases:\n%s", stderr)
	}
}

// TestBumpNoneJSON: --json still emits the machine verdict (level none, no
// next) on stdout and exits 1 without a second stderr envelope.
func TestBumpNoneJSON(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo:= document the bump model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 1 {
		t.Fatalf("docs-only bump --json exited %d, want 1", code)
	}
	var res struct {
		Current string          `json:"current"`
		Level   string          `json:"level"`
		Next    *string         `json:"next"`
		Commits json.RawMessage `json:"commits"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("bump --json stdout is not JSON: %v\n%s", err, stdout)
	}
	if res.Level != "none" || res.Next != nil {
		t.Fatalf("bump --json none verdict = %+v, want level none and no next", res)
	}
	if res.Current != "v0.1.0" {
		t.Fatalf("current = %q, want v0.1.0", res.Current)
	}
	if res.Reason == "" {
		t.Fatalf("reason must explain the none verdict")
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("--json already carries the verdict; stderr should stay empty, got:\n%s", stderr)
	}
}

// TestBumpJSONShape: the machine schema is {current, level, next, commits[],
// reason} with per-commit sha/gitmoji/level/breaking/subject.
func TestBumpJSONShape(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump --json exited %d, want 0", code)
	}
	if strings.Count(strings.TrimRight(stdout, "\n"), "\n") != 0 {
		t.Fatalf("bump --json must be a single line, got:\n%s", stdout)
	}
	var res struct {
		Current string `json:"current"`
		Level   string `json:"level"`
		Next    string `json:"next"`
		Commits []struct {
			SHA     string `json:"sha"`
			Sigil   string `json:"sigil"`
			Level   string `json:"level"`
			Subject string `json:"subject"`
		} `json:"commits"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("bump --json stdout is not JSON: %v\n%s", err, stdout)
	}
	if res.Current != "v0.1.0" || res.Level != "minor" || res.Next != "v0.2.0" {
		t.Fatalf("bump --json = %+v, want v0.1.0 → minor → v0.2.0", res)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("commits carries %d entries, want 2: %s", len(res.Commits), stdout)
	}
	for _, c := range res.Commits {
		if len(c.SHA) != 40 || c.Sigil == "" || c.Level == "" || c.Subject == "" {
			t.Fatalf("commit entry is missing fields: %+v", c)
		}
	}
	if !strings.Contains(res.Reason, "minor") {
		t.Fatalf("reason %q should name the deciding level", res.Reason)
	}
}

// TestBumpEmptyRangeJSON: an empty range is a none verdict with commits
// normalized to [] (never null) so a consumer can index unconditionally.
func TestBumpEmptyRangeJSON(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", "HEAD..HEAD", "--json")
	if code != 1 {
		t.Fatalf("bump over an empty range exited %d, want 1", code)
	}
	if !strings.Contains(stdout, `"commits":[]`) {
		t.Fatalf("commits must normalize to [], got:\n%s", stdout)
	}
}

// TestBumpCurrentOverride: --current beats the tag lookup.
func TestBumpCurrentOverride(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--current", "v3.4.5")
	if code != 0 || stdout != "v3.4.6\n" {
		t.Fatalf("bump --current = exit %d stdout %q, want 0 / v3.4.6", code, stdout)
	}
}

// TestBumpCurrentInvalidIsUsage: a malformed --current is the caller's input —
// usage (exit 2), not git's fault.
func TestBumpCurrentInvalidIsUsage(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)
	if code, _, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--current", "garbage"); code != 2 {
		t.Fatalf("bump --current garbage should exit 2")
	}
}

// TestBumpNoTagsStartsAtZero: with no version tag anywhere, the base is
// v0.0.0 — the first release-worthy range yields v0.0.1 or v0.1.0 (v1.0.0
// needs a '%'; a bare '!' cannot leave 0.x).
func TestBumpNoTagsStartsAtZero(t *testing.T) {
	dir, base := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ first feature")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 || stdout != "v0.1.0\n" {
		t.Fatalf("bump without tags = exit %d stdout %q, want 0 / v0.1.0", code, stdout)
	}
}

// TestBumpUnknownCodeIsLint: an unknown gitmoji anywhere in the range is a
// hard lint failure (exit 3) — never silently classified.
func TestBumpUnknownCodeIsLint(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":not-a-real-code: mystery change")
	t.Chdir(dir)

	if code, _, _ := runGlyph(t, "bump", "--range", base+"..HEAD"); code != 3 {
		t.Fatalf("bump with an unknown code should exit 3")
	}
}

// TestBumpMalformedCommitIsLint: a non-parsing human commit in the range is a
// lint failure carrying the offending SHA.
func TestBumpMalformedCommitIsLint(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
	t.Chdir(dir)

	// The SHA is what makes the failure actionable: published history is
	// immutable, so the operator's next move is to go look at that commit.
	// Asserting the word "commit " instead proved only that range.go's format
	// string still had a literal in it — the message is "commit %.7s: %v", so
	// dropping the SHA left this green. The API path already asserts the sha
	// (pr_test.go's TestPRMalformedCommitIsLint); the local path now does too.
	sha := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	code, _, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("bump with a malformed commit exited %d, want 3", code)
	}
	if !strings.Contains(stderr, sha[:7]) {
		t.Fatalf("the lint error should name the offending commit %.7s:\n%s", sha, stderr)
	}
}

// TestBumpRequiresRange: bump with no input mode at all is usage. The
// squash-safe inputs ship too (--pr, --since-tag) and cobra requires exactly one
// of the three, so this pins the empty invocation, not an unimplemented feature.
func TestBumpRequiresRange(t *testing.T) {
	if code, _, _ := runGlyph(t, "bump"); code != 2 {
		t.Fatalf("bump without --range should exit 2")
	}
}

// TestBumpOutsideRepoIsAPI: git failures classify as API (exit 4).
func TestBumpOutsideRepoIsAPI(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := runGlyph(t, "bump", "--range", "main..HEAD"); code != 4 {
		t.Fatalf("bump outside a repo should exit 4")
	}
}

// TestBump0xBangStaysIn0x is the end-to-end half of the 0.x rule: a repository
// at v0.1.0 answers a breaking commit with v0.2.0, and the JSON still calls it
// major. Both halves are asserted together on purpose — the whole design is
// that the clamp moves the arithmetic without touching the classification, and
// a test that checked only the version would pass just as happily if the
// commit had quietly stopped being breaking.
func TestBump0xBangStaysIn0x(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":boom:! drop the legacy flag")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Current string `json:"current"`
		Level   string `json:"level"`
		Next    string `json:"next"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if res.Current != "v0.1.0" || res.Next != "v0.2.0" {
		t.Fatalf("verdict = %s → %s, want v0.1.0 → v0.2.0", res.Current, res.Next)
	}
	if res.Level != "major" {
		t.Fatalf("level = %q, want major (the clamp shortens the step, not the classification)", res.Level)
	}
}

// TestBumpPromoteReachesV1: '%' is the only door out of 0.x, and this walks
// through it — including the reason line, which must name the promoting commit
// rather than whichever breaking commit happened to come first.
func TestBumpPromoteReachesV1(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":boom:! drop the legacy flag")
	testCommit(t, dir, "akira-toriyama", ":rocket:% call it 1.0")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Level  string `json:"level"`
		Next   string `json:"next"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if res.Next != "v1.0.0" {
		t.Fatalf("next = %q, want v1.0.0", res.Next)
	}
	if res.Level != "major" {
		t.Fatalf("level = %q, want major — a fifth level word would vanish from every consumer that filters on one", res.Level)
	}
	if !strings.Contains(res.Reason, "call it 1.0") {
		t.Fatalf("reason = %q, want the promoting commit named", res.Reason)
	}
}

// TestBumpPromoteFrom1xIsAPlainMajor: from 1.x a '%' must step like '!'. A
// constant v1.0.0 would sit at or below the current version, which is the one
// shape checkPublishedFloor rejects outright (exit 4) — so this is the test
// that keeps a promotion from deadlocking every repository that already
// reached 1.0.
func TestBumpPromoteFrom1xIsAPlainMajor(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":rocket:% call it 1.0")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--current", "v1.4.2")
	if code != 0 || stdout != "v2.0.0\n" {
		t.Fatalf("bump = exit %d stdout %q, want 0 / v2.0.0", code, stdout)
	}
}

// TestBumpWarnsEachWarnedCommit pins the fold-side emission: every commit a
// warned pattern claimed annotates on the diagnostic stream, and the JSON
// row carries the same message — the verdict itself is untouched. This is
// the release-time half of the v1-window loudness: the dotfiles measurement
// (v1 verdict v1.0.0, v2 verdict none, release silently stopped) is exactly
// what a warning here would have named.
func TestBumpWarnsEachWarnedCommit(t *testing.T) {
	dir, base := testRepo(t)
	useV1WindowConfig(t, dir)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(x) a v1 subject with no sigil")
	testCommit(t, dir, "akira-toriyama", ":bug:~ a strict fix")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "v1-acceptance window") {
		t.Fatalf("the warned commit must annotate:\n%s", stderr)
	}
	var res struct {
		Commits []struct {
			Sigil string `json:"sigil"`
			Warn  string `json:"warn"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("bump --json: %v\n%s", err, stdout)
	}
	var warned int
	for _, c := range res.Commits {
		if c.Warn != "" {
			warned++
			if c.Sigil != "=" {
				t.Errorf("warned row = %+v, want the window's none sigil", c)
			}
		}
	}
	if warned != 1 {
		t.Errorf("%d warned row(s) in the machine verdict, want exactly the sigil-less commit", warned)
	}
}
