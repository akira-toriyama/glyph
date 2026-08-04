package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRulesJSON: `glyph rules --json` emits the embedded table as JSON carrying
// all 75 codes, so a caller can pipe it to jq.
func TestRulesJSON(t *testing.T) {
	code, got, stderr := runGlyph(t, "rules", "--json")
	if code != 0 {
		t.Fatalf("glyph rules --json exited %d, want 0\nstderr: %s", code, stderr)
	}
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
	code, got, stderr := runGlyph(t, "rules")
	if code != 0 {
		t.Fatalf("glyph rules exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(got, "# gitmoji") {
		t.Fatalf("plain `rules` should print a Markdown heading, got: %q", first80(got))
	}
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("plain `rules` should not emit JSON, got: %q", first80(got))
	}
}

// TestRulesMarkdownFlag: `--md` is the explicit form of the default.
func TestRulesMarkdownFlag(t *testing.T) {
	code, got, stderr := runGlyph(t, "rules", "--md")
	if code != 0 {
		t.Fatalf("glyph rules --md exited %d, want 0\nstderr: %s", code, stderr)
	}
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

// TestRulesBooleanFormatFlagsAreReadByValue: the format group is decided by what
// the flags ARE, not by whether they were mentioned.
//
// cobra's MarkFlagsMutuallyExclusive groups on Changed, which got both
// directions wrong on a boolean. `--md=false` was not in the group's way, so
// nothing looked at it and the caller who said "not Markdown" was handed
// Markdown at exit 0 — the silent ignore flags.go exists to forbid, wearing a
// bool. And `--json=false --md`, which asks for one format and explicitly
// declines the other, was refused with "[json md] were all set": a false
// statement about the invocation, inside the machine-readable envelope other
// repositories parse.
func TestRulesBooleanFormatFlagsAreReadByValue(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		wantCode int
		wantOut  string // a prefix stdout must carry when the command runs
		wantErr  string // a fragment the usage message must carry
	}{
		"no flags is the markdown default": {
			args: []string{"rules"}, wantCode: 0, wantOut: "# gitmoji",
		},
		"explicit md is the same path": {
			args: []string{"rules", "--md"}, wantCode: 0, wantOut: "# gitmoji",
		},
		"json selects json": {
			args: []string{"rules", "--json"}, wantCode: 0, wantOut: "{",
		},
		"declining json still leaves markdown": {
			args: []string{"rules", "--json=false", "--md"}, wantCode: 0, wantOut: "# gitmoji",
		},
		"declining json alone is still the default": {
			args: []string{"rules", "--json=false"}, wantCode: 0, wantOut: "# gitmoji",
		},
		"declining the default selects nothing": {
			args: []string{"rules", "--md=false"}, wantCode: 2, wantErr: "selects nothing",
		},
		"both on is the real conflict": {
			args: []string{"rules", "--json", "--md"}, wantCode: 2, wantErr: "cannot be combined",
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("%v exited %d, want %d\nstdout: %s\nstderr: %s", tc.args, code, tc.wantCode, stdout, stderr)
			}
			if tc.wantOut != "" && !strings.HasPrefix(strings.TrimSpace(stdout), tc.wantOut) {
				t.Errorf("%v stdout should start with %q:\n%s", tc.args, tc.wantOut, stdout)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("%v should explain itself with %q:\n%s", tc.args, tc.wantErr, stderr)
			}
			// Whatever the verdict, the diagnostic must not claim a flag was set
			// that the caller turned off.
			if strings.Contains(stderr, "were all set") {
				t.Errorf("%v: the envelope repeats cobra's Changed-based claim, which is false for a =false flag:\n%s", tc.args, stderr)
			}
		})
	}
}
