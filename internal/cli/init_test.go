package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/testutil"
)

// TestInitWritesThePresetVerbatim pins init's whole contract: the file on
// disk is the embedded preset byte for byte (no templating between the
// shipped artifact and the user's tree), and it loads under the loader.
func TestInitWritesThePresetVerbatim(t *testing.T) {
	for _, preset := range config.PresetNames() {
		t.Run(preset, func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, stderr := runGlyph(t, "init", "--"+preset)
			if code != 0 {
				t.Fatalf("init --%s exited %d\nstderr: %s", preset, code, stderr)
			}
			if !strings.Contains(stdout, "glyph.toml") {
				t.Errorf("stdout should name the file written: %q", stdout)
			}
			got, err := os.ReadFile("glyph.toml")
			if err != nil {
				t.Fatalf("read glyph.toml: %v", err)
			}
			want, _ := config.Preset(preset)
			if !bytes.Equal(got, want) {
				t.Errorf("glyph.toml differs from the embedded %s preset", preset)
			}
			if _, err := config.Load(got); err != nil {
				t.Errorf("written glyph.toml does not load: %v", err)
			}
		})
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("glyph.toml", []byte("# mine\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, _, stderr := runGlyph(t, "init", "--gemoji")
	if code != 2 {
		t.Fatalf("init over an existing glyph.toml exited %d, want 2 (usage)\nstderr: %s", code, stderr)
	}
	got, _ := os.ReadFile("glyph.toml")
	if string(got) != "# mine\n" {
		t.Fatalf("refusal must leave the existing file untouched, got %q", got)
	}

	code, _, stderr = runGlyph(t, "init", "--gemoji", "--force")
	if code != 0 {
		t.Fatalf("init --force exited %d\nstderr: %s", code, stderr)
	}
	got, _ = os.ReadFile("glyph.toml")
	want, _ := config.Preset("gemoji")
	if !bytes.Equal(got, want) {
		t.Errorf("--force should have replaced the file with the preset")
	}
}

func TestInitFlagGrammar(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runGlyph(t, "init")
	if code != 2 {
		t.Fatalf("bare init exited %d, want 2 (a preset is required)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--conventional") || !strings.Contains(stderr, "--gemoji") {
		t.Errorf("the usage error should name every preset: %q", stderr)
	}
	code, _, _ = runGlyph(t, "init", "--gemoji", "--conventional")
	if code != 2 {
		t.Fatalf("two presets at once exited %d, want 2", code)
	}
	if _, err := os.Lstat("glyph.toml"); !os.IsNotExist(err) {
		t.Errorf("a usage error must not leave a glyph.toml behind")
	}
}

// TestInitWritesTheTopLevelFromASubdirectory: every other command reads the
// glyph.toml at the checkout's top level (loadConfig), so init writes it
// there from wherever in the checkout it runs, and the remedy a missing file
// prints, followed from the same subdirectory, produces the file the next
// command reads. Measured before (2026-09-29, 2026-10-04): from sub/ init
// wrote sub/glyph.toml at exit 0, nothing read it, and lint repeated the
// remedy — and with glyph.toml already at the top level, an init from a
// subdirectory wrote a second one there at exit 0.
func TestInitWritesTheTopLevelFromASubdirectory(t *testing.T) {
	testutil.GitOrSkip(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	top, err := filepath.EvalSymlinks(dir) // git names the real path
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(top, "haiku", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	code, _, stderr := runGlyph(t, "lint", "--message", ":bug:~ fix a crash")
	if code != int(core.CodeUsage) || !strings.Contains(stderr, "glyph init --gemoji") {
		t.Fatalf("lint in an uninitialized checkout exited %d, want exactly %d with the init remedy\nstderr: %s", code, core.CodeUsage, stderr)
	}
	code, stdout, stderr := runGlyph(t, "init", "--gemoji")
	if code != 0 {
		t.Fatalf("init --gemoji from a subdirectory exited %d\nstderr: %s", code, stderr)
	}
	if _, err := os.Lstat(filepath.Join(sub, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("init wrote glyph.toml into the subdirectory it ran from — a file no command reads")
	}
	got, err := os.ReadFile(filepath.Join(top, "glyph.toml"))
	if err != nil {
		t.Fatalf("no glyph.toml at the checkout's top level: %v", err)
	}
	if want, _ := config.Preset("gemoji"); !bytes.Equal(got, want) {
		t.Errorf("the top-level glyph.toml differs from the embedded gemoji preset")
	}
	if !strings.Contains(stdout, filepath.Join(top, "glyph.toml")) {
		t.Errorf("stdout = %q, want it to name the path written, which is not the directory init ran from", stdout)
	}
	if code, _, stderr := runGlyph(t, "lint", "--message", ":bug:~ fix a crash"); code != 0 {
		t.Fatalf("lint after following the remedy exited %d, want 0 — the file init writes is the one every command reads\nstderr: %s", code, stderr)
	}

	code, _, stderr = runGlyph(t, "init", "--gemoji")
	if code != int(core.CodeUsage) {
		t.Fatalf("a second init from the subdirectory exited %d, want exactly %d: the top level already holds glyph.toml\nstderr: %s", code, core.CodeUsage, stderr)
	}
	if _, err := os.Lstat(filepath.Join(sub, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("the refused init still left a glyph.toml in the subdirectory")
	}
}

// TestInitInterruptDuringTopLevelReadWritesNothing: init asks git for the top
// level before it writes, and a signal landing while that question is in
// flight is the user's abort — not git saying "no checkout here", the one
// failure that sends the write to the current directory. Read as the latter,
// the run writes ./glyph.toml at exit 0 after the Ctrl-C: from a
// subdirectory, the file nothing reads that init was fixed to stop writing
// (measured 2026-10-04 with the arm deleted: exit 0 and "wrote glyph.toml",
// where the arm exits 130 with nothing written). The fake git signals that
// the read is in flight, then blocks, and only then is the context cancelled
// — the shape of TestDoctorInterruptDuringTopLevelReadCarriesOut.
func TestInitInterruptDuringTopLevelReadWritesNothing(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	bin := t.TempDir()
	asked := filepath.Join(bin, "asked")
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\ntouch " + asked + "\nexec sleep 30 </dev/null >/dev/null 2>&1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for range 400 {
			if _, err := os.Stat(asked); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel() // safety net: never leave the run behind the fake's 30s block
	}()

	code, stdout, _ := runGlyphCtx(t, ctx, "init", "--gemoji")
	if _, err := os.Stat(asked); err != nil {
		t.Fatalf("the fake git was never asked for the top level, so the run proves nothing: %v", err)
	}
	if code != int(core.CodeInterrupted) {
		t.Errorf("init exited %d, want exactly %d — an interrupt in the top-level read is the user's own abort, not \"outside a checkout\"", code, core.CodeInterrupted)
	}
	if _, err := os.Lstat(filepath.Join(cwd, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("an interrupted init wrote glyph.toml into the directory it ran from (stdout %q)", stdout)
	}
}
