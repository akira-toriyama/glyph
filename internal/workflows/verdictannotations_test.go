package workflows

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestVerdictReusablesAnnotateEachRefusedCommitOnce runs the verdict steps of
// pr-verdict.yml and release.yml over a stub glyph that writes a refusal's
// stream — one `::error::` per refused commit, then the envelope, whose message
// names the first of them — and reads the step's log. Each refused commit must
// be annotated exactly once, by glyph's own line replayed verbatim: dropped,
// the second commit never reaches the log (t-q733); framed again from
// .error.message, the first is annotated twice. DESIGN §5: the binary that
// computed a finding renders it.
//
// Positive control: over an error glyph did not annotate (a warning, then an
// exit-4 envelope), pr-verdict.yml's frame must be the step's one `::error::`,
// so the frame path ran, its sieve fed jq, and a "no second annotation" above
// is not the frame having gone missing altogether.
func TestVerdictReusablesAnnotateEachRefusedCommitOnce(t *testing.T) {
	requireTool(t, "jq")
	const (
		first  = "::error::glyph: commit 89c1b0e: matches none of the 5 configured patterns"
		second = "::error::glyph: commit 72bb395: matches none of the 5 configured patterns"
	)
	refusal := first + "\n" + second + "\n" + `{
  "error": {
    "code": 3,
    "message": "commit 89c1b0e in pull request o/r#7: matches none of the 5 configured patterns (+1 more refused commit(s), all in details) — the release walk will refuse this commit the same way once it is merged, and a merged commit cannot be rewritten; fix it on the branch",
    "details": [
      {"sha": "89c1b0e15ec06228f6ab56e168c5b59d0fc5e269", "subject": "no sigil one", "detail": "matches none of the 5 configured patterns"},
      {"sha": "72bb395a61c74fb929e92745a902a23bc36a3d6b", "subject": "no sigil two", "detail": "matches none of the 5 configured patterns"}
    ]
  }
}
`
	const apiMessage = "GET repos/o/r/pulls/7/commits: 502 Bad Gateway"
	unannotated := "::warning::glyph: a warning before the failure\n" +
		`{"error":{"code":4,"message":"` + apiMessage + `"}}` + "\n"

	steps := []struct{ file, step string }{
		{"pr-verdict.yml", "Compose the verdict comment"},
		{"release.yml", "Compose the verdict and upsert the rolling DRAFT"},
	}
	cases := []struct {
		name, stream string
		exit         int
		want         []string // the step's `::error::` lines, in order
		only         string   // the one workflow the case runs against, or "" for every step
	}{
		{"a refusal glyph annotated", refusal, 3, []string{first, second}, ""},
		{"an error glyph did not annotate", unannotated, 4, []string{"::error::glyph preview: " + apiMessage}, "pr-verdict.yml"},
	}
	for _, s := range steps {
		script := extractRun(t, repoFile(t, filepath.Join(".github", "workflows", s.file)), s.step)
		for _, c := range cases {
			if c.only != "" && c.only != s.file {
				continue
			}
			t.Run(s.file+"/"+c.name, func(t *testing.T) {
				dir := t.TempDir()
				stream := filepath.Join(dir, "stream")
				if err := os.WriteFile(stream, []byte(c.stream), 0o600); err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(dir, "bin")
				if err := os.Mkdir(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				writeStub(t, bin, "glyph", "cat \"$STUB_STREAM\" >&2\nexit \"$STUB_EXIT\"\n")
				output := filepath.Join(dir, "github_output")
				if err := os.WriteFile(output, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				run := runStep(t, script, unspecifiedShell, dir, []string{
					"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"STUB_STREAM=" + stream,
					"STUB_EXIT=" + strconv.Itoa(c.exit),
					"PR_NUMBER=7",
					"DRY_RUN=false",
					"INSTALL_NOTES=",
					"RUNNER_TEMP=" + dir,
					"GITHUB_OUTPUT=" + output,
				})
				if run.exit != c.exit {
					t.Fatalf("the step exited %d, want glyph's %d preserved:\n%s", run.exit, c.exit, run)
				}
				var got []string
				for line := range strings.SplitSeq(run.stdout+run.stderr, "\n") {
					if strings.HasPrefix(line, "::error::") {
						got = append(got, line)
					}
				}
				if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
					t.Errorf("the step's log carries these ::error:: lines:\n  %s\nwant exactly:\n  %s\n"+
						"each refused commit annotated once, by glyph's own line as glyph wrote it; "+
						"the frame only where glyph annotated nothing\n%s",
						strings.Join(got, "\n  "), strings.Join(c.want, "\n  "), run)
				}
			})
		}
	}
}
