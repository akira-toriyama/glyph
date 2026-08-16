package config

import (
	"embed"
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
