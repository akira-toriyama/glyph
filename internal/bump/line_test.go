package bump

import (
	"strings"
	"testing"
)

func TestSplitTag(t *testing.T) {
	cases := []struct{ in, prefix, rest string }{
		{"v1.2.3", "", "v1.2.3"},
		{"haiku/v1.2.3", "haiku/", "v1.2.3"},
		{"exporters/prometheus/v0.59.1", "exporters/prometheus/", "v0.59.1"},
		{"haiku/", "haiku/", ""},
		{"Unreleased", "", "Unreleased"},
	}
	for _, c := range cases {
		prefix, rest := SplitTag(c.in)
		if prefix != c.prefix || rest != c.rest {
			t.Errorf("SplitTag(%q) = %q, %q; want %q, %q", c.in, prefix, rest, c.prefix, c.rest)
		}
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

// FuzzSplitTag: the split is lossless, the prefix is "" or ends in '/', and
// the remainder holds no '/' — so ParseVersionOn(prefix, tag) sees exactly
// the tag's own line, whatever the tag.
func FuzzSplitTag(f *testing.F) {
	for _, s := range []string{"", "v1.2.3", "a/b/v1.0.0", "/", "a/", "//v1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		prefix, rest := SplitTag(tag)
		if prefix+rest != tag {
			t.Fatalf("SplitTag(%q) = %q + %q, not lossless", tag, prefix, rest)
		}
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			t.Fatalf("SplitTag(%q) prefix %q does not end in '/'", tag, prefix)
		}
		if strings.Contains(rest, "/") {
			t.Fatalf("SplitTag(%q) rest %q holds a '/'", tag, rest)
		}
		if v, err := ParseVersionOn(prefix, tag); err == nil {
			if v.TagOn(prefix) != prefix+v.String() {
				t.Fatalf("TagOn round trip broke for %q", tag)
			}
		}
	})
}
