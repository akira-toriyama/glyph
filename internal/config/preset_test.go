package config

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestPresetsShareOnePackagesBlock: the commented [[packages]] paragraph is
// what `glyph init` writes into an adopter's file about a shipped feature, and
// it is the same paragraph under every grammar. It was once typed into each
// preset separately, and the copies drifted — the gemoji copy said packages
// is a verdict input while the conventional copy told the same binary's
// adopters the walk "is still being built". One embedded snippet is the fix;
// this pins that every preset carries it, once, and that what it says is the
// shipped state.
func TestPresetsShareOnePackagesBlock(t *testing.T) {
	const open = "\n# Independently versioned lines in one repository"
	const close = "\n[commit]\n"
	var want []byte
	for _, name := range PresetNames() {
		data, ok := Preset(name)
		if !ok {
			t.Fatalf("Preset(%s) missing", name)
		}
		i := bytes.Index(data, []byte(open))
		j := bytes.Index(data, []byte(close))
		if i < 0 || j < 0 || j < i {
			t.Fatalf("preset %s: packages block not found above [commit] (open=%d close=%d)", name, i, j)
		}
		block := data[i:j]
		if bytes.Count(data, []byte(open)) != 1 {
			t.Errorf("preset %s carries the packages block %d times, want once", name, bytes.Count(data, []byte(open)))
		}
		if strings.Contains(string(block), "Not yet a verdict input") {
			t.Errorf("preset %s still calls packages \"Not yet a verdict input\" — the walk shipped (DESIGN §4.1)", name)
		}
		if !strings.Contains(string(block), "A verdict input wherever a diff can be read") {
			t.Errorf("preset %s: packages block does not say packages is a verdict input", name)
		}
		if want == nil {
			want = block
			continue
		}
		if !bytes.Equal(block, want) {
			t.Errorf("preset %s: packages block differs from the first preset's — one snippet, one wording", name)
		}
	}
}

// TestGlyphOwnConfigIsTheGemojiPreset holds glyph's own committed glyph.toml
// byte-identical to `glyph init --gemoji` output: the file is a generated
// artifact and the preset is its only source. The discipline dates from the
// v1-acceptance window's composed artifact — the window block once lived
// only as a hand edit here, and 33 migrating repositories were asked to
// retype it — and outlives the window because the hub's config arm ships
// this file at the canonical tag AS generator output, without running a
// binary. Regenerate with `go run ./cmd/glyph init --gemoji --force`; never
// hand-edit.
func TestGlyphOwnConfigIsTheGemojiPreset(t *testing.T) {
	own, err := os.ReadFile("../../glyph.toml")
	if err != nil {
		t.Fatalf("read glyph.toml: %v", err)
	}
	want, ok := Preset("gemoji")
	if !ok {
		t.Fatalf("Preset(gemoji) missing")
	}
	if !bytes.Equal(own, want) {
		t.Fatalf("glyph.toml is not the generated artifact — regenerate it with `go run ./cmd/glyph init --gemoji --force` (never hand-edit; diff begins at %q)", firstDiffLine(string(own), string(want)))
	}
}

// firstDiffLine names the first line where two texts diverge, for a failure
// message that points instead of dumping both files.
func firstDiffLine(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return al[i]
		}
	}
	if len(al) < len(bl) {
		return bl[len(al)]
	}
	if len(bl) < len(al) {
		return al[len(bl)]
	}
	return ""
}
