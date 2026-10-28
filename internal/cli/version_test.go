package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/version"
)

// TestVersionHuman keeps the default (no --json) as a human-readable
// `glyph <ver>` line so the JSON branch stays opt-in.
func TestVersionHuman(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "version")
	if code != 0 {
		t.Fatalf("glyph version exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "glyph ") {
		t.Fatalf("plain `version` should print a `glyph <ver>` line, got: %q", stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("plain `version` should not emit JSON, got: %q", stdout)
	}
}

// TestVersionJSON: the subcommand owns its own --json (a root-level one is a
// local flag that would not reach it) and emits single-line JSON carrying the
// version field.
func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "version", "--json")
	if code != 0 {
		t.Fatalf("glyph version --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	line := strings.TrimSpace(stdout)
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

// TestVersionRunsFromOptionsWithoutCobra: versionRun is a function of its
// options — no command built, no flag parsed — and each output is
// version.Resolve() itself: the human line, or its compact JSON.
func TestVersionRunsFromOptionsWithoutCobra(t *testing.T) {
	stdout, stderr, restore := captureOut()
	defer restore()
	info := version.Resolve()

	if err := versionRun(versionOptions{}); err != nil || stdout.String() != "glyph "+info.String()+"\n" {
		t.Fatalf("versionRun from options: %v, stdout %q, want %q\nstderr: %s", err, stdout.String(), "glyph "+info.String()+"\n", stderr.String())
	}
	stdout.Reset()
	if err := versionRun(versionOptions{json: true}); err != nil {
		t.Fatalf("versionRun --json from options: %v\nstderr: %s", err, stderr.String())
	}
	line, rest, _ := strings.Cut(stdout.String(), "\n")
	var got version.Info
	if uerr := json.Unmarshal([]byte(line), &got); uerr != nil || rest != "" || got != info {
		t.Fatalf("versionRun --json from options wrote %q (decode: %v), want one compact line of %+v", stdout.String(), uerr, info)
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
// a bound field nothing reads is legal Go. That is t-y2hr's defect one flag
// over, and this is its only defender. The machine flag is deliberately NOT
// default-bearing: declining it selects a real output, so it gets no
// default-mode guard (v1's `rules --md` was the counterexample: declining it
// selected nothing).
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
