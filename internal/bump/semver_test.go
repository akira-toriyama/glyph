package bump

import (
	"testing"
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

// TestParseBaseVersion: the bound of --since-tag=below: may be a release
// CANDIDATE, and it compares as the release it is a candidate for. The two
// tables below are the whole contract — what the base parse adds over
// ParseVersion, and what it deliberately does not relax.
func TestParseBaseVersion(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Version
	}{
		// Plain versions parse identically — below: did not lose its old input.
		{"v1.2.3", Version{1, 2, 3}},
		{"1.2.3", Version{1, 2, 3}},
		// The shape that killed a real release job: GoReleaser's `prerelease:
		// auto` is keyed off exactly this, and goreleaser.yml hands the tag
		// straight to below:.
		{"v3.0.0-rc.1", Version{3, 0, 0}},
		{"v1.0.0-rc1", Version{1, 0, 0}},
		{"v0.1.0-alpha.2.beta", Version{0, 1, 0}},
		{"v1.2.3+build.5", Version{1, 2, 3}},
		{"v1.2.3-rc.1+build.5", Version{1, 2, 3}},
	} {
		got, err := ParseBaseVersion(c.in)
		if err != nil {
			t.Errorf("ParseBaseVersion(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseBaseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}

	// Still rejected. The triple in front is parsed by ParseVersion itself, so
	// the house rules on it are unchanged; only a suffix AFTER a complete
	// triple is new. A workflow templating an unset variable, or naming a
	// branch, must still die as caller input rather than resolve to a tag it
	// did not mean.
	for _, in := range []string{
		"",
		"main",
		"v1.2",
		"v1.2.3.4",
		"v01.2.3",
		"V1.2.3",
		" v1.2.3",
		"v1.2.3 ",
		"v1.2.3-",     // a suffix marker with no suffix is not a pre-release
		"v1.2.3-rc 1", // whitespace is not in semver's pre-release alphabet
		"v1.2-rc.1",   // the suffix cannot complete an incomplete triple
	} {
		if got, err := ParseBaseVersion(in); err == nil {
			t.Errorf("ParseBaseVersion(%q) should fail, got %+v", in, got)
		}
	}
}

// TestParseVersionStillRefusesCandidates guards the half of the split that is
// easy to lose: ParseBaseVersion exists so the CANDIDATE SET does not have to
// change. latestVersionTag parses every tag in the repository with
// ParseVersion and skips what fails, so the moment ParseVersion accepts a
// pre-release, a release resolves the predecessor of a candidate — the exact
// wrong answer the shell derivation was retired for (t-s5n4), now reachable
// from inside glyph.
func TestParseVersionStillRefusesCandidates(t *testing.T) {
	for _, in := range []string{"v1.0.0-rc1", "v3.0.0-rc.1", "v1.2.3+build.5"} {
		if got, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %+v, want an error: a candidate must never be eligible "+
				"as a walk base or a version to step from", in, got)
		}
		// Positive control: the same string IS a valid bound, so a failure
		// above cannot be blamed on the string being malformed.
		if _, err := ParseBaseVersion(in); err != nil {
			t.Errorf("ParseBaseVersion(%q) rejects it too (%v) — this table is asserting nothing", in, err)
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
// and that a 0.x major steps to 1.0.0 (plain semver, with no 0.x-keeps-0.x
// exception). This test IS the check on that arithmetic — see Next's own doc
// for why nothing downstream re-derives it.
func TestNext(t *testing.T) {
	cases := []struct {
		in    string
		level Level
		want  string
	}{
		{"v1.2.3", LevelNone, "v1.2.3"},
		{"v1.2.3", LevelPatch, "v1.2.4"},
		{"v1.2.3", LevelMinor, "v1.3.0"},
		{"v1.2.3", LevelMajor, "v2.0.0"},
		{"v0.6.1", LevelPatch, "v0.6.2"},
		{"v0.6.1", LevelMinor, "v0.7.0"},
		{"v0.6.1", LevelMajor, "v1.0.0"},
		{"v0.0.0", LevelPatch, "v0.0.1"},
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
		b := Level(level)
		if !b.Valid() {
			t.Skip("out-of-lattice level")
		}
		v := Version{Major: major, Minor: minor, Patch: patch}
		next := v.Next(b)
		if b == LevelNone {
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
