package config

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
)

// presetFS embeds the shipped glyph.toml presets — the artifacts `glyph init
// --<preset>` writes, byte for byte. The preset files are the single source:
// the init command writes them, and this package's tests load them, so the
// generated artifact and the loader can never drift apart silently.
//
//go:embed presets/*.toml
var presetFS embed.FS

// Preset returns the named preset's glyph.toml content, or false for a name
// this binary does not ship.
func Preset(name string) ([]byte, bool) {
	b, err := presetFS.ReadFile("presets/" + name + ".toml")
	if err != nil {
		return nil, false
	}
	return b, true
}

// v1WindowSnippet is the v1-acceptance window pattern, embedded as the ONE
// source of the block: `init --gemoji --v1-window` splices it, and glyph's
// own committed glyph.toml is held byte-identical to that output by test —
// before this existed the block lived only as a hand edit, so every migrating
// repository was asked to retype the same eight lines and nothing could
// notice a retype drifting.
//
//go:embed presets/v1window.snippet
var v1WindowSnippet []byte

// PresetWithV1Window returns the named preset with the v1-acceptance window
// pattern spliced in as the LAST pattern — after the skip patterns, whose
// prefixes are disjoint from a gitmoji subject, so last is semantically the
// same as anywhere below the strict pattern and mechanically anchored on the
// [note] table every preset carries.
//
// Only the gemoji preset composes: the window exists to accept the fleet's
// pre-sigil history, and that history is gitmoji — under any other grammar
// the pattern would claim nothing and its warning would be a lie about what
// the repository migrated from.
func PresetWithV1Window(name string) ([]byte, error) {
	if name != "gemoji" {
		return nil, fmt.Errorf("--v1-window composes only with --gemoji: the window accepts the fleet's v1 history, which is gitmoji — a %s repository has no such history to accept", name)
	}
	data, ok := Preset(name)
	if !ok {
		return nil, fmt.Errorf("unknown preset %q", name)
	}
	anchor := []byte("\n[note]\n")
	i := bytes.Index(data, anchor)
	if i < 0 {
		return nil, fmt.Errorf("preset %q carries no [note] table to anchor the window before", name)
	}
	var b bytes.Buffer
	b.Write(bytes.Replace(data[:i], []byte("`glyph init --gemoji`"), []byte("`glyph init --gemoji --v1-window`"), 1))
	b.WriteString("\n")
	b.Write(v1WindowSnippet)
	b.Write(data[i:])
	return b.Bytes(), nil
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
