package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNotesMarkdown: the full Markdown body under the gemoji preset — the
// section order the config declares, a major landing in Breaking Changes, a
// none commit landing nowhere (no none-section is configured), and the
// note.line template rendered with the mention fence over the author.
//
// Every commit here reached main without a merged pull — which is EVERY
// commit under --range, since the range walk resolves no pulls at all — so
// this is the empty arm of the preset's optional span, byte-exact: no `()`,
// and no double space where the span used to sit. The populated arm is
// TestReleaseDryRunGolden, whose two lines both carry `(#N)`; break the span
// in either direction and one of the two fails.
func TestNotesMarkdown(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a command palette")
	testCommit(t, dir, "akira-toriyama", ":memo:= document the palette")
	testCommit(t, dir, "akira-toriyama", ":recycle:! rework the store")
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Breaking Changes\n\n" +
		"- rework the store @akira-toriyama\n\n" +
		"## Features\n\n" +
		"- add a command palette @akira-toriyama\n\n" +
		"## Fixes\n\n" +
		"- fix a crash @akira-toriyama\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesSectionsAloneDecide: exclude_authors keeps a bot out of the FOLD,
// never out of the notes — whether a commit appears is note.sections'
// decision alone (ratified), so a bot's raw bump lands in the author-axis
// Dependencies section and a bot's conforming commit lands in its semver
// section too.
func TestNotesSectionsAloneDecide(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "dependabot[bot]", "Bump a dep from 1 to 2")
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0", code)
	}
	if !strings.Contains(stdout, "## Dependencies") || !strings.Contains(stdout, "Bump a dep from 1 to 2") {
		t.Fatalf("the bot commit must land in the Dependencies author section, rendered from its raw first line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Fixes") {
		t.Fatalf("the human fix must reach the notes:\n%s", stdout)
	}
}

// TestNotesEmptyIsNoRelease: a docs-only range is the soft no-release exit —
// nothing on stdout, exit 1, the reason in the stderr envelope.
func TestNotesEmptyIsNoRelease(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":memo:= document the notes model")
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
	testCommit(t, dir, "akira-toriyama", ":memo:= document the notes model")
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
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a command palette")
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
			Title string   `json:"title"`
			Lines []string `json:"lines"`
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
	if len(res.Sections[0].Lines) != 1 || !strings.Contains(res.Sections[0].Lines[0], "drop the broken fallback") {
		t.Fatalf("breaking section lines = %v", res.Sections[0].Lines)
	}
	if !strings.Contains(res.Sections[1].Lines[0], "add a command palette") {
		t.Fatalf("feature section lines = %v", res.Sections[1].Lines)
	}
}

// TestNotesUnmatchedIsRendered: notes never refuse a range — an unmatched
// commit has no semver level, so it can only surface through an author-axis
// section; whether it appears at all is note.sections' decision (the fold is
// where an unmatched commit refuses, and bump/release run the fold).
func TestNotesUnmatchedIsRendered(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":not_a_real_code:^ mystery change")
	t.Chdir(dir)

	if code, _, _ := runGlyph(t, "notes", "--range", base+"..HEAD"); code != 0 {
		t.Fatalf("notes over a matched unknown-prefix commit should exit 0 — the prefix is the author's call")
	}
}

// TestNotesUnmatchedLandsNowhereByDefault: notes never refuse a range — an
// unmatched commit renders through the bot fallback (raw first line) but has
// no semver level, so under the preset (whose only author section names
// dependabot) an unmatched human commit lands in no section at all, and a
// range holding only it is the soft no-release. The STRICT arm lives in the
// fold: bump and release refuse this same range (Q2), so nothing publishes a
// version around the commit notes quietly passed over.
func TestNotesUnmatchedLandsNowhereByDefault(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 1 {
		t.Fatalf("notes over an unmatched-only range exited %d, want 1 (nothing lands in a section)", code)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty, got %q", stdout)
	}
}

// TestNotesRequiresRange: notes with no input mode at all is usage. The
// squash-safe inputs ship too (--pr, --since-tag) and cobra requires exactly one
// of the three, so this pins the empty invocation, not an unimplemented feature.
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
