package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packagesConfig declares the given package paths on top of minimalConfig.
func packagesConfig(paths ...string) string {
	var b strings.Builder
	b.WriteString(minimalConfig)
	for _, p := range paths {
		b.WriteString("\n[[packages]]\npath = '" + p + "'\n")
	}
	return b.String()
}

// TestPackagePathsAbsentIsAFailure pins the severity: a declared subtree the
// checkout does not have was OBSERVED absent, and it is a finding — every
// commit under the directory the author meant would be attributed elsewhere
// without a word. Fail, and the details name each missing path.
func TestPackagePathsAbsentIsAFailure(t *testing.T) {
	path := configPathWith(t, packagesConfig("haiku", "curry"))
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), "haiku"), 0o750); err != nil {
		t.Fatal(err)
	}
	c := checkPackagePaths(path, nil)
	if c.Status != StatusFail {
		t.Fatalf("%s with curry/ absent = %s (%s), want %s", IDPackagePaths, c.Status, c.Observed, StatusFail)
	}
	if len(c.Details) != 1 || c.Details[0] != "curry" {
		t.Errorf("Details = %q, want exactly the missing path", c.Details)
	}
	if strings.Contains(c.Observed, "haiku") && !strings.Contains(c.Observed, "absent from the checkout: curry") {
		t.Errorf("the observation must name the absent path, not the present one: %q", c.Observed)
	}
	if !strings.Contains(c.Fix, "glyph.toml") {
		t.Errorf("the fix must point at the file, got %q", c.Fix)
	}
}

func TestPackagePathsPresentPasses(t *testing.T) {
	path := configPathWith(t, packagesConfig("haiku", "exporters/prometheus", "."))
	for _, d := range []string{"haiku", "exporters/prometheus"} {
		if err := os.MkdirAll(filepath.Join(filepath.Dir(path), filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	c := checkPackagePaths(path, nil)
	if c.Status != StatusPass {
		t.Fatalf("%s = %s (%s), want %s", IDPackagePaths, c.Status, c.Observed, StatusPass)
	}
	if !strings.Contains(c.Observed, "3 package(s)") {
		t.Errorf("the observation must say what was checked, got %q", c.Observed)
	}
}

// TestPackagePathFileIsAFailure — a path present as a FILE names no subtree.
func TestPackagePathFileIsAFailure(t *testing.T) {
	path := configPathWith(t, packagesConfig("haiku"))
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "haiku"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := checkPackagePaths(path, nil)
	if c.Status != StatusFail || !strings.Contains(c.Observed, "not a directory: haiku") {
		t.Fatalf("%s over a file = %s (%s), want fail naming the file", IDPackagePaths, c.Status, c.Observed)
	}
}

// TestNoPackagesPasses — the single line has nothing to check and says so.
func TestNoPackagesPasses(t *testing.T) {
	c := checkPackagePaths(configPathWith(t, minimalConfig), nil)
	if c.Status != StatusPass || !strings.Contains(c.Observed, "no [[packages]]") {
		t.Fatalf("%s with no packages = %s (%s), want pass", IDPackagePaths, c.Status, c.Observed)
	}
}

// TestPackagePathsDegradeWithTheUnloadedConfig pins independence: a config
// that is missing, unloadable or unresolvable was never read here, so this
// check is unknown — the config check carries the failure, and this one may
// not repeat it as a second finding nor pass over it.
func TestPackagePathsDegradeWithTheUnloadedConfig(t *testing.T) {
	cases := map[string]Check{
		"missing":      checkPackagePaths(filepath.Join(t.TempDir(), "glyph.toml"), nil),
		"unloadable":   checkPackagePaths(configPathWith(t, "schema = 999\n"), nil),
		"no top level": checkPackagePaths("", errors.New("git rev-parse --show-toplevel: not a git repository")),
	}
	for name, c := range cases {
		if c.Status != StatusUnknown {
			t.Errorf("%s: %s = %s (%s), want unknown", name, IDPackagePaths, c.Status, c.Observed)
		}
	}
}
