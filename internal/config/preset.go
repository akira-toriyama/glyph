package config

import (
	"bytes"
	"embed"
	"sort"
	"strings"
)

// presetFS embeds the shipped glyph.toml presets — what `glyph init
// --<preset>` writes, once Preset has composed the shared blocks in. The
// preset files plus the snippets are the single source: the init command
// writes the composition, and this package's tests load the same bytes, so
// the generated artifact and the loader can never drift apart silently.
//
//go:embed presets/*.toml
var presetFS embed.FS

// packagesSnippet is the commented [[packages]] paragraph every preset
// carries, embedded as the ONE source of the block. Before this existed the
// paragraph was typed into each preset separately, and the copies drifted:
// the gemoji preset was corrected to say packages is a verdict input while
// the conventional preset kept writing "not yet a verdict input" into every
// adopter's glyph.toml for the same shipped binary.
//
//go:embed presets/packages.snippet
var packagesSnippet []byte

// Preset returns the named preset's glyph.toml content, or false for a name
// this binary does not ship. The packages block is spliced in above the
// [commit] table — the block is grammar-independent, so it sits in the
// top-level section every preset shares rather than under the grammar each
// preset chooses.
func Preset(name string) ([]byte, bool) {
	data, err := presetFS.ReadFile("presets/" + name + ".toml")
	if err != nil {
		return nil, false
	}
	anchor := []byte("\n[commit]\n")
	i := bytes.Index(data, anchor)
	if i < 0 {
		// Every shipped preset carries a [commit] table; a preset without one
		// is a build defect, not a runtime condition.
		panic("config: preset " + name + " carries no [commit] table to anchor the packages block before")
	}
	var b bytes.Buffer
	b.Write(data[:i])
	b.WriteString("\n")
	b.Write(packagesSnippet)
	b.Write(data[i:])
	return b.Bytes(), true
}

// PresetNames lists the shipped presets, sorted.
func PresetNames() []string {
	entries, err := presetFS.ReadDir("presets")
	if err != nil {
		// The directory is embedded at compile time; an unreadable embed is a
		// build defect, not a runtime condition.
		panic("config: embedded presets unreadable: " + err.Error())
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(names)
	return names
}
