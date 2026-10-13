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

// TestPackagePathsAbsentIsAFailure pins the severity: a declared subtree HEAD
// does not record was OBSERVED absent, and it is a finding — every commit under
// the directory the author meant would be attributed elsewhere without a word.
// Fail, and the details name each missing path.
func TestPackagePathsAbsentIsAFailure(t *testing.T) {
	c := checkPackagePaths(t.TempDir(), loaded(t, packagesConfig("haiku", "curry")), []string{"haiku"}, nil)
	if c.Status != StatusFail {
		t.Fatalf("%s with curry/ absent = %s (%s), want %s", IDPackagePaths, c.Status, c.Observed, StatusFail)
	}
	if len(c.Details) != 1 || !strings.HasPrefix(c.Details[0], "curry") {
		t.Errorf("Details = %q, want exactly the missing path", c.Details)
	}
	if strings.Contains(c.Observed, "haiku") {
		t.Errorf("the observation must name the absent path, not the present one: %q", c.Observed)
	}
	if !strings.Contains(c.Fix, "glyph.toml") {
		t.Errorf("the fix must point at the file, got %q", c.Fix)
	}
}

func TestPackagePathsPresentPasses(t *testing.T) {
	c := checkPackagePaths(t.TempDir(), loaded(t, packagesConfig("haiku", "exporters/prometheus", ".")), []string{"exporters", "exporters/prometheus", "haiku"}, nil)
	if c.Status != StatusPass {
		t.Fatalf("%s = %s (%s), want %s", IDPackagePaths, c.Status, c.Observed, StatusPass)
	}
	if !strings.Contains(c.Observed, "3 package(s)") {
		t.Errorf("the observation must say what was checked, got %q", c.Observed)
	}
}

// TestPackagePathFileIsAFailure — a path present as a FILE names no subtree.
func TestPackagePathFileIsAFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "haiku"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := checkPackagePaths(root, loaded(t, packagesConfig("haiku")), nil, nil)
	if c.Status != StatusFail || !strings.Contains(strings.Join(c.Details, "\n"), "haiku — a file") {
		t.Fatalf("%s over a file = %s (%s, %q), want fail naming the file", IDPackagePaths, c.Status, c.Observed, c.Details)
	}
}

// TestPackagePathsAskGitNotTheFilesystem pins t-fdd8 (1). The check stat'ed
// each path, and the filesystem answers a different question than the one
// attribution asks: on a case-insensitive volume (APFS, measured) `Haiku`
// resolves to haiku/, and a symlink `currylink` -> haiku resolves to a
// directory while git records it as a 120000 blob — both passed, while
// `lint --range` under the same config exited 3 (its files belong to no
// declared package) on a commit under haiku/. Attribution reads git's path strings, so the
// check asks HEAD's trees, byte for byte. The on-disk layout below is real,
// so a Stat-based check passes the symlink on every OS and `Haiku` on macOS.
func TestPackagePathsAskGitNotTheFilesystem(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "haiku"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("haiku", filepath.Join(root, "currylink")); err != nil {
		t.Fatal(err)
	}
	trees := []string{"haiku"} // what `git ls-tree -r -d HEAD` lists for that layout

	// Positive control, over the same layout: the declaration git does record
	// passes.
	if c := checkPackagePaths(root, loaded(t, packagesConfig("haiku")), trees, nil); c.Status != StatusPass {
		t.Fatalf("haiku against HEAD's haiku: status = %s (%s), want %s", c.Status, c.Observed, StatusPass)
	}

	c := checkPackagePaths(root, loaded(t, packagesConfig("Haiku", "currylink")), trees, nil)
	if c.Status != StatusFail {
		t.Fatalf("Haiku and a symlink against HEAD's trees: status = %s (%s), want %s", c.Status, c.Observed, StatusFail)
	}
	details := strings.Join(c.Details, "\n")
	for _, want := range []string{"Haiku — HEAD tracks haiku", "currylink — a symlink"} {
		if !strings.Contains(details, want) {
			t.Errorf("details %q do not say %q — the reader needs to know why a path that opens on disk claims nothing", c.Details, want)
		}
	}
}

// TestPackagePathsWithoutGitsAnswerAreUnknown: with packages declared and no
// tree listing (an unborn HEAD, a git that failed) nothing was compared, so
// the check is unverified — while a repository declaring no packages needs no
// listing and still passes.
func TestPackagePathsWithoutGitsAnswerAreUnknown(t *testing.T) {
	noTree := errors.New("git ls-tree: fatal: Not a valid object name HEAD")
	if c := checkPackagePaths(t.TempDir(), loaded(t, packagesConfig("haiku")), nil, noTree); c.Status != StatusUnknown {
		t.Errorf("packages declared, no tree listing: status = %s (%s), want %s", c.Status, c.Observed, StatusUnknown)
	}
	if c := checkPackagePaths(t.TempDir(), loaded(t, minimalConfig), nil, noTree); c.Status != StatusPass {
		t.Errorf("no packages, no tree listing: status = %s (%s), want %s", c.Status, c.Observed, StatusPass)
	}
}

// TestNoPackagesPasses — the single line has nothing to check and says so.
func TestNoPackagesPasses(t *testing.T) {
	c := checkPackagePaths(t.TempDir(), loaded(t, minimalConfig), nil, nil)
	if c.Status != StatusPass || !strings.Contains(c.Observed, "no [[packages]]") {
		t.Fatalf("%s with no packages = %s (%s), want pass", IDPackagePaths, c.Status, c.Observed)
	}
}

// TestPackagePathsDegradeWithTheUnloadedConfig pins independence: a config
// that is missing, unloadable or unresolvable was never read here, so this
// check is unknown — the config check carries the failure, and this one may
// not repeat it as a second finding nor pass over it. The caller loads the
// file once, so every one of those reasons reaches this check as the same
// nil, and the observation points at the check that names the reason.
func TestPackagePathsDegradeWithTheUnloadedConfig(t *testing.T) {
	c := checkPackagePaths(".", nil, []string{"haiku"}, nil)
	if c.Status != StatusUnknown {
		t.Fatalf("%s = %s (%s), want unknown", IDPackagePaths, c.Status, c.Observed)
	}
	if !strings.Contains(c.Observed, IDConfigLoads) || !strings.Contains(c.Fix, IDConfigLoads) {
		t.Errorf("the observation and the fix must point at %s, which owns the reason: %q / %q", IDConfigLoads, c.Observed, c.Fix)
	}
}
