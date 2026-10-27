package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
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
	assertParsedTrailerDemand(t, "scripts/dist-gate.sh", script, "Dist-gate-exempt")
}

// assertParsedTrailerDemand holds a range gate to the trailer rule both gates
// share through scripts/lib/range-gate.sh: the gate sources the library and
// demands its own key from it, and the library reads that key out of `git
// interpret-trailers --parse` rather than out of the raw message.
func assertParsedTrailerDemand(t *testing.T, name, script, key string) {
	t.Helper()
	body := code(script)
	for _, want := range []string{
		". ./scripts/lib/range-gate.sh",
		"require_trailer_on_every_commit " + key + " ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s no longer runs %q; the %s demand is either gone or no longer goes "+
				"through the shared trailer rule", name, want, key)
		}
	}
	lib := code(repoFile(t, filepath.Join("scripts", "lib", "range-gate.sh")))
	for _, want := range []string{"git interpret-trailers --parse", `grep "^$1:"`} {
		if !strings.Contains(lib, want) {
			t.Errorf("scripts/lib/range-gate.sh no longer contains %q; the trailer rule has "+
				"degraded into a substring match a body paragraph can satisfy", want)
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

// pinnedGoreleaserAction is goreleaser.yml's real goreleaser-action step, the
// positive control the stand-down below is proven against: the indent with
// `uses: `, the SHA, and the `# vX.Y.Z` comment.
var pinnedGoreleaserAction = regexp.MustCompile(`(?m)^(\s*uses: )goreleaser/goreleaser-action@([0-9a-f]{40})( # v\S+)$`)

// firstStepName is a glyph-owned line of the same file, edited in place to
// take the shape of a real :bug: fix.
var firstStepName = regexp.MustCompile(`(?m)^\s*- name: .*$`)

// TestDistGateStandsDownForAPinSwapAlone runs the gate over a fixture
// repository, because what it holds is a judgement about a diff, which no text
// assertion over the script can make; the rule and the incident behind it are
// scripts/dist-gate.sh's header. The positive control is goreleaser.yml's real
// goreleaser-action line bumped the way dependabot writes it; the in-place
// edit is the shape a real :bug: fix takes (#306 touched goreleaser.yml's
// body). The fixture's copy carries one extra line, a `run:` that installs a
// tool at a SHA, so that a SHA swap on a line that is not a `uses:` has
// something to swap.
func TestDistGateStandsDownForAPinSwapAlone(t *testing.T) {
	testutil.RequireGit(t)
	sh := requireTool(t, "sh")

	const gated = ".github/workflows/goreleaser.yml"
	workflow := repoFile(t, gated)
	m := pinnedGoreleaserAction.FindStringSubmatch(workflow)
	if m == nil {
		t.Fatalf("%s no longer carries a SHA-pinned goreleaser/goreleaser-action step; the "+
			"positive control needs a real pinned `uses:` line — point this test at another", gated)
	}
	step, indent, sha, comment := m[0], m[1], m[2], m[3]
	other := strings.Repeat("0123456789", 4)
	nameLine := firstStepName.FindString(workflow)
	if nameLine == "" {
		t.Fatalf("%s has no `- name:` line to edit in place", gated)
	}
	install := "      - run: go install example.com/tool@" + sha + "\n"
	original := workflow + install
	swap := func(to string) string { return strings.Replace(original, step, to, 1) }
	edit := func(in string) string { return strings.Replace(in, nameLine, nameLine+" (edited)", 1) }
	bumped := swap(indent + "goreleaser/goreleaser-action@" + other + " # v9.9.9")

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		return testutil.Git(t, dir, "akira-toriyama", args...)
	}
	git("init", "-q", "-b", "main")
	git("config", "core.fileMode", "true")
	for _, rel := range []string{"scripts/dist-gate.sh", "scripts/lib/range-gate.sh"} {
		testutil.WriteFile(t, dir, rel, repoFile(t, rel))
	}
	testutil.WriteFile(t, dir, gated, original)
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
	}

	for _, tc := range []struct {
		name       string
		after      string // the gated file as the pull request leaves it
		executable bool   // a mode change: the diff has no changed line
		renameTo   string // the gated file moved to this path instead
		exit       int
		carries    string // on stdout for a stand-down, on stderr for a refusal
	}{
		{name: "dependabot bumps the pin", after: bumped, carries: "goreleaser/goreleaser-action"},
		{name: "the pin comment alone", after: swap(indent + "goreleaser/goreleaser-action@" + sha + " # v9.9.9"), carries: "goreleaser/goreleaser-action"},
		{name: "a step name edited in place", after: edit(original), exit: 1, carries: "::error::dist-gate"},
		{name: "a bump riding with an edit", after: edit(bumped), exit: 1, carries: "::error::dist-gate"},
		{name: "the action swapped for another", after: swap(indent + "akira-toriyama/goreleaser-action@" + other + comment), exit: 1, carries: "::error::dist-gate"},
		{name: "the pin moved to a tag", after: swap(indent + "goreleaser/goreleaser-action@v7"), exit: 1, carries: "::error::dist-gate"},
		{name: "the step moved", after: strings.Replace(original, step+"\n", "", 1) + step + "\n", exit: 1, carries: "::error::dist-gate"},
		{name: "the step re-indented", after: swap(" " + indent + "goreleaser/goreleaser-action@" + other + comment), exit: 1, carries: "::error::dist-gate"},
		{name: "a pinned step added", after: original + "      - uses: actions/setup-node@" + other + "\n", exit: 1, carries: "::error::dist-gate"},
		{name: "a run: line's SHA swapped", after: strings.Replace(original, install, "      - run: go install example.com/tool@"+other+"\n", 1), exit: 1, carries: "::error::dist-gate"},
		{name: "no changed line", after: original, executable: true, exit: 1, carries: "::error::dist-gate"},
		{name: "the file renamed out of the gate", renameTo: ".github/workflows/goreleaser-old.yml", exit: 1, carries: "::error::dist-gate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git("checkout", "-q", "--detach", base)
			if tc.renameTo != "" {
				git("mv", gated, tc.renameTo)
			} else {
				testutil.WriteFile(t, dir, gated, tc.after)
			}
			if tc.executable {
				if err := os.Chmod(filepath.Join(dir, gated), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			git("commit", "-q", "-a", "-m", "change")
			run := execute(t, []string{sh, filepath.Join(dir, "scripts", "dist-gate.sh"), base}, dir, env)
			if run.exit != tc.exit {
				t.Fatalf("dist-gate exited %d, want %d\n%s", run.exit, tc.exit, run)
			}
			stream := run.stdout
			if tc.exit != 0 {
				stream = run.stderr
			}
			if !strings.Contains(stream, tc.carries) {
				t.Errorf("dist-gate's verdict does not carry %q\n%s", tc.carries, run)
			}
			if tc.exit == 0 && run.stderr != "" {
				t.Errorf("a stand-down wrote to stderr — the shell or awk complained on the way to 0\n%s", run)
			}
		})
	}
}
