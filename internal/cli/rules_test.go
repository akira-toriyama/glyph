package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRulesJSON: `glyph rules --json` emits the embedded table as JSON carrying
// all 75 codes, so a caller can pipe it to jq.
func TestRulesJSON(t *testing.T) {
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"rules", "--json"})
		if err := root.Execute(); err != nil {
			t.Fatalf("glyph rules --json: unexpected error: %v", err)
		}
	})
	var table struct {
		Version string           `json:"version"`
		Codes   []map[string]any `json:"codes"`
	}
	if err := json.Unmarshal([]byte(got), &table); err != nil {
		t.Fatalf("rules --json output is not valid JSON: %v\n%s", err, got)
	}
	if len(table.Codes) != 75 {
		t.Fatalf("rules --json has %d codes, want 75", len(table.Codes))
	}
	if table.Version == "" {
		t.Fatalf("rules --json is missing the version field")
	}
}

// TestRulesDefaultIsMarkdown: with no flag, `glyph rules` prints the Markdown
// table (a `# ` heading), never JSON — so the JSON branch stays opt-in.
func TestRulesDefaultIsMarkdown(t *testing.T) {
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"rules"})
		if err := root.Execute(); err != nil {
			t.Fatalf("glyph rules: unexpected error: %v", err)
		}
	})
	if !strings.HasPrefix(got, "# gitmoji") {
		t.Fatalf("plain `rules` should print a Markdown heading, got: %q", first80(got))
	}
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("plain `rules` should not emit JSON, got: %q", first80(got))
	}
}

// TestRulesMarkdownFlag: `--md` is the explicit form of the default.
func TestRulesMarkdownFlag(t *testing.T) {
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"rules", "--md"})
		if err := root.Execute(); err != nil {
			t.Fatalf("glyph rules --md: unexpected error: %v", err)
		}
	})
	if !strings.HasPrefix(got, "# gitmoji") {
		t.Fatalf("`rules --md` should print a Markdown heading, got: %q", first80(got))
	}
}

// TestRulesJSONAndMDConflict: the two formats are mutually exclusive; asking for
// both is a usage error (exit 2), not a silent pick.
func TestRulesJSONAndMDConflict(t *testing.T) {
	if code, _, _ := runGlyph(t, "rules", "--json", "--md"); code != 2 {
		t.Fatalf("`rules --json --md` should exit 2 (usage), got %d", code)
	}
}

func first80(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
