package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// captureOut runs fn with the package stdout writer redirected to a buffer and
// returns what was written.
func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := out
	out = &buf
	defer func() { out = old }()
	fn()
	return buf.String()
}

// TestVersionHuman keeps the default (no --ndjson) as a human-readable
// `glyph <ver>` line so the JSON branch stays opt-in.
func TestVersionHuman(t *testing.T) {
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"version"})
		if err := root.Execute(); err != nil {
			t.Fatalf("glyph version: unexpected error: %v", err)
		}
	})
	if !strings.HasPrefix(got, "glyph ") {
		t.Fatalf("plain `version` should print a `glyph <ver>` line, got: %q", got)
	}
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("plain `version` should not emit JSON, got: %q", got)
	}
}

// TestVersionNDJSON: the subcommand owns its own --ndjson (root's --ndjson is a
// local flag that would not reach it) and emits single-line JSON carrying the
// version field.
func TestVersionNDJSON(t *testing.T) {
	var e error
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"version", "--ndjson"})
		e = root.Execute()
	})
	if e != nil {
		t.Fatalf("glyph version --ndjson: unexpected error: %v", e)
	}
	line := strings.TrimSpace(got)
	if strings.Contains(line, "\n") {
		t.Fatalf("--ndjson output must be a single line, got:\n%s", line)
	}
	var info map[string]any
	if uerr := json.Unmarshal([]byte(line), &info); uerr != nil {
		t.Fatalf("version --ndjson output is not valid JSON: %v\n%s", uerr, line)
	}
	if _, ok := info["version"]; !ok {
		t.Fatalf("compact version JSON is missing the \"version\" field: %s", line)
	}
}

// TestExecuteReturnsInt guards the exit-code funnel: a successful run returns
// CodeOK.
func TestUnknownCommandIsUsage(t *testing.T) {
	// A bare cobra parse error (unknown subcommand) must map to the usage code
	// through finish(), never to an unclassified code.
	root := newRootCmd()
	root.SetArgs([]string{"no-such-command"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if code := finish(root.Execute()); code != 2 {
		t.Fatalf("unknown command should exit 2 (usage), got %d", code)
	}
}
