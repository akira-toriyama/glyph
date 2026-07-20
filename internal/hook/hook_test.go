package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// The hook's whole reason to exist is that it holds no copy of the convention.
// If a gitmoji, a scope regex, or a Conventional type word ever appears in the
// script, the drift this command was built to end has been reintroduced.
func TestScriptCarriesNoCopyOfTheConvention(t *testing.T) {
	for _, banned := range []string{":sparkles:", ":bug:", "grep", "[a-z0-9]", "feat", "BREAKING CHANGE"} {
		if strings.Contains(Script, banned) {
			t.Errorf("hook script contains %q — the rules must stay in the binary "+
				"(a local copy is exactly what fell out of lockstep in the repos this replaces)", banned)
		}
	}
	// It must actually delegate, and read the message file git passes as $1.
	for _, want := range []string{"glyph lint --stdin", `<"$1"`} {
		if !strings.Contains(Script, want) {
			t.Errorf("hook script is missing %q", want)
		}
	}
	// A missing glyph must not block the commit.
	if !strings.Contains(Script, "exit 0") {
		t.Error("hook script has no pass-through for a missing glyph; a developer without " +
			"glyph on PATH would be unable to commit (CI is the authority, the hook is early warning)")
	}
}

func TestInstallWritesExecutableHook(t *testing.T) {
	dir := t.TempDir()

	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Action != "installed" {
		t.Errorf("Action = %q, want %q", res.Action, "installed")
	}
	if res.Existed {
		t.Error("Existed = true for a fresh install")
	}

	got, err := os.ReadFile(filepath.Join(dir, "commit-msg"))
	if err != nil {
		t.Fatalf("reading the installed hook: %v", err)
	}
	if string(got) != Script {
		t.Error("installed hook does not match Script")
	}
	info, err := os.Stat(filepath.Join(dir, "commit-msg"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// git silently ignores a hook without the execute bit — the failure mode is
	// "lint never runs and nobody notices", so pin the mode.
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook mode = %v, want the execute bit set (git skips non-executable hooks)", info.Mode().Perm())
	}
}

// A hooks directory that does not exist yet is the normal case for a repo whose
// core.hooksPath points at a tracked directory in a fresh clone.
func TestInstallCreatesMissingHooksDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts", "hooks")

	if _, err := Install(dir, false); err != nil {
		t.Fatalf("Install into a missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commit-msg")); err != nil {
		t.Fatalf("hook was not created: %v", err)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := Install(dir, false); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res.Action != "unchanged" {
		t.Errorf("Action = %q on re-install, want %q", res.Action, "unchanged")
	}
}

// An older glyph-written hook is ours to rewrite: that is how a repo picks up a
// new script without the developer having to reason about --force.
func TestInstallRefreshesAnOlderGlyphHook(t *testing.T) {
	dir := t.TempDir()
	stale := "#!/bin/sh\n# glyph commit-msg hook — " + Marker + "\nexec glyph lint --old-flag\n"
	writeHook(t, dir, stale)

	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("Install over a glyph-written hook: %v", err)
	}
	if res.Action != "refreshed" {
		t.Errorf("Action = %q, want %q", res.Action, "refreshed")
	}
	if !res.Existed {
		t.Error("Existed = false when replacing an existing hook")
	}
	if got := readHook(t, dir); got != Script {
		t.Error("stale glyph hook was not rewritten to the current Script")
	}
}

// The repos this targets track a real commit-msg hook in git. Clobbering one
// unasked would stage a content change the developer never requested, so a
// foreign hook is a refusal — and specifically a usage refusal, since the fix
// is to re-run with a different flag.
func TestInstallRefusesAForeignHook(t *testing.T) {
	dir := t.TempDir()
	foreign := "#!/bin/sh\n# hand-written house rules\nexit 0\n"
	writeHook(t, dir, foreign)

	res, err := Install(dir, false)
	if err == nil {
		t.Fatal("Install overwrote a foreign hook without --force")
	}
	if code := core.ExitCode(err); code != int(core.CodeUsage) {
		t.Errorf("exit code = %d, want %d (usage — the caller fixes it with a flag)", code, core.CodeUsage)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not name the escape hatch: %v", err)
	}
	if res.Existed != true {
		t.Error("Existed = false for a refused overwrite")
	}
	if got := readHook(t, dir); got != foreign {
		t.Error("foreign hook was modified despite the refusal")
	}
}

func TestInstallForceReplacesAForeignHook(t *testing.T) {
	dir := t.TempDir()
	writeHook(t, dir, "#!/bin/sh\n# hand-written house rules\nexit 0\n")

	res, err := Install(dir, true)
	if err != nil {
		t.Fatalf("Install --force: %v", err)
	}
	if res.Action != "refreshed" {
		t.Errorf("Action = %q, want %q", res.Action, "refreshed")
	}
	if got := readHook(t, dir); got != Script {
		t.Error("--force did not replace the foreign hook")
	}
}

// A hook that lost its execute bit is a hook git ignores, so re-running install
// must restore the mode even when the content is already current.
func TestInstallRestoresTheExecuteBitOnAnUnchangedHook(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	path := filepath.Join(dir, "commit-msg")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res, err := Install(dir, false)
	if err != nil {
		t.Fatalf("re-Install: %v", err)
	}
	if res.Action != "unchanged" {
		t.Errorf("Action = %q, want %q", res.Action, "unchanged")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("re-install left the hook non-executable; git would silently skip it")
	}
}

func writeHook(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "commit-msg"), []byte(content), 0o755); err != nil { //nolint:gosec
		t.Fatalf("seeding the hook: %v", err)
	}
}

func readHook(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "commit-msg"))
	if err != nil {
		t.Fatalf("reading the hook: %v", err)
	}
	return string(b)
}
