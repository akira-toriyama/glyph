package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// distPattern is dist-gate.sh's OWN classification pattern, copied verbatim
// from the DIST_PATTERN assignment in scripts/dist-gate.sh. Go's RE2 accepts
// the ERE unchanged, so this is the same matcher byte for byte —
// TestDistGateGuardsTheDistributionLayer asserts the byte-for-byte part, so the
// two copies cannot drift apart silently.
const distPattern = `^(\.goreleaser\.yaml$|\.github/actions/|\.github/workflows/(lint|pr-verdict|release|goreleaser)\.yml$)`

// evidencePattern is the companion copy of EVIDENCE_PATTERN: the change that
// satisfies the gate.
const evidencePattern = `^internal/workflows/.*_test\.go$`

// TestDistGateGuardsTheDistributionLayer pins the gate that makes evidence for
// a distribution-layer change mandatory rather than discretionary (t-5dbh):
// before it, a :bug: fix touching only .goreleaser.yaml or only the install
// action reached main with zero tests — `bite` acts on Go diffs and the extras
// smoke reads no YAML, so nothing even noticed. The gate is one job and one
// script that no Go code compiles, which is exactly the shape this package
// exists to guard.
func TestDistGateGuardsTheDistributionLayer(t *testing.T) {
	build := code(repoFile(t, filepath.Join(".github", "workflows", "build.yml")))

	// The job must exist, run the script, and hand it the base the diff needs.
	for _, want := range []string{
		"sh scripts/dist-gate.sh",
		"github.event.pull_request.base.sha",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build.yml no longer contains %q; without the dist-gate job a "+
				"workflow/action/goreleaser change ships with zero tests and nothing notices "+
				"— the measured gap the job closed", want)
		}
	}

	// ...on a full checkout: the gate diffs BASE...HEAD and walks trailers, and
	// a depth-1 checkout holds neither. build.yml carried no `fetch-depth: 0`
	// before this job, so the whole-file match is the job's own.
	if !fetchDepthFull.MatchString(build) {
		t.Error("build.yml has no `fetch-depth: 0` left; the dist-gate checkout is shallow, " +
			"so the base does not resolve and the gate fails every pull request it should judge")
	}

	script := repoFile(t, filepath.Join("scripts", "dist-gate.sh"))

	// The two pattern copies must agree byte for byte, or the coverage this test
	// proves below is the coverage of the WRONG pattern.
	if !strings.Contains(script, "DIST_PATTERN='"+distPattern+"'") {
		t.Errorf("scripts/dist-gate.sh's DIST_PATTERN differs from this test's copy (%q); "+
			"edit them together — the canary and non-vacuity checks here are only evidence "+
			"about the pattern the script actually runs", distPattern)
	}
	if !strings.Contains(script, "EVIDENCE_PATTERN='"+evidencePattern+"'") {
		t.Errorf("scripts/dist-gate.sh's EVIDENCE_PATTERN differs from this test's copy (%q); "+
			"edit them together", evidencePattern)
	}

	// The waiver must stay a parsed trailer, not a substring search: a
	// `Dist-gate-exempt:` line inside a body paragraph must waive nothing.
	for _, want := range []string{"git interpret-trailers --parse", "Dist-gate-exempt:"} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/dist-gate.sh no longer contains %q; the waiver is either gone "+
				"or has degraded into a substring match a body paragraph can satisfy", want)
		}
	}
}

// TestDistGatePatternCoversTheRealDistributionLayer is the non-vacuity half:
// every arm of the pattern must still name a file that exists, and the files
// deliberately OUTSIDE the gate must stay outside. A classification regex with
// no canary is how a fleet invariant dies green.
func TestDistGatePatternCoversTheRealDistributionLayer(t *testing.T) {
	dist := regexp.MustCompile(distPattern)

	// Each arm, proven against a real file: an arm matching only paths that no
	// longer exist is a dead arm, and the gate has silently stopped covering
	// what it was written for.
	for _, path := range []string{
		".goreleaser.yaml",
		".github/actions/install/action.yml",
		".github/workflows/lint.yml",
		".github/workflows/pr-verdict.yml",
		".github/workflows/release.yml",
		".github/workflows/goreleaser.yml",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(path))); err != nil {
			t.Errorf("%s does not exist, so the pattern arm covering it is dead — either the "+
				"file moved (update DIST_PATTERN in scripts/dist-gate.sh AND this test) or the "+
				"distribution layer shrank (say so in both)", path)
		}
		if !dist.MatchString(path) {
			t.Errorf("DIST_PATTERN does not match %s — a distribution-layer file the gate "+
				"was written to cover now changes without demanding evidence", path)
		}
	}

	// Negative control: this repo's own CI and the fleet-synced consumers are
	// deliberately outside the gate — their canonical copies live in
	// akira-toriyama/.github and arrive by sync, not by pull request.
	for _, path := range []string{
		".github/workflows/build.yml",
		".github/workflows/commit-lint.yml",
		".github/workflows/task-status.yml",
		"internal/cli/cmd_release.go",
	} {
		if dist.MatchString(path) {
			t.Errorf("DIST_PATTERN matches %s, which is not distribution layer; the gate would "+
				"demand a workflow test for a change no workflow test can express", path)
		}
	}

	// The evidence pattern's own canary: it must accept the tests that satisfy
	// the gate and nothing else in the package.
	evidence := regexp.MustCompile(evidencePattern)
	if !evidence.MatchString("internal/workflows/workflows_test.go") {
		t.Error("EVIDENCE_PATTERN no longer matches internal/workflows/workflows_test.go — " +
			"no change could ever satisfy the gate, and every distribution-layer PR is red")
	}
	if evidence.MatchString("internal/workflows/doc.go") {
		t.Error("EVIDENCE_PATTERN matches doc.go — a non-test edit would count as evidence")
	}
	if evidence.MatchString("internal/cli/release_test.go") {
		t.Error("EVIDENCE_PATTERN matches a test outside internal/workflows — a Go unit test " +
			"would satisfy a gate that demands a workflow assertion")
	}
}
