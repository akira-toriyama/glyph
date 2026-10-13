package bump

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

func TestLinePrefix(t *testing.T) {
	cases := []struct{ in, prefix string }{
		{"v1.2.3", ""},
		{"haiku/v1.2.3", "haiku/"},
		{"exporters/prometheus/v0.59.1", "exporters/prometheus/"},
		{"haiku/", "haiku/"},
		{"Unreleased", ""},
	}
	for _, c := range cases {
		if prefix := LinePrefix(c.in); prefix != c.prefix {
			t.Errorf("LinePrefix(%q) = %q; want %q", c.in, prefix, c.prefix)
		}
	}
}

// TestHouseTagOnRequiresTheV pins the one difference between the two tag
// rules: a v-less 1.2.3 is a version the tolerant readers accept (the walk
// base, --current) and never a house tag — not glyph's draft, not its floor,
// not a release a caller can pin. The line boundary and the major are
// ParseVersionOn's and Line.Holds' rules, asserted here only as far as the
// house reader must inherit them.
func TestHouseTagOnRequiresTheV(t *testing.T) {
	bare, haiku := config.Line{}, config.Line{Prefix: "haiku/"}
	ok := []struct {
		line config.Line
		tag  string
		want Version
	}{
		{bare, "v1.2.3", Version{1, 2, 3}},
		{haiku, "haiku/v1.2.3", Version{1, 2, 3}},
		{config.Line{Prefix: "pubsub/", Major: 2}, "pubsub/v2.7.0", Version{2, 7, 0}},
	}
	for _, c := range ok {
		got, is := HouseTagOn(c.line, c.tag)
		if !is || got != c.want {
			t.Errorf("HouseTagOn(%+v, %q) = %v, %t; want %v, true", c.line, c.tag, got, is, c.want)
		}
	}
	refused := []struct {
		line config.Line
		tag  string
		why  string
	}{
		{bare, "1.2.3", "no v — ParseVersion reads it, the house does not"},
		{haiku, "haiku/1.2.3", "no v on a line"},
		{haiku, "v1.2.3", "the bare line's tag asked on a package"},
		{bare, "haiku/v1.2.3", "a package's tag asked on the bare line"},
		{haiku, "haiku/sub/v1.2.3", "a nested line's tag"},
		{haiku, "haiku/v1.2.3-rc.1", "a candidate is version-shaped, never a house tag"},
		{bare, "Unreleased", "the placeholder is deliberately not house-shaped"},
		{bare, "vNext", "the v alone is not the shape"},
		{config.Line{Prefix: "pubsub/", Major: 2}, "pubsub/v1.9.0", "a major the locked line does not hold"},
		{config.Line{Claimed: []int{2}}, "v2.0.0", "a major a sibling line claimed"},
	}
	for _, c := range refused {
		if got, is := HouseTagOn(c.line, c.tag); is {
			t.Errorf("HouseTagOn(%+v, %q) = %v, true; want false — %s", c.line, c.tag, got, c.why)
		}
	}
	// The tolerant rule stays tolerant: the same v-less tag the house refuses
	// is a version to the readers that take releases of someone else's making.
	if v, err := ParseVersionOn("", "1.2.3"); err != nil || v != (Version{1, 2, 3}) {
		t.Errorf("ParseVersionOn(\"\", 1.2.3) = %v, %v; want v1.2.3 — the walk base must still read a hand-cut v-less tag", v, err)
	}
}

// TestParseVersionOnRefusesOtherLines pins the line boundary: a tag answers
// only on its own line. A bare tag on a package's line, a package's tag on
// the bare line, a sibling's tag, and a NESTED line's tag are all refused —
// the last one because haiku/sub/v9.0.0 is exactly the tag a parent line must
// not take as its base.
func TestParseVersionOnRefusesOtherLines(t *testing.T) {
	ok := []struct {
		prefix, tag string
		want        Version
	}{
		{"", "v1.2.3", Version{1, 2, 3}},
		{"", "1.2.3", Version{1, 2, 3}},
		{"haiku/", "haiku/v1.2.3", Version{1, 2, 3}},
		{"haiku/", "haiku/1.2.3", Version{1, 2, 3}},
		{"haiku/sub/", "haiku/sub/v9.0.0", Version{9, 0, 0}},
	}
	for _, c := range ok {
		got, err := ParseVersionOn(c.prefix, c.tag)
		if err != nil || got != c.want {
			t.Errorf("ParseVersionOn(%q, %q) = %v, %v; want %v", c.prefix, c.tag, got, err, c.want)
		}
	}
	refused := []struct{ prefix, tag string }{
		{"haiku/", "v1.2.3"},
		{"", "haiku/v1.2.3"},
		{"haiku/", "curry/v1.2.3"},
		{"haiku/", "haiku/sub/v9.0.0"},
		{"haiku/", "haikus/v1.2.3"},
		{"haiku/", "haiku/v1.2.3-rc.1"},
		{"haiku/", "haiku/Unreleased"},
	}
	for _, c := range refused {
		if got, err := ParseVersionOn(c.prefix, c.tag); err == nil {
			t.Errorf("ParseVersionOn(%q, %q) = %v, want an error — the tag is on another line", c.prefix, c.tag, got)
		}
	}
}

func TestParseBaseVersionOn(t *testing.T) {
	got, err := ParseBaseVersionOn("haiku/", "haiku/v3.0.0-rc.1")
	if err != nil || got != (Version{3, 0, 0}) {
		t.Errorf("ParseBaseVersionOn(haiku/, haiku/v3.0.0-rc.1) = %v, %v; want v3.0.0", got, err)
	}
	for _, c := range []struct{ prefix, tag string }{{"", "haiku/v3.0.0-rc.1"}, {"haiku/", "v3.0.0-rc.1"}, {"haiku/", "haiku/sub/v3.0.0-rc.1"}} {
		if got, err := ParseBaseVersionOn(c.prefix, c.tag); err == nil {
			t.Errorf("ParseBaseVersionOn(%q, %q) = %v, want an error", c.prefix, c.tag, got)
		}
	}
}

func TestTagOn(t *testing.T) {
	v := Version{1, 2, 3}
	if got := v.TagOn(""); got != "v1.2.3" {
		t.Errorf("TagOn(\"\") = %q", got)
	}
	if got := v.TagOn("haiku/"); got != "haiku/v1.2.3" {
		t.Errorf("TagOn(haiku/) = %q", got)
	}
}

// FuzzLinePrefix: the prefix is "" or ends in '/', the tag starts with it,
// and what follows holds no '/' — so ParseVersionOn(prefix, tag) sees exactly
// the tag's own line, whatever the tag.
func FuzzLinePrefix(f *testing.F) {
	for _, s := range []string{"", "v1.2.3", "a/b/v1.0.0", "/", "a/", "//v1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		prefix := LinePrefix(tag)
		rest, ok := strings.CutPrefix(tag, prefix)
		if !ok {
			t.Fatalf("LinePrefix(%q) = %q, not a prefix of the tag", tag, prefix)
		}
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			t.Fatalf("LinePrefix(%q) = %q does not end in '/'", tag, prefix)
		}
		if strings.Contains(rest, "/") {
			t.Fatalf("LinePrefix(%q) leaves %q, which holds a '/'", tag, rest)
		}
		if v, err := ParseVersionOn(prefix, tag); err == nil {
			if v.TagOn(prefix) != prefix+v.String() {
				t.Fatalf("TagOn round trip broke for %q", tag)
			}
		}
	})
}
