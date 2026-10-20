package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// minimalConfig is the smallest glyph.toml the loader accepts — one pattern
// that captures the sigil. Deliberately not a preset: this package asserts the
// check's verdicts, not the preset's content, and internal/config's own tests
// hold the presets loadable.
const minimalConfig = "schema = 1\n\n[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!%]) (?P<subject>.+)'\n"

// configPathWith writes content as a throwaway glyph.toml at the top level of
// a throwaway checkout and returns its path.
func configPathWith(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "glyph.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write glyph.toml: %v", err)
	}
	return path
}

// loadedAt loads the glyph.toml at path exactly as internal/cli does once per
// doctor run, and returns the checkout's top level with the loader's answer —
// the three Input fields the config check classifies.
func loadedAt(path string) (root string, cfg *config.Config, err error) {
	cfg, err = config.LoadFile(path)
	return filepath.Dir(path), cfg, err
}

// loaded is the glyph.toml content as the checks that read the loaded file
// receive it; content that does not load is the test's own mistake.
func loaded(t *testing.T, content string) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(content))
	if err != nil {
		t.Fatalf("the fixture config does not load: %v", err)
	}
	return cfg
}

func TestConfigLoadsPasses(t *testing.T) {
	root, cfg, err := loadedAt(configPathWith(t, minimalConfig))
	c := checkConfig(root, nil, cfg, err)
	if c.Status != StatusPass {
		t.Fatalf("%s = %s (%s), want %s", idConfigLoads, c.Status, c.Observed, StatusPass)
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
	root, cfg, err := loadedAt(filepath.Join(t.TempDir(), "glyph.toml"))
	c := checkConfig(root, nil, cfg, err)
	if c.Status != StatusFail {
		t.Fatalf("%s on a missing glyph.toml = %s, want %s", idConfigLoads, c.Status, StatusFail)
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
			root, cfg, err := loadedAt(configPathWith(t, content))
			c := checkConfig(root, nil, cfg, err)
			if c.Status != StatusFail {
				t.Fatalf("%s = %s (%s), want %s", idConfigLoads, c.Status, c.Observed, StatusFail)
			}
			if !strings.Contains(c.Observed, "does not load") {
				t.Errorf("the observation must carry the loader's verdict, got %q", c.Observed)
			}
		})
	}
}

// TestConfigNotARegularFileIsAFailure: a glyph.toml path holding a directory
// or a named pipe was OBSERVED — the loader stats it and never opens it — so
// it is the missing file's failure with the missing file's remedy behind a
// removal, never the unreadable arm. That arm graded a directory unknown,
// quoted its path twice and told the reader to fix the file permissions; a
// named pipe never got that far, hanging doctor in the read (t-zvax, measured
// at ec53ed3). The load runs under a deadline so that regression fails here
// instead of hanging the package.
func TestConfigNotARegularFileIsAFailure(t *testing.T) {
	for _, c := range []struct {
		kind  string
		stage func(path string) error
	}{
		{"a directory", func(p string) error { return os.Mkdir(p, 0o700) }},
		{"a named pipe", func(p string) error { return syscall.Mkfifo(p, 0o600) }},
	} {
		t.Run(c.kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "glyph.toml")
			if err := c.stage(path); err != nil {
				t.Fatalf("staging %s at %s: %v", c.kind, path, err)
			}
			type answer struct {
				root string
				cfg  *config.Config
				err  error
			}
			done := make(chan answer, 1)
			go func() {
				root, cfg, err := loadedAt(path)
				done <- answer{root, cfg, err}
			}()
			var a answer
			select {
			case a = <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("loading %s as glyph.toml has not returned after 10s — doctor hangs there", c.kind)
			}
			ch := checkConfig(a.root, nil, a.cfg, a.err)
			if ch.Status != StatusFail {
				t.Fatalf("%s on %s = %s (%s), want %s — what is at the path was observed", idConfigLoads, c.kind, ch.Status, ch.Observed, StatusFail)
			}
			if want := "glyph.toml at " + a.root + " is " + c.kind; !strings.Contains(ch.Observed, want) {
				t.Errorf("the observation must name what is there (%q), got %q", want, ch.Observed)
			}
			if !strings.Contains(ch.Fix, "remove it") || !strings.Contains(ch.Fix, "glyph init") {
				t.Errorf("the fix must remove what is there and hand over the init command, got %q", ch.Fix)
			}
		})
	}
}

// TestConfigUnresolvableTopLevelIsCouldNotRun: with no top level to resolve
// the file's location does not exist to check — unverified, never a verdict,
// exactly like every other unreadable input in the report.
func TestConfigUnresolvableTopLevelIsCouldNotRun(t *testing.T) {
	c := checkConfig(".", errors.New("git rev-parse --show-toplevel: not a git repository"), nil, nil)
	if c.Status != StatusUnknown {
		t.Fatalf("%s = %s, want %s — nothing was observed", idConfigLoads, c.Status, StatusUnknown)
	}
	if !strings.Contains(c.Observed, "not a git repository") {
		t.Errorf("the observation must carry git's own reason — the checks that read the loaded file point here for it, got %q", c.Observed)
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
	root, cfg, err := loadedAt(path)
	c := checkConfig(root, nil, cfg, err)
	if c.Status != StatusUnknown {
		t.Fatalf("%s on an unreadable glyph.toml = %s, want %s", idConfigLoads, c.Status, StatusUnknown)
	}
}
