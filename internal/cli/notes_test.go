package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNotesMarkdown: the full Markdown body — the breaking hoist, section
// order, a scoped entry, a none commit dropped — pinned byte-for-byte with the
// real commit SHAs.
func TestNotesMarkdown(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a command palette")
	testCommit(t, dir, "akira-toriyama", ":memo: document the palette")
	testCommit(t, dir, "akira-toriyama", ":recycle: rework the store\n\nBREAKING CHANGE: Store is gone.")
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	t.Chdir(dir)
	shas := strings.Fields(testGit(t, dir, "akira-toriyama", "log", "--format=%H", "--reverse", base+"..HEAD"))
	if len(shas) != 4 {
		t.Fatalf("expected 4 commits in the range, got %v", shas)
	}

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Breaking Changes\n\n" +
		"- ♻️ rework the store (" + shas[2][:7] + ")\n\n" +
		"## Features\n\n" +
		"- ✨ **ui:** add a command palette (" + shas[0][:7] + ")\n\n" +
		"## Fixes\n\n" +
		"- 🐛 fix a crash (" + shas[3][:7] + ")\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesSkipsExcluded: a bot's feature commit never reaches the notes —
// the same participation rules as lint and bump.
func TestNotesSkipsExcluded(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "dependabot[bot]", ":sparkles: a bot feature that must stay out")
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0", code)
	}
	if strings.Contains(stdout, "bot feature") || strings.Contains(stdout, "## Features") {
		t.Fatalf("the bot commit must not reach the notes:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Fixes") {
		t.Fatalf("the human fix must reach the notes:\n%s", stdout)
	}
}

// TestNotesEmptyIsNoRelease: a docs-only range is the soft no-release exit —
// nothing on stdout, exit 1, the reason in the stderr envelope.
func TestNotesEmptyIsNoRelease(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo: document the notes model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 1 {
		t.Fatalf("docs-only notes exited %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("no-release must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no release notes") {
		t.Fatalf("stderr should say why there are no notes:\n%s", stderr)
	}
}

// TestNotesEmptyJSON: --json still emits the machine verdict (sections
// normalized to [], a reason) on stdout and exits 1 without a second stderr
// envelope.
func TestNotesEmptyJSON(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo: document the notes model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD", "--json")
	if code != 1 {
		t.Fatalf("docs-only notes --json exited %d, want 1", code)
	}
	if !strings.Contains(stdout, `"sections":[]`) {
		t.Fatalf("sections must normalize to [], got:\n%s", stdout)
	}
	var res struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("notes --json stdout is not JSON: %v\n%s", err, stdout)
	}
	if res.Reason == "" {
		t.Fatalf("reason must explain the empty verdict: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("--json already carries the verdict; stderr should stay empty, got:\n%s", stderr)
	}
}

// TestNotesJSONShape: the machine schema is {sections:[{title,entries:[{sha,
// code,emoji,scope,subject,breaking}]}]} on one line.
func TestNotesJSONShape(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a command palette")
	testCommit(t, dir, "akira-toriyama", ":bug:! drop the broken fallback")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("notes --json exited %d, want 0", code)
	}
	if strings.Count(strings.TrimRight(stdout, "\n"), "\n") != 0 {
		t.Fatalf("notes --json must be a single line, got:\n%s", stdout)
	}
	var res struct {
		Sections []struct {
			Title   string `json:"title"`
			Entries []struct {
				SHA      string `json:"sha"`
				Code     string `json:"code"`
				Emoji    string `json:"emoji"`
				Scope    string `json:"scope"`
				Subject  string `json:"subject"`
				Breaking bool   `json:"breaking"`
			} `json:"entries"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("notes --json stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(res.Sections) != 2 {
		t.Fatalf("want the Breaking Changes and Features sections, got %s", stdout)
	}
	if res.Sections[0].Title != "Breaking Changes" || res.Sections[1].Title != "Features" {
		t.Fatalf("section order = %q, %q; want Breaking Changes, Features", res.Sections[0].Title, res.Sections[1].Title)
	}
	e := res.Sections[0].Entries[0]
	if len(e.SHA) != 40 || e.Code != ":bug:" || e.Emoji == "" || !e.Breaking || e.Subject == "" {
		t.Fatalf("breaking entry is missing fields: %+v", e)
	}
	f := res.Sections[1].Entries[0]
	if f.Scope != "ui" || f.Breaking {
		t.Fatalf("feature entry = %+v, want scope ui and breaking false", f)
	}
}

// TestNotesUnknownCodeIsLint: an unknown gitmoji anywhere in the range is a
// hard lint failure (exit 3) — never silently dropped from the notes.
func TestNotesUnknownCodeIsLint(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":not-a-real-code: mystery change")
	t.Chdir(dir)

	if code, _, _ := runGlyph(t, "notes", "--range", base+"..HEAD"); code != 3 {
		t.Fatalf("notes with an unknown code should exit 3")
	}
}

// TestNotesMalformedCommitIsLint: a non-parsing human commit in the range is a
// lint failure carrying the offending SHA.
func TestNotesMalformedCommitIsLint(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("notes with a malformed commit exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "commit ") {
		t.Fatalf("the lint error should name the offending commit:\n%s", stderr)
	}
}

// TestNotesRequiresRange: notes without --range is usage — the squash-safe
// inputs (--pr / --since-tag) arrive with the GitHub plumbing phase.
func TestNotesRequiresRange(t *testing.T) {
	if code, _, _ := runGlyph(t, "notes"); code != 2 {
		t.Fatalf("notes without --range should exit 2")
	}
}

// TestNotesOutsideRepoIsAPI: git failures classify as API (exit 4).
func TestNotesOutsideRepoIsAPI(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := runGlyph(t, "notes", "--range", "main..HEAD"); code != 4 {
		t.Fatalf("notes outside a repo should exit 4")
	}
}
