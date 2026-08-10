package hook

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// The hooks' whole reason to exist is that they hold no copy of the convention.
// If a gitmoji, a scope regex, or a Conventional type word ever appears in a
// script, the drift this command was built to end has been reintroduced.
//
// Table-driven over Kinds so a third hook cannot be added without declaring what
// it must and must not contain — the previous shape named one script by hand,
// which is how a second one would have shipped unchecked.
func TestEveryKindsScriptCarriesNoCopyOfTheConvention(t *testing.T) {
	required := map[string][]string{
		// commit-msg is handed the message FILE as $1 and must read it.
		"commit-msg": {"glyph lint --stdin", `<"$1"`},
		// pre-push gets git's protocol on inherited stdin and forwards argv
		// verbatim — a named flag here would freeze git's argv shape into every
		// installed copy, and nothing refreshes one.
		"pre-push": {"glyph hook pre-push", `-- "$@"`},
	}
	// The range arithmetic must live in the binary. A script that computed it
	// would go on computing the OLD thing in every clone it was installed into,
	// and a wrong range is a wrong verdict rather than a loud failure.
	bannedPerKind := map[string][]string{
		"pre-push": {"rev-list", "rev-parse", "git log", "--not"},
	}

	for _, k := range Kinds {
		t.Run(k.Name, func(t *testing.T) {
			for _, banned := range append([]string{":sparkles:", ":bug:", "grep", "[a-z0-9]", "feat", "BREAKING CHANGE"}, bannedPerKind[k.Name]...) {
				if strings.Contains(k.Script, banned) {
					t.Errorf("%s script contains %q — the rules and the arithmetic must stay in the binary "+
						"(a local copy is exactly what fell out of lockstep in the repos this replaces)", k.Name, banned)
				}
			}
			for _, want := range required[k.Name] {
				if !strings.Contains(k.Script, want) {
					t.Errorf("%s script is missing %q", k.Name, want)
				}
			}
			// A missing glyph must not block the developer.
			if !strings.Contains(k.Script, "exit 0") {
				t.Errorf("%s script has no pass-through for a missing glyph; a developer without "+
					"glyph on PATH would be blocked (CI is the authority, the hook is early warning)", k.Name)
			}
			// If a script ever goes back to `exec glyph`, every glyph malfunction —
			// a missing source clone behind the PATH wrapper, a broken build, a
			// renamed flag — becomes a hard block in repos with no other local gate.
			if strings.Contains(k.Script, "exec glyph") {
				t.Errorf("%s script execs glyph directly, so ANY non-zero exit blocks; it must "+
					"distinguish the convention-violation exit from glyph being unable to answer", k.Name)
			}
			// Asserted THROUGH the constant, never against the literal 3. Pinning
			// the text was the same defect the script itself had: renumbering
			// core.CodeLint would leave a hook comparing against a code glyph no
			// longer emits, and a test pinned to "-eq 3" would keep passing.
			if want := fmt.Sprintf("-eq %d", int(core.CodeLint)); !strings.Contains(k.Script, want) {
				t.Errorf("%s script does not single out glyph's lint gate code (%q); without that check it "+
					"cannot tell a real violation from an unwell toolchain", k.Name, want)
			}
		})
	}
}

// The exit behaviour is the whole contract of every hook here, so drive each
// REAL script with a stub `glyph` that returns each interesting code.
func TestEveryKindsScriptExitsOnlyOnAViolation(t *testing.T) {
	tests := []struct {
		name       string
		glyphExit  int
		wantExit   int
		wantWarned bool
	}{
		// Named through the core constants, so a renumbering moves the fixture
		// and the script together instead of leaving the table asserting a
		// contract the binary no longer has.
		{name: "clean passes", glyphExit: int(core.CodeOK), wantExit: 0},
		{name: "violation blocks", glyphExit: int(core.CodeLint), wantExit: int(core.CodeLint)},
		{name: "usage error passes with a warning", glyphExit: int(core.CodeUsage), wantExit: 0, wantWarned: true},
		{name: "IO/API failure passes with a warning", glyphExit: int(core.CodeAPI), wantExit: 0, wantWarned: true},
		{name: "wrapper failure passes with a warning", glyphExit: int(core.CodeNoRelease), wantExit: 0, wantWarned: true},
	}
	for _, k := range Kinds {
		for _, tt := range tests {
			t.Run(k.Name+"/"+tt.name, func(t *testing.T) {
				dir := t.TempDir()
				hookPath := filepath.Join(dir, k.Name)
				if err := os.WriteFile(hookPath, []byte(k.Script), 0o755); err != nil { //nolint:gosec
					t.Fatalf("writing the hook: %v", err)
				}
				msg := filepath.Join(dir, "MSG")
				if err := os.WriteFile(msg, []byte(":sparkles: a subject\n"), 0o600); err != nil {
					t.Fatalf("writing the message: %v", err)
				}

				// A stub glyph earlier on PATH than any real one.
				stubDir := t.TempDir()
				stub := fmt.Sprintf("#!/bin/sh\nexit %d\n", tt.glyphExit)
				if err := os.WriteFile(filepath.Join(stubDir, "glyph"), []byte(stub), 0o755); err != nil { //nolint:gosec
					t.Fatalf("writing the stub: %v", err)
				}

				args := []string{hookPath, msg}
				if k.Name == "pre-push" {
					args = []string{hookPath, "origin", "https://example.invalid/x.git"}
				}
				cmd := exec.Command("/bin/sh", args...) // #nosec G204 -- fixture paths
				cmd.Env = append(os.Environ(), "PATH="+stubDir)
				cmd.Stdin = strings.NewReader("")
				out, err := cmd.CombinedOutput()

				got := 0
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					got = ee.ExitCode()
				} else if err != nil {
					t.Fatalf("running the hook: %v\n%s", err, out)
				}
				if got != tt.wantExit {
					t.Errorf("hook exit = %d, want %d (glyph exited %d)\n%s", got, tt.wantExit, tt.glyphExit, out)
				}
				if warned := strings.Contains(string(out), "could not lint"); warned != tt.wantWarned {
					t.Errorf("warned = %v, want %v\n%s", warned, tt.wantWarned, out)
				}
			})
		}
	}
}

