package config

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// packagesToml wraps a [[packages]] block in the smallest loadable file so
// every case here exercises the packages validation and nothing else.
func packagesToml(block string) string {
	return "schema = 1\n" + minimalPatterns + block
}

func TestLoadPackages(t *testing.T) {
	cfg, err := Load([]byte(packagesToml(`
[[packages]]
path = "haiku"

[[packages]]
path = "exporters/prometheus"

[[packages]]
path = "."
name = "core"
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Package{
		{Path: "haiku", Name: "haiku"},
		{Path: "exporters/prometheus", Name: "prometheus"},
		{Path: ".", Name: "core"},
	}
	if len(cfg.Packages) != len(want) {
		t.Fatalf("Packages = %+v, want %+v", cfg.Packages, want)
	}
	for i, w := range want {
		if cfg.Packages[i] != w {
			t.Errorf("Packages[%d] = %+v, want %+v (file order, name defaulting to the last path segment)", i, cfg.Packages[i], w)
		}
	}
}

// TestNoPackagesMeansOneLine pins the additive contract: a file with no
// [[packages]] — every shipped preset, the composed init artifact and glyph's
// own committed glyph.toml — loads with NO packages, not a synthesised root
// package. Every consumer that branches on len(Packages) > 0 reads that as
// "the author declared lines", and the single line's verdicts must stay byte
// for byte what they were before the key existed.
func TestNoPackagesMeansOneLine(t *testing.T) {
	sources := map[string][]byte{}
	for _, name := range PresetNames() {
		data, _ := Preset(name)
		sources["preset "+name] = data
	}
	composed, err := PresetWithV1Window("gemoji")
	if err != nil {
		t.Fatalf("PresetWithV1Window: %v", err)
	}
	sources["composed --v1-window"] = composed
	own, err := os.ReadFile("../../glyph.toml")
	if err != nil {
		t.Fatalf("read glyph.toml: %v", err)
	}
	sources["glyph's own glyph.toml"] = own
	sources["minimal"] = []byte("schema = 1\n" + minimalPatterns)
	sources["empty array"] = []byte("schema = 1\npackages = []\n" + minimalPatterns)

	for name, data := range sources {
		cfg, err := Load(data)
		if err != nil {
			t.Fatalf("%s does not load: %v", name, err)
		}
		if len(cfg.Packages) != 0 {
			t.Errorf("%s declares no [[packages]] but loaded %d: %+v — the single line must stay the single line", name, len(cfg.Packages), cfg.Packages)
		}
	}
}

func TestLoadPackagesErrors(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"missing path", "[[packages]]\nname = 'x'\n", "packages[0]: path is required"},
		{"empty path", "[[packages]]\npath = ''\n", "packages[0]: path is required"},
		{"absolute path", "[[packages]]\npath = '/haiku'\n", "is absolute"},
		{"parent path", "[[packages]]\npath = '..'\n", "escapes the repository"},
		{"escaping path", "[[packages]]\npath = '../haiku'\n", "escapes the repository"},
		{"trailing slash", "[[packages]]\npath = 'haiku/'\n", `write "haiku"`},
		{"leading dot-slash", "[[packages]]\npath = './haiku'\n", `write "haiku"`},
		{"double slash", "[[packages]]\npath = 'a//b'\n", `write "a/b"`},
		{"inner parent segment", "[[packages]]\npath = 'a/../b'\n", `write "b"`},
		{"empty name", "[[packages]]\npath = 'haiku'\nname = ''\n", "name is empty"},
		{"duplicate path", "[[packages]]\npath = 'haiku'\n[[packages]]\npath = 'haiku'\nname = 'other'\n", "packages[0] and packages[1] declare the same path"},
		{"duplicate default names", "[[packages]]\npath = 'a/util'\n[[packages]]\npath = 'b/util'\n", `share the name "util"`},
		{"explicit name collides with a default", "[[packages]]\npath = 'haiku'\n[[packages]]\npath = 'curry'\nname = 'haiku'\n", `share the name "haiku"`},
		// tag_prefix was rejected by design (§4.1): the tag line is derived
		// from path. The strict decoder is the whole enforcement, and this
		// row keeps it that way.
		{"tag_prefix is not a key", "[[packages]]\npath = 'haiku'\ntag_prefix = 'h'\n", "unknown key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load([]byte(packagesToml(c.block)))
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want substring %q", err, c.want)
			}
		})
	}
}

// TestPackageTagPrefix pins the path-to-prefix rule, Go's own: the module
// subdirectory NOT including a major version suffix (go.dev/ref/mod). A
// /vN last segment with N ≥ 2 is folded into the line's major; v0, v1 and
// a zero-padded v02 are plain directory names (mutation row
// major-subdirectory-kept-in-the-tag-prefix; t-z9d3, measured on
// google-cloud-go pubsub/v2 → pubsub/v2.7.0 and etcd client/v3 →
// client/v3.6.0).
func TestPackageTagPrefix(t *testing.T) {
	cases := map[string]struct {
		prefix string
		major  int
	}{
		".":                    {"", 0},
		"haiku":                {"haiku/", 0},
		"exporters/prometheus": {"exporters/prometheus/", 0},
		"pubsub/v2":            {"pubsub/", 2},
		"client/v3":            {"client/", 3},
		"a/b/v10":              {"a/b/", 10},
		"v2":                   {"", 2},
		"pubsub/v1":            {"pubsub/v1/", 0},
		"pubsub/v0":            {"pubsub/v0/", 0},
		"pubsub/v02":           {"pubsub/v02/", 0},
		"pubsub/v2x":           {"pubsub/v2x/", 0},
	}
	for path, want := range cases {
		p := Package{Path: path}
		if got := p.TagPrefix(); got != want.prefix {
			t.Errorf("Package{Path: %q}.TagPrefix() = %q, want %q", path, got, want.prefix)
		}
		if got := p.Major(); got != want.major {
			t.Errorf("Package{Path: %q}.Major() = %d, want %d", path, got, want.major)
		}
	}
}

// TestLineOfSharesThePrefixByMajor: pubsub and pubsub/v2 tag on ONE prefix
// and are told apart by the major — the locked line holds 2 alone, the free
// line everything else; an undeclared root yields to a declared root-level
// vN the same way (mutation row line-reads-tags-of-every-major).
func TestLineOfSharesThePrefixByMajor(t *testing.T) {
	cfg, err := Load([]byte(packagesToml("\n[[packages]]\npath = 'pubsub'\n\n[[packages]]\npath = 'pubsub/v2'\n\n[[packages]]\npath = 'pubsub/v3'\n\n[[packages]]\npath = 'v2'\n")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	free, v2, v3, root := cfg.LineOf(cfg.Packages[0]), cfg.LineOf(cfg.Packages[1]), cfg.LineOf(cfg.Packages[2]), cfg.LineOf(Package{Path: "."})
	if free.Prefix != "pubsub/" || free.Major != 0 || fmt.Sprint(free.Claimed) != "[2 3]" {
		t.Errorf("free line = %+v, want pubsub/ with majors 2 and 3 claimed", free)
	}
	if v2.Prefix != "pubsub/" || v2.Major != 2 || v2.Claimed != nil || v3.Major != 3 {
		t.Errorf("locked lines = %+v / %+v", v2, v3)
	}
	for major, want := range map[int][3]bool{0: {true, false, false}, 1: {true, false, false}, 2: {false, true, false}, 3: {false, false, true}, 4: {true, false, false}} {
		if got := [3]bool{free.Holds(major), v2.Holds(major), v3.Holds(major)}; got != want {
			t.Errorf("major %d held by (free, v2, v3) = %v, want %v", major, got, want)
		}
	}
	if root.Prefix != "" || fmt.Sprint(root.Claimed) != "[2]" || root.Holds(2) || !root.Holds(1) {
		t.Errorf("the undeclared root's line = %+v, want the bare prefix with 2 claimed by the v2 package", root)
	}
	if v2.Label() != "pubsub/v2.*" || free.Label() != "pubsub/" || cfg.LineOf(cfg.Packages[3]).Label() != "bare v2.*" || root.Label() != "bare v*" {
		t.Errorf("labels = %q %q %q %q", v2.Label(), free.Label(), cfg.LineOf(cfg.Packages[3]).Label(), root.Label())
	}
	if v2.MajorDir() != "v2/" || free.MajorDir() != "" {
		t.Errorf("MajorDir = %q / %q", v2.MajorDir(), free.MajorDir())
	}
	// The default scope word keeps the suffix, as monorepos write it.
	if cfg.Packages[1].Name != "pubsub/v2" || cfg.Packages[3].Name != "v2" || cfg.Packages[0].Name != "pubsub" {
		t.Errorf("names = %q %q %q", cfg.Packages[1].Name, cfg.Packages[3].Name, cfg.Packages[0].Name)
	}
}
