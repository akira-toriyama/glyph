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
)

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
