package cli

import (
	"strings"
	"testing"
)

// TestLintMessageClean: a convention-clean message is a silent success — no
// payload, exit 0 (Rule of Silence).
func TestLintMessageClean(t *testing.T) {
	code, stdout, _ := runGlyph(t, "lint", "--message", ":bug: fix a crash")
	if code != 0 {
		t.Fatalf("clean lint --message exited %d, want 0", code)
	}
	if stdout != "" {
		t.Fatalf("clean lint --message wrote %q to stdout, want nothing", stdout)
	}
}

// TestLintMessageLegacyFormatStaysClean: pre-glyph history keeps linting — the
// retired Conventional token is accepted, not flagged.
func TestLintMessageLegacyFormatStaysClean(t *testing.T) {
	code, _, stderr := runGlyph(t, "lint", "--message", ":wrench: ci(hub): raise the zizmor gate")
	if code != 0 {
		t.Fatalf("legacy-format lint exited %d, want 0\nstderr: %s", code, stderr)
	}
}

// TestLintMessageViolations: violations exit 3 with a structured stderr
// envelope carrying the stable rule ids, keeping stdout pure.
func TestLintMessageViolations(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "lint", "--message", ":bug: Fix a crash.")
	if code != 3 {
		t.Fatalf("lint --message with violations exited %d, want 3", code)
	}
	if stdout != "" {
		t.Fatalf("violations must go to stderr, stdout got %q", stdout)
	}
	for _, rule := range []string{"uppercase-subject", "trailing-period"} {
		if !strings.Contains(stderr, rule) {
			t.Fatalf("stderr envelope is missing rule %q:\n%s", rule, stderr)
		}
	}
}

// TestLintMessageUnknownCode: an unknown gitmoji is a violation against the
// real embedded table — never a silent pass.
func TestLintMessageUnknownCode(t *testing.T) {
	code, _, stderr := runGlyph(t, "lint", "--message", ":not-a-real-code: fix a crash")
	if code != 3 {
		t.Fatalf("unknown code exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "unknown-gitmoji") {
		t.Fatalf("stderr envelope is missing unknown-gitmoji:\n%s", stderr)
	}
}

// TestLintStdin: --stdin reads the message from the input stream (the
// commit-msg hook path) and stays authoring-mode — :construction: is legal.
func TestLintStdin(t *testing.T) {
	oldIn := in
	in = strings.NewReader(":construction: try things\n")
	defer func() { in = oldIn }()
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 0 {
		t.Fatalf("lint --stdin exited %d, want 0 (WIP is legal at authoring time)\nstderr: %s", code, stderr)
	}
}

// TestLintStdinViolation: the stdin path still lints — a violation exits 3.
func TestLintStdinViolation(t *testing.T) {
	oldIn := in
	in = strings.NewReader("no gitmoji here\n")
	defer func() { in = oldIn }()
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 3 {
		t.Fatalf("lint --stdin with a bad message exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "malformed-subject") {
		t.Fatalf("stderr envelope is missing malformed-subject:\n%s", stderr)
	}
}

// TestLintModeFlagsAreExclusiveAndRequired: exactly one of --range /
// --message / --stdin — none or two is a usage error (exit 2).
func TestLintModeFlagsAreExclusiveAndRequired(t *testing.T) {
	if code, _, _ := runGlyph(t, "lint"); code != 2 {
		t.Fatalf("lint with no mode exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--message", ":bug: x", "--stdin"); code != 2 {
		t.Fatalf("lint with two modes exited %d, want 2", code)
	}
}

// TestLintRange: over a PR-shaped range the merge-candidate rules apply
// (:construction: blocks), excluded commits (bots, autosquash) are skipped
// rather than failed, and each violation carries its commit SHA.
func TestLintRange(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")   // bot: skipped
	testCommit(t, dir, "akira-toriyama", "fixup! :bug: fix a crash")   // autosquash: skipped
	testCommit(t, dir, "akira-toriyama", ":construction: try an idea") // WIP: violation here
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")     // malformed: violation
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("violations must go to stderr, stdout got %q", stdout)
	}
	for _, want := range []string{"wip-merge-candidate", "malformed-subject"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr envelope is missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "build(deps)") {
		t.Fatalf("bot commit leaked into the violations:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"sha"`) {
		t.Fatalf("range violations must carry commit SHAs:\n%s", stderr)
	}
}

// TestLintRangeClean: a range of clean and skipped commits is a silent 0.
func TestLintRangeClean(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu\n\nBody.\n\n---（和訳）\nメニュー。")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("clean lint --range exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("clean lint --range wrote %q to stdout, want nothing", stdout)
	}
}

// TestLintRangeOutsideRepo: git failures classify as API (exit 4), never as
// lint or usage.
func TestLintRangeOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := runGlyph(t, "lint", "--range", "main..HEAD"); code != 4 {
		t.Fatalf("lint --range outside a repo exited %d, want 4", code)
	}
}

// TestLintRangeUsageGuards: an empty or option-shaped --range is caught as
// usage (exit 2) before git ever runs.
func TestLintRangeUsageGuards(t *testing.T) {
	if code, _, _ := runGlyph(t, "lint", "--range", ""); code != 2 {
		t.Fatalf("lint --range '' exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--range", "--all"); code != 2 {
		t.Fatalf("lint --range --all exited %d, want 2", code)
	}
}
