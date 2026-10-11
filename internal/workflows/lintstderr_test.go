package workflows

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLintReplaysGlyphStderrBeforeTheGreenExit pins the order of two lines in
// each lint step of lint.yml: glyph's stderr is replayed to the log BEFORE the
// step decides that exit 0 means "all commits OK" / "title OK" and returns.
//
// The binary writes every diagnostic onto that stream — one `::error::` per
// finding, but also, on a GREEN run, one `::warning::` per warn-pattern commit
// and the "nothing linted" warning when the range held no commit to judge. The
// step used to replay the stream only on the failure arm, so every green-run
// warning was dropped fleet-wide: DESIGN §2 promises a warn annotation at every
// gate ("a warning loud at one gate and silent at another teaches the reader
// that the loud gate is noise"), and the binary's own tests pin the warnings,
// yet CI showed none — measured live on glyph-test#91 (a window-pattern commit
// linted green with no annotation) and dotfiles#393 (a bot-only range with no
// "nothing linted" line, run 36304487152). pr-verdict.yml's compose step has
// always replayed its stream unconditionally; lint.yml was the odd one.
//
// The assertion is structural — the `cat` must come before the `-eq 0` test in
// the same step — because the anchors are the step's own executable lines and
// the failure this guards is a reordering, not a deletion. Both anchors are
// positive controls: each must exist exactly once per step, so a step that
// dropped the `cat` altogether fails here rather than passing vacuously.
func TestLintReplaysGlyphStderrBeforeTheGreenExit(t *testing.T) {
	body := lintBody(t)
	for _, step := range []struct{ name, errFile, ok string }{
		{"range", `"$RUNNER_TEMP/glyph-lint.err"`, `echo "all commits OK"`},
		{"title", `"$RUNNER_TEMP/glyph-title.err"`, `echo "title OK"`},
	} {
		replay := "cat " + step.errFile + " >&2"
		green := `if [ "$status" -eq 0 ]; then` + "\n" + strings.Repeat(" ", 12) + step.ok
		ri, gi := strings.Index(body, replay), strings.Index(body, green)
		if ri < 0 || strings.Count(body, replay) != 1 {
			t.Fatalf("%s step: expected exactly one %q in lint.yml's executable body (found %d) — "+
				"the replay is what puts glyph's own annotations in the log", step.name, replay, strings.Count(body, replay))
		}
		if gi < 0 || strings.Count(body, green) != 1 {
			t.Fatalf("%s step: expected exactly one green exit (%q followed by %q) in lint.yml", step.name, `if [ "$status" -eq 0 ]; then`, step.ok)
		}
		if ri > gi {
			t.Errorf("%s step: glyph's stderr is replayed (%q) only AFTER the green exit (%q) — a warn-pattern "+
				"commit and the \"nothing linted\" warning would lint green with no annotation, the fleet-wide "+
				"silence glyph-test#91 and dotfiles#393 measured", step.name, replay, step.ok)
		}
	}
}

// TestLintKeepsGlyphsStreamInRunnerTemp pins where both lint steps keep
// glyph's stderr and the envelope sieved from it: under $RUNNER_TEMP, the
// directory each job has to itself. They used fixed /tmp paths, which were
// private only while every job ran on a fresh hosted VM; the `runner` input
// (v4.3.0) lets a caller put the job on its own host, where runner instances
// share /tmp, so two concurrent lint jobs could overwrite each other's stream
// and annotate one pull request with another's findings, and the files
// outlive the job. pr-verdict.yml, release.yml, goreleaser.yml and the
// install action already wrote under $RUNNER_TEMP. Positive control: each
// step's script must name "$RUNNER_TEMP/glyph-<step>. itself, so a /tmp/
// count of zero is over the script that writes the files.
func TestLintKeepsGlyphsStreamInRunnerTemp(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(lintWorkflow))
	for _, step := range []struct{ name, prefix string }{
		{lintRangeStep, `"$RUNNER_TEMP/glyph-lint.`},
		{"Lint the squash title", `"$RUNNER_TEMP/glyph-title.`},
	} {
		script := extractRun(t, raw, step.name)
		if !strings.Contains(script, step.prefix) {
			t.Fatalf("the %q step names no %s… file — either it stopped keeping glyph's stream in a "+
				"file, or this test reads the wrong script and the check below proves nothing", step.name, step.prefix)
		}
		if n := strings.Count(script, "/tmp/"); n != 0 {
			t.Errorf("the %q step names /tmp/ %d time(s) — a self-hosted runner shares /tmp across "+
				"concurrent jobs, so the step's files belong under \"$RUNNER_TEMP\"", step.name, n)
		}
	}
}
