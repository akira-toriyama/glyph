package bump

import (
	"testing"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// TestParseVersion accepts exactly the house tag shape — vX.Y.Z with an
// optional leading v — and nothing looser (no pre-release, no leading zeros).
func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"v1.2.3", Version{1, 2, 3}},
		{"1.2.3", Version{1, 2, 3}},
		{"v0.0.0", Version{0, 0, 0}},
		{"v0.6.1", Version{0, 6, 1}},
		{"v10.20.30", Version{10, 20, 30}},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if err != nil {
			t.Fatalf("ParseVersion(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestParseVersionRejects: anything that is not a plain semver triple errors —
// the caller decides the exit-code class from where the string came from.
func TestParseVersionRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"v1",
		"v1.2",
		"v1.2.3.4",
		"1.2.x",
		"v01.2.3",
		"v1.02.3",
		"v1.2.03",
		"v-1.2.3",
		"v1.2.3-rc1",
		"v1.2.3+meta",
		"V1.2.3",
		" v1.2.3",
		"v1.2.3 ",
	} {
		if got, err := ParseVersion(in); err == nil {
			t.Fatalf("ParseVersion(%q) should fail, got %+v", in, got)
		}
	}
}

// TestVersionString: versions render with the house v prefix regardless of the
// input form.
func TestVersionString(t *testing.T) {
	v, err := ParseVersion("1.2.3")
	if err != nil {
		t.Fatalf("ParseVersion(1.2.3): %v", err)
	}
	if got := v.String(); got != "v1.2.3" {
		t.Fatalf("String() = %q, want %q", got, "v1.2.3")
	}
}

// TestNext pins the stepping rule, including that none holds the version still
// and that a 0.x major steps to 1.0.0 (plain semver — the shadow-mode phase
// compares this against git-cliff before any publish relies on it).
func TestNext(t *testing.T) {
	cases := []struct {
		in    string
		level gitmoji.Bump
		want  string
	}{
		{"v1.2.3", gitmoji.BumpNone, "v1.2.3"},
		{"v1.2.3", gitmoji.BumpPatch, "v1.2.4"},
		{"v1.2.3", gitmoji.BumpMinor, "v1.3.0"},
		{"v1.2.3", gitmoji.BumpMajor, "v2.0.0"},
		{"v0.6.1", gitmoji.BumpPatch, "v0.6.2"},
		{"v0.6.1", gitmoji.BumpMinor, "v0.7.0"},
		{"v0.6.1", gitmoji.BumpMajor, "v1.0.0"},
		{"v0.0.0", gitmoji.BumpPatch, "v0.0.1"},
	}
	for _, c := range cases {
		v, err := ParseVersion(c.in)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.in, err)
		}
		if got := v.Next(c.level).String(); got != c.want {
			t.Fatalf("%s.Next(%s) = %s, want %s", c.in, c.level, got, c.want)
		}
	}
}

// FuzzParseVersion: never panics; an accepted string round-trips through
// String back to the identical Version.
func FuzzParseVersion(f *testing.F) {
	f.Add("v1.2.3")
	f.Add("0.0.0")
	f.Add("v01.2.3")
	f.Add("not a version")
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseVersion(s)
		if err != nil {
			return
		}
		back, err := ParseVersion(v.String())
		if err != nil {
			t.Fatalf("ParseVersion(%q).String() = %q does not re-parse: %v", s, v.String(), err)
		}
		if back != v {
			t.Fatalf("round trip changed %q: %+v -> %+v", s, v, back)
		}
		if v.Major < 0 || v.Minor < 0 || v.Patch < 0 {
			t.Fatalf("ParseVersion(%q) accepted a negative component: %+v", s, v)
		}
	})
}

// TestVersionCompare backs the published-floor guard: the next version must be
// STRICTLY greater than the latest published release, so equality is not
// greater — a deleted published release's tag is permanently burned and can
// never be re-published.
func TestVersionCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.10", "v1.2.9", 1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v0.9.9", "v1.0.0", -1},
	} {
		a, err := ParseVersion(c.a)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.a, err)
		}
		b, err := ParseVersion(c.b)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.b, err)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// FuzzVersionNext: for any version and any version-moving level, Next is
// strictly increasing — the invariant the published-floor guard rests on
// (a computed next that could equal or regress below its base would deadlock
// or burn a tag). none holds the version still, exactly.
func FuzzVersionNext(f *testing.F) {
	f.Add(0, 1, 0, "minor")
	f.Add(1, 2, 3, "major")
	f.Add(0, 0, 0, "patch")
	f.Add(4, 5, 6, "none")
	f.Fuzz(func(t *testing.T, major, minor, patch int, level string) {
		// Clamp to the production-reachable shape: ParseVersion only ever
		// yields non-negative components.
		if major < 0 || minor < 0 || patch < 0 {
			t.Skip("unreachable: parsed versions are non-negative")
		}
		b := gitmoji.Bump(level)
		if !b.Valid() {
			t.Skip("out-of-lattice level")
		}
		v := Version{Major: major, Minor: minor, Patch: patch}
		next := v.Next(b)
		if b == gitmoji.BumpNone {
			if next != v {
				t.Fatalf("Next(none) moved %v to %v", v, next)
			}
			return
		}
		if next.Compare(v) <= 0 {
			t.Fatalf("Next(%s) did not increase: %v -> %v", b, v, next)
		}
	})
}
