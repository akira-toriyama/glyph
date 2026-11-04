package workflows

import (
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// goreleaserDefaultCaskSubject is the subject GoReleaser writes to a tap when
// it pushes a cask with no template — what the tap got before the subject
// was written by hand.
const goreleaserDefaultCaskSubject = "Brew cask update for glyph version v4.1.0"

// caskCommitSubject reads the subject the cask push step commits with.
var caskCommitSubject = regexp.MustCompile(`(?m)^\s*commit\s+(?:--quiet\s+)?-m\s+"([^"]*)"\s*$`)

// TestCaskCommitSubjectLintsInTheTap: the commit goreleaser.yml's cask job
// pushes to akira-toriyama/homebrew-tap on every release is judged by the
// tap's own commit-lint, under the gemoji preset. GoReleaser's default
// subject matched no pattern — measured on the tap: 5bb48cd "Brew cask update
// for glyph version v4.1.0" exits 3. The step's subject must lint clean; the
// default subject failing under the same preset is the positive control that
// the preset would catch the regression.
func TestCaskCommitSubjectLintsInTheTap(t *testing.T) {
	script := extractRun(t, goreleaserWorkflow(t), caskPushStep)

	cfg := testutil.Config(t)
	if v := cfg.Lint(goreleaserDefaultCaskSubject, ""); v.OK {
		t.Fatal("GoReleaser's default cask subject lints clean under the gemoji preset — the positive control is dead, so this guard can no longer tell a subject the tap refuses")
	}

	m := caskCommitSubject.FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("the cask push step commits with no `-m \"…\"` this guard can read:\n%s", script)
	}
	subject := strings.ReplaceAll(m[1], "$TAG", "v4.1.0")
	if strings.Contains(subject, "$") {
		t.Fatalf("the subject %q expands a variable this guard does not render — extend the replacer", m[1])
	}
	if v := cfg.Lint(subject, ""); !v.OK {
		t.Errorf("the tap commit subject %q does not lint under the gemoji preset: %s", subject, v.Reason)
	}
}
