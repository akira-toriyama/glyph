package workflows

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGoreleaserIsToldWhichTagItReleases: goreleaser.yml's release step must
// carry GORELEASER_CURRENT_TAG from the ref that triggered the run. Left
// unset, GoReleaser infers the tag with `git describe`, and a commit that
// carries two tags — a release cut on the same tree as its last rc, which is
// the ordinary shape here — makes that a coin toss. Measured 2026-09-10: the
// v3.3.0 push built 3.3.0-rc.2 artifacts and tried to upload them onto the
// rc.2 release (422 already_exists); v3.3.0 had no release while every pin
// site was about to move to it. Positive control: the step exists and has an
// env block at all, so the assertion is about a line inside a real block.
func TestGoreleaserIsToldWhichTagItReleases(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml")))
	i := strings.Index(body, "name: GoReleaser\n")
	if i < 0 {
		t.Fatal("goreleaser.yml has no step named GoReleaser — the release step was renamed and this guard is asserting nothing")
	}
	step := body[i:]
	if j := strings.Index(step, "\n      - "); j >= 0 {
		step = step[:j]
	}
	if !strings.Contains(step, "env:") {
		t.Fatal("the GoReleaser step has no env block; the guard's premise (the tag is passed through env) needs re-deriving")
	}
	if !strings.Contains(step, "GORELEASER_CURRENT_TAG: ${{ github.ref_name }}") {
		t.Errorf("the GoReleaser step does not pass GORELEASER_CURRENT_TAG from github.ref_name — on a commit "+
			"carrying two tags GoReleaser guesses with git describe and can release the wrong one:\n%s", step)
	}
}
