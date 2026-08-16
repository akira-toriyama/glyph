package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
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
