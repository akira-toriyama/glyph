package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalConfig is the smallest glyph.toml the loader accepts — one pattern
// that captures the sigil. Deliberately not a preset: this package asserts the
// check's verdicts, not the preset's content, and internal/config's own tests
// hold the presets loadable.
const minimalConfig = "schema = 1\n\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]) (?P<subject>.+)'\n"

// configPathWith writes content as a throwaway glyph.toml and returns its
// path — the resolved location Input.ConfigPath carries.
func configPathWith(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "glyph.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write glyph.toml: %v", err)
	}
	return path
}

func TestConfigLoadsPasses(t *testing.T) {
	c := checkConfig(configPathWith(t, minimalConfig), nil)
	if c.Status != StatusPass {
		t.Fatalf("%s = %s (%s), want %s", IDConfigLoads, c.Status, c.Observed, StatusPass)
	}
	if !strings.Contains(c.Observed, "schema 1") || !strings.Contains(c.Observed, "1 pattern(s)") {
		t.Errorf("the observation must say what loaded, got %q", c.Observed)
	}
}

// TestConfigMissingIsAFailureNotUnknown pins the severity this check was added
// for: an absent glyph.toml was OBSERVED absent, and it is a finding about the
// repository — every verdict command refuses to run without the file, so a
// repository that moves its pin first has its whole gate down. Fail (the
// CLI's exit 3), never unknown (nothing was unobservable) and never advice
// (nothing about the gate being down is optional).
func TestConfigMissingIsAFailureNotUnknown(t *testing.T) {
	c := checkConfig(filepath.Join(t.TempDir(), "glyph.toml"), nil)
	if c.Status != StatusFail {
		t.Fatalf("%s on a missing glyph.toml = %s, want %s", IDConfigLoads, c.Status, StatusFail)
	}
	if !strings.Contains(c.Fix, "glyph init") {
		t.Errorf("the fix must hand over the init command, got %q", c.Fix)
	}
}

// TestConfigUnloadableIsAFailure holds the second failing shape: a file that
// exists but violates its contract is the same class as a missing one — the
// loader rejects rather than repairs, and so does every verdict command.
func TestConfigUnloadableIsAFailure(t *testing.T) {
	for name, content := range map[string]string{
		"unsupported schema": "schema = 999\n\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]) '\n",
		"sigil-less pattern": "schema = 1\n\n[[patterns]]\npattern = '^x'\n",
		"not toml at all":    "{\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := checkConfig(configPathWith(t, content), nil)
			if c.Status != StatusFail {
				t.Fatalf("%s = %s (%s), want %s", IDConfigLoads, c.Status, c.Observed, StatusFail)
			}
			if !strings.Contains(c.Observed, "does not load") {
				t.Errorf("the observation must carry the loader's verdict, got %q", c.Observed)
			}
		})
	}
}

// TestConfigUnresolvableTopLevelIsCouldNotRun: with no top level to resolve
// the file's location does not exist to check — unverified, never a verdict,
// exactly like every other unreadable input in the report.
func TestConfigUnresolvableTopLevelIsCouldNotRun(t *testing.T) {
	c := checkConfig("", errors.New("git rev-parse --show-toplevel: not a git repository"))
	if c.Status != StatusUnknown {
		t.Fatalf("%s = %s, want %s — nothing was observed", IDConfigLoads, c.Status, StatusUnknown)
	}
}

// TestConfigUnreadableIsCouldNotRunNotAFailure draws the read/parse line: a
// file whose CONTENT was never observed is unverified, not broken — the same
// distinction the token check draws between "the API answered no" and "the
// API never answered".
func TestConfigUnreadableIsCouldNotRunNotAFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, where an unreadable file does not exist")
	}
	path := configPathWith(t, minimalConfig)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	c := checkConfig(path, nil)
	if c.Status != StatusUnknown {
		t.Fatalf("%s on an unreadable glyph.toml = %s, want %s", IDConfigLoads, c.Status, StatusUnknown)
	}
}
