package workflows

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPRVerdictBreakingFollowsLevelIntoNotComputed runs pr-verdict.yml's
// verdict step — its own shell, with a stub glyph on PATH handing it an
// envelope — and reads what it writes to $GITHUB_OUTPUT. `breaking` is the
// readable alias of `level`: "true" for major, "false" for any other level,
// and EMPTY whenever level is. A repository declaring [[packages]] always
// gets an empty level, because no one line has a level for a scalar to
// describe (DESIGN §4.1), whatever its lines do. The first cut derived
// breaking as `[ "$level" = "major" ]` and published "false" there — a
// definite "no" for a pull that breaks a line (t-xbk0, measured 2026-09-26
// with this step and a real binary on glyph-monorepo-test #30: haiku folds
// major, the step wrote level= and breaking=false). Positive control: the
// single-line major envelope must come out "true", which proves the stub ran
// and its envelope was read (mutation row
// pr-verdict-breaking-reads-not-computed-as-false).
func TestPRVerdictBreakingFollowsLevelIntoNotComputed(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is not on PATH, and the step under test is jq — install it rather than skip: an unrun guard is a green nobody earned")
	}
	script := extractRun(t, repoFile(t, filepath.Join(".github", "workflows", "pr-verdict.yml")), "Compose the verdict comment")

	const packagesEnvelope = `{"current":"","untagged":false,"level":"","pr":"","pending":"","body":"b","packages":[{"path":"haiku","current":"v2.1.0","untagged":false,"level":"major","next":"v3.0.0","pr":"major","pending":"major"}]}`
	cases := []struct {
		name, envelope, level, breaking string
	}{
		{"single line, major", `{"current":"v1.2.3","untagged":false,"level":"major","next":"v2.0.0","pr":"major","pending":"none","body":"b"}`, "major", "true"},
		{"single line, minor", `{"current":"v1.2.3","untagged":false,"level":"minor","next":"v1.3.0","pr":"minor","pending":"none","body":"b"}`, "minor", "false"},
		{"single line, none", `{"current":"v1.2.3","untagged":false,"level":"none","pr":"none","pending":"none","body":"b"}`, "none", "false"},
		{"packages, a line folding major", packagesEnvelope, "", ""},
		{"packages, nothing touched", `{"current":"","untagged":false,"level":"","pr":"","pending":"","body":"b"}`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			envelope := filepath.Join(dir, "envelope.json")
			if err := os.WriteFile(envelope, []byte(c.envelope), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			// #nosec G306 -- the stub must be executable to stand in for glyph on PATH.
			if err := os.WriteFile(filepath.Join(bin, "glyph"), []byte("#!/bin/sh\ncat \"$STUB_ENVELOPE\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "github_output")
			if err := os.WriteFile(output, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"STUB_ENVELOPE="+envelope,
				"PR_NUMBER=7",
				"RUNNER_TEMP="+dir,
				"GITHUB_OUTPUT="+output)
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				t.Fatalf("the verdict step exited %d over %s:\n%s", ee.ExitCode(), c.envelope, out)
			case err != nil:
				t.Fatalf("running the verdict step: %v", err)
			}
			raw, rerr := os.ReadFile(output)
			if rerr != nil {
				t.Fatal(rerr)
			}
			got := map[string]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				k, v, _ := strings.Cut(line, "=")
				got[k] = v
			}
			if lv, ok := got["level"]; !ok || lv != c.level {
				t.Fatalf("level output = %q (written: %v), want %q — the step did not read the envelope it was handed:\n%s", lv, ok, c.level, raw)
			}
			bk, ok := got["breaking"]
			if !ok {
				t.Fatalf("the step wrote no breaking output at all:\n%s", raw)
			}
			if bk != c.breaking {
				t.Errorf("breaking = %q, want %q — breaking follows level, and an empty level is NOT COMPUTED, never \"false\":\n%s", bk, c.breaking, raw)
			}
		})
	}
}