func TestInstallWritesExecutableHook(t *testing.T) {
	dir := t.TempDir()

	res, err := Install(dir, false, Kinds[0])
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Hooks[0].Action != "installed" {
		t.Errorf("Action = %q, want %q", res.Hooks[0].Action, "installed")
	}
	if res.Hooks[0].Existed {
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

	if _, err := Install(dir, false, Kinds[0]); err != nil {
		t.Fatalf("Install into a missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commit-msg")); err != nil {
		t.Fatalf("hook was not created: %v", err)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := Install(dir, false, Kinds[0]); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	res, err := Install(dir, false, Kinds[0])
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res.Hooks[0].Action != "unchanged" {
		t.Errorf("Action = %q on re-install, want %q", res.Hooks[0].Action, "unchanged")
	}
}

// An older glyph-written hook is ours to rewrite: that is how a repo picks up a
// new script without the developer having to reason about --force.
func TestInstallRefreshesAnOlderGlyphHook(t *testing.T) {
	dir := t.TempDir()
	stale := "#!/bin/sh\n# glyph commit-msg hook — " + Marker + "\nexec glyph lint --old-flag\n"
	writeHook(t, dir, stale)

	res, err := Install(dir, false, Kinds[0])
	if err != nil {
		t.Fatalf("Install over a glyph-written hook: %v", err)
	}
	if res.Hooks[0].Action != "refreshed" {
		t.Errorf("Action = %q, want %q", res.Hooks[0].Action, "refreshed")
	}
	if !res.Hooks[0].Existed {
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

	res, err := Install(dir, false, Kinds[0])
	if err == nil {
		t.Fatal("Install overwrote a foreign hook without --force")
	}
	if code := core.ExitCode(err); code != int(core.CodeUsage) {
		t.Errorf("exit code = %d, want %d (usage — the caller fixes it with a flag)", code, core.CodeUsage)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal does not name the escape hatch: %v", err)
	}
	if res.Hooks[0].Existed != true {
		t.Error("Existed = false for a refused overwrite")
	}
	if got := readHook(t, dir); got != foreign {
		t.Error("foreign hook was modified despite the refusal")
	}
}

func TestInstallForceReplacesAForeignHook(t *testing.T) {
	dir := t.TempDir()
	writeHook(t, dir, "#!/bin/sh\n# hand-written house rules\nexit 0\n")

	res, err := Install(dir, true, Kinds[0])
	if err != nil {
		t.Fatalf("Install --force: %v", err)
	}
	if res.Hooks[0].Action != "refreshed" {
		t.Errorf("Action = %q, want %q", res.Hooks[0].Action, "refreshed")
	}
	if got := readHook(t, dir); got != Script {
		t.Error("--force did not replace the foreign hook")
	}
}

// A hook that lost its execute bit is a hook git ignores, so re-running install
// must restore the mode even when the content is already current.
func TestInstallRestoresTheExecuteBitOnAnUnchangedHook(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, false, Kinds[0]); err != nil {
		t.Fatalf("Install: %v", err)
	}
	path := filepath.Join(dir, "commit-msg")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res, err := Install(dir, false, Kinds[0])
	if err != nil {
		t.Fatalf("re-Install: %v", err)
	}
	if res.Hooks[0].Action != "unchanged" {
		t.Errorf("Action = %q, want %q", res.Hooks[0].Action, "unchanged")
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
