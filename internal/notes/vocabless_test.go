package notes

import (
	"strings"
	"testing"
)

// TestEntryLineWithoutEmoji pins the conventional-vocabulary rendering: an
// entry whose table row carries no emoji renders "- subject", not "-  subject"
// — the double space measured on glyph-test2's first draft. The gitmoji
// rendering (emoji present) is pinned by the golden files and must not move.
func TestEntryLineWithoutEmoji(t *testing.T) {
	got := entryLine(Entry{Subject: "rename the exported field", Pull: 3})
	if want := "- rename the exported field (#3)"; got != want {
		t.Fatalf("entryLine without emoji = %q, want %q", got, want)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("entryLine without emoji carries a double space: %q", got)
	}
}
