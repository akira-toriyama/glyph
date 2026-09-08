package cli

import (
	"encoding/json"
	"testing"

	"github.com/akira-toriyama/glyph/internal/emoji"
)

// TestEmojiPrintsTheShippedBytes: stdout is the embedded dictionary byte for
// byte — no re-encoding, no envelope, nothing on stderr — so a caller piping
// it to jq and a session reading table.json off GitHub hold the same bytes.
func TestEmojiPrintsTheShippedBytes(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "emoji")
	if code != 0 {
		t.Fatalf("glyph emoji exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != string(emoji.JSON()) {
		t.Fatalf("stdout is not the shipped table.json byte for byte\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("emoji wrote to stderr; the command has no diagnostics: %q", stderr)
	}
	var entries []emoji.Entry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array of entries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the dictionary is empty")
	}
}

// TestEmojiTakesNoArguments: the command has no search surface of its own
// (jq and fzf are the search), so an argument is usage, at the usage code.
func TestEmojiTakesNoArguments(t *testing.T) {
	if code, _, _ := runGlyph(t, "emoji", ":bug:"); code != 2 {
		t.Fatalf("glyph emoji <arg> should exit 2 (usage), got %d", code)
	}
}
