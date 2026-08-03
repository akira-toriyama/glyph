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

// TestVersionHuman keeps the default (no --json) as a human-readable
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

// TestVersionJSON: the subcommand owns its own --json (a root-level one is a
// local flag that would not reach it) and emits single-line JSON carrying the
// version field.
func TestVersionJSON(t *testing.T) {
	var e error
	got := captureOut(t, func() {
		root := newRootCmd()
		root.SetArgs([]string{"version", "--json"})
		e = root.Execute()
	})
	if e != nil {
		t.Fatalf("glyph version --json: unexpected error: %v", e)
	}
	line := strings.TrimSpace(got)
	if strings.Contains(line, "\n") {
		t.Fatalf("--json output must be a single line, got:\n%s", line)
	}
	var info map[string]any
	if uerr := json.Unmarshal([]byte(line), &info); uerr != nil {
		t.Fatalf("version --json output is not valid JSON: %v\n%s", uerr, line)
	}
	if _, ok := info["version"]; !ok {
		t.Fatalf("compact version JSON is missing the \"version\" field: %s", line)
	}
}

// TestUnknownCommandIsUsage: a bare cobra parse error (unknown subcommand) must
// map to the usage code through finish(), never to an unclassified code.
func TestUnknownCommandIsUsage(t *testing.T) {
	if code, _, _ := runGlyph(t, "no-such-command"); code != 2 {
		t.Fatalf("unknown command should exit 2 (usage), got %d", code)
	}
}

// TestVersionJSONFalseIsTheHumanLine defends the answer t-y2hr measured and
// nothing pinned: an EXPLICIT --json=false selects the human line, at exit 0.
//
// pflag's Changed is true for `--json=false`, so a future reader who "fixes"
// the flag read by switching from the value to cmd.Flags().Changed("json")
// hands JSON to the caller who just said not-JSON — and it compiles, because
// an unread package-level var is legal Go. That is t-y2hr's defect one flag
// over, and this is its only defender. The machine flag is deliberately NOT
// default-bearing: declining it selects a real output, so it gets no
// checkDefaultModeOff (unlike `rules --md`, where declining selects nothing).
func TestVersionJSONFalseIsTheHumanLine(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "version", "--json=false")
	if code != 0 {
		t.Fatalf("version --json=false exited %d, want 0 — declining the machine flag selects "+
			"the human line, which is a real output\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "glyph ") {
		t.Errorf("version --json=false should print the `glyph <ver>` line, got: %q", stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Errorf("version --json=false emitted JSON to a caller who declined it — the flag is "+
			"being read by MENTION (Changed) instead of by value: %q", stdout)
	}
}
