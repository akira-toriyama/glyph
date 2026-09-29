package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/testutil"
)

// TestConventionalPresetTakesTheVersionOnlyFromTheSigil runs the file `glyph
// init --conventional` writes through every gate that judges a message
// (t-h9y7, DESIGN §2). A type word is no version: `feat: add x` is refused
// at exit 3 by both hook modes, by lint --range and by bump, where a plain
// Conventional Commits reading would fold it minor, and the refusal quotes
// the form the author should have written. A BREAKING CHANGE footer is
// prose: `fix~:` over one folds patch. `feat^:` passing is the control that
// the conventional grammar is the one loaded — every other file refuses
// `feat: add x` too.
//
// bite-exempt: ratifies the behaviour the tree already has, so it cannot fail
// against pre-PR source; the mutation-ledger rows
// conventional-preset-reads-the-type-as-the-version.patch and
// presets-read-the-breaking-change-footer.patch are what prove it still bites.
func TestConventionalPresetTakesTheVersionOnlyFromTheSigil(t *testing.T) {
	dir := testutil.NewRepo(t)
	t.Chdir(dir)
	if code, _, stderr := runGlyph(t, "init", "--conventional", "--force"); code != int(core.CodeOK) {
		t.Fatalf("init --conventional --force exited %d\nstderr: %s", code, stderr)
	}
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-am", "chore=: adopt the conventional preset")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")

	if code, _, stderr := runGlyph(t, "lint", "--message", "feat^: add x"); code != int(core.CodeOK) {
		t.Fatalf("lint --message %q exited %d, want 0 — the conventional grammar is not the one loaded\nstderr: %s", "feat^: add x", code, stderr)
	}

	const sigilless = "feat: add x"
	t.Run("a sigil-less type is refused at every gate", func(t *testing.T) {
		code, stdout, stderr := runGlyph(t, "lint", "--message", sigilless)
		if code != int(core.CodeLint) || stdout != "" {
			t.Fatalf("lint --message %q = %d stdout %q, want 3 and nothing on stdout\nstderr: %s", sigilless, code, stdout, stderr)
		}
		var details []struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(decodeErrorEnvelope(t, stderr).Details, &details); err != nil {
			t.Fatalf("details: %v", err)
		}
		const form = "write it as <type>[(scope)]<semver_sigil>: <subject>"
		if len(details) != 1 || !strings.Contains(details[0].Detail, form) {
			t.Errorf("details = %+v, want one finding quoting %q", details, form)
		}

		setStdin(t, sigilless+"\n")
		if code, _, stderr := runGlyph(t, "lint", "--stdin"); code != int(core.CodeLint) {
			t.Errorf("lint --stdin %q = %d, want 3 — the commit-msg hook refuses it\nstderr: %s", sigilless, code, stderr)
		}

		testCommit(t, dir, "akira-toriyama", sigilless)
		if code, _, stderr := runGlyph(t, "lint", "--range", "v1.0.0..HEAD"); code != int(core.CodeLint) {
			t.Errorf("lint --range = %d, want 3\nstderr: %s", code, stderr)
		}
		if code, stdout, stderr := runGlyph(t, "bump", "--range", "v1.0.0..HEAD"); code != int(core.CodeLint) || stdout != "" {
			t.Errorf("bump --range = %d %q, want 3 and no version — v1.1.0 would be the type word deciding it\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("a BREAKING CHANGE footer moves nothing", func(t *testing.T) {
		testGit(t, dir, "akira-toriyama", "reset", "-q", "--hard", "v1.0.0")
		testCommit(t, dir, "akira-toriyama", "fix~: repair y\n\nBREAKING CHANGE: y is gone")
		if code, _, stderr := runGlyph(t, "lint", "--range", "v1.0.0..HEAD"); code != int(core.CodeOK) {
			t.Errorf("lint --range = %d, want 0\nstderr: %s", code, stderr)
		}
		if code, stdout, stderr := runGlyph(t, "bump", "--range", "v1.0.0..HEAD"); code != int(core.CodeOK) || stdout != "v1.0.1\n" {
			t.Errorf("bump --range = %d %q, want 0 and v1.0.1 — the subject's ~, not the footer\nstderr: %s", code, stdout, stderr)
		}
	})
}
