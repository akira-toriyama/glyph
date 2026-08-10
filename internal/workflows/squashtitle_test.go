package workflows

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLintGateAlsoJudgesTheSquashTitle pins the second half of the lint gate:
// the squash title. A squash merge records the PR title as the subject of the
// commit that lands on main — CONTRIBUTING ratifies that title as a commit
// subject — and the BASE..HEAD range can never see it, because the range holds
// the pre-squash commits while the title exists as no commit at all until the
// squash mints it. Measured across the fleet before the step existed, 158
// landed subjects had failed the grammar this way, each one read by the
// release walk's fallback as a silent none. Losing this step does not red
// anything: the range job stays green and the hole simply reopens, which is
// why the mutation ledger re-breaks it by name.
func TestLintGateAlsoJudgesTheSquashTitle(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "lint.yml"))
	body := code(raw)

	if !strings.Contains(body, `glyph lint --pr "$PR_NUMBER"`) {
		t.Errorf("lint.yml no longer runs `glyph lint --pr` — the squash title is back to being " +
			"the one merge-candidate subject no gate checks, and its violations resurface only " +
			"as silently dropped bumps in the next release walk")
	}
	if !strings.Contains(body, "if: github.event_name == 'pull_request'") {
		t.Errorf("the squash-title step is no longer gated to pull_request events — a push " +
			"event has no pull_request.number, so the step would refuse every direct push " +
			"the push arm exists to annotate")
	}
	if !strings.Contains(body, "PR_NUMBER: ${{ github.event.pull_request.number }}") {
		t.Errorf("the squash-title step no longer reads the PR number from the event payload — " +
			"whatever it lints now, it is not this pull request's title")
	}

	// The read needs a token permission the range lint never did, in BOTH
	// halves of this file: the executable grant (a reusable can only downgrade
	// the caller's token) and the commented caller stub that fleet-sync's
	// canonical copy is written from — a stub without the grant distributes a
	// caller whose title step 403s on every private repository.
	if !strings.Contains(body, "pull-requests: read") {
		t.Errorf("lint.yml's permissions no longer include pull-requests: read — the squash-title " +
			"read 403s (exit 4, a loud infra failure on every pull request) the moment the " +
			"caller's token is scoped down to match")
	}
	if !strings.Contains(raw, "#     pull-requests: read") {
		t.Errorf("the commented caller stub no longer grants pull-requests: read — the stub is " +
			"what the fleet's canonical caller is written from, and a caller granting only " +
			"contents: read hands this reusable a token whose title read 403s")
	}
}
