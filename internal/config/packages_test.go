package config

import (
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
