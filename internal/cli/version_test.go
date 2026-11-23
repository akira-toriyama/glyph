package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	info := decodeJSON[map[string]any](t, stdout)
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

// TestVersionJSONFalseIsTheHumanLine pins: an EXPLICIT --json=false selects
// the human line, at exit 0.
//
// pflag's Changed is true for `--json=false`, so a future reader who "fixes"
// the flag read by switching from the value to cmd.Flags().Changed("json")
// hands JSON to the caller who just said not-JSON — and it compiles, because
// a bound field nothing reads is legal Go. This test is that answer's only
// defender. The machine flag is deliberately NOT default-bearing: declining
// it selects a real output, so it gets no default-mode guard (the
// counterexample is a flag whose declining selects nothing).
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

// TestVersionHelpAndReadmeSayWhatTheDateIs: the two places a user reads what
// `glyph version` prints name the date as the commit date, once each, and
// say what a module-mode `go install …@vX.Y.Z` build leaves out. Measured
// 2026-11-15 on v6.1.0 and at c06e426: `{"version":"v6.1.0"}` and a
// pseudo-version alone, and `go version -m` lists no vcs.* setting for
// either binary. "build date" was the wording while the stamp was the
// build's clock, and the help said a `go install` build "still identifies
// itself" without saying by how little.
func TestVersionHelpAndReadmeSayWhatTheDateIs(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `glyph version` \\|.*$").Find(readme)
	if row == nil {
		t.Fatal("README.md has no `glyph version` row in its command table")
	}
	// runGlyph hands cobra's help to the diagnostic buffer.
	code, _, help := runGlyph(t, "version", "--help")
	if code != 0 {
		t.Fatalf("glyph version --help exited %d, want 0\n%s", code, help)
	}
	for where, text := range map[string]string{
		"README.md's `glyph version` row": string(row),
		"`glyph version --help`":          strings.Join(strings.Fields(help), " "),
	} {
		if n := strings.Count(text, "commit date"); n != 1 {
			t.Errorf("%s says \"commit date\" %d time(s), want once: the date is the commit's in every build that states one", where, n)
		}
		if strings.Contains(text, "build date") {
			t.Errorf("%s says \"build date\"; no build stamps the time it ran", where)
		}
		if !goInstallReportsTheTagAlone.MatchString(text) {
			t.Errorf("%s does not say that a module-mode `go install …@vX.Y.Z` build reports the tag alone — no "+
				"commit, no date — because Go embeds no VCS data there", where)
		}
	}
}

// goInstallReportsTheTagAlone matches the statement, not its lead-in: a text
// that says a module-mode build reports a commit and a date is as wrong as
// one that says nothing.
var goInstallReportsTheTagAlone = regexp.MustCompile("module-mode `go install …@vX\\.Y\\.Z` build reports the " +
	"(?:release )?tag alone[^.]*\\bno commit\\b[^.]*\\bno date\\b")
