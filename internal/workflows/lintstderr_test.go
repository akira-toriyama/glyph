package workflows

import (
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
		{"range", "/tmp/glyph-lint.err", `echo "all commits OK"`},
		{"title", "/tmp/glyph-title.err", `echo "title OK"`},
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
