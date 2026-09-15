package config

import (
	"bytes"
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
