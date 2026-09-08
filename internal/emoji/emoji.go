// Package emoji ships the gemoji dictionary `glyph emoji` prints: for each
// kind of change the fleet makes, the one GitHub shortcode it is written as,
// and when to pick it over its neighbours.
//
// It is advice for the writer of a subject and nothing else reads it: not
// lint (any `:name:` the repository's pattern accepts is legal), not bump
// (the sigil beside the code decides the version, DESIGN §2), not notes.
// table.json is the dictionary's one home and is served byte for byte, so
// the file on GitHub and the command's stdout are the same bytes — a test
// holds the file to the encoder's canonical form. No I/O beyond the embed.
package emoji

import (
	_ "embed"
	"encoding/json"
)

// Entry is one kind of change. Code is the shortcode as it is written at the
// head of a subject (`:bug:`); Emoji is the code points GitHub renders it as,
// exactly as https://api.github.com/emojis reports them (no variation
// selector appended — the API is the oracle, not a font); Name is the kind,
// one word, unique across the table (that uniqueness IS the one-meaning-
// one-emoji rule, and a test enforces it); Description says when this code
// is the right one, borders with its neighbours included; Absorbs lists the
// gitmoji codes whose meaning this entry took over, so a reader arriving
// with gitmoji habits can look their old code up.
type Entry struct {
	Code        string   `json:"code"`
	Emoji       string   `json:"emoji"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Absorbs     []string `json:"absorbs,omitempty"`
}

// The dictionary is ORDERED: the first entry whose description fits is the
// one to write, top to bottom, exactly as [[patterns]] resolve a message.
// Specific kinds (a license file, a .gitignore) sit above broad ones (fix,
// feature) for that reason, so a reader stops at the first fit rather than
// weighing the whole list.
//
//go:embed table.json
var tableJSON []byte

// JSON returns the dictionary exactly as shipped: the embedded file, byte for
// byte, trailing newline included.
func JSON() []byte { return tableJSON }

// Table decodes the dictionary. The file is embedded at compile time, so a
// decode failure is a build defect, not a runtime condition.
func Table() []Entry {
	var t []Entry
	if err := json.Unmarshal(tableJSON, &t); err != nil {
		panic("emoji: embedded table.json is not the dictionary: " + err.Error())
	}
	return t
}
