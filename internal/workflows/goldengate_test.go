package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goldenPattern is golden-gate.sh's OWN classification pattern, copied verbatim
// from the GOLDEN_PATTERN assignment in scripts/golden-gate.sh. Go's RE2
// accepts the ERE unchanged, so this is the same matcher byte for byte —
// TestGoldenGateGuardsTheGoldenFiles asserts the byte-for-byte part, so the two
// copies cannot drift apart silently.
const goldenPattern = `^(docs/gitmoji-table\.md$|.*/testdata/.*\.golden\..*$)`

// TestGoldenGateGuardsTheGoldenFiles pins the gate that makes a golden rewrite
// state its reason (t-3f4s): `go test -update` regenerates a golden from
// whatever the code currently produces, so a rendering bug can arrive looking
// intentional — the gate demands a `Golden-change: <reason>` trailer on every
// non-merge commit of any pull request whose diff touches one. The gate is one
// job and one script that no Go code compiles, which is exactly the shape this
// package exists to guard.
func TestGoldenGateGuardsTheGoldenFiles(t *testing.T) {
	build := code(repoFile(t, filepath.Join(".github", "workflows", "build.yml")))

	// The job's own stanza, not the whole file: dist-gate's checkout also
	// carries `fetch-depth: 0`, so a whole-file match would stay green after
	// THIS job lost its full checkout and the gate silently stopped resolving
	// the base on every pull request it should judge.
	from := strings.Index(build, "golden-gate:")
	if from < 0 {
		t.Fatal("build.yml has no golden-gate job; a golden rewrite ships with no stated reason and nothing notices — the -update hazard the job was added for")
	}
	to := strings.Index(build[from:], "\n  extras:")
	if to < 0 {
		t.Fatal("cannot delimit the golden-gate job stanza (no `extras:` job follows it in build.yml); if the jobs were reordered, update this slice — the assertions below need the stanza alone")
	}
	stanza := build[from : from+to]

	for _, want := range []string{
		"sh scripts/golden-gate.sh",
		"github.event.pull_request.base.sha",
	} {
		if !strings.Contains(stanza, want) {
			t.Errorf("build.yml's golden-gate job no longer contains %q; the gate either "+
				"does not run or judges the wrong diff", want)
		}
	}
	if !fetchDepthFull.MatchString(stanza) {
		t.Error("build.yml's golden-gate job has no `fetch-depth: 0`; the checkout is shallow, " +
			"so the base does not resolve and the gate fails every pull request it should judge")
	}

	script := repoFile(t, filepath.Join("scripts", "golden-gate.sh"))

	// The two pattern copies must agree byte for byte, or the coverage this
	// test proves below is the coverage of the WRONG pattern.
	if !strings.Contains(script, "GOLDEN_PATTERN='"+goldenPattern+"'") {
		t.Errorf("scripts/golden-gate.sh's GOLDEN_PATTERN differs from this test's copy (%q); "+
			"edit them together — the canary and non-vacuity checks here are only evidence "+
			"about the pattern the script actually runs", goldenPattern)
	}

	// The demand must stay a parsed trailer, not a substring search: a
	// `Golden-change:` line inside a body paragraph must assert nothing.
	for _, want := range []string{"git interpret-trailers --parse", "Golden-change:"} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/golden-gate.sh no longer contains %q; the demand is either gone "+
				"or has degraded into a substring match a body paragraph can satisfy", want)
		}
	}
}

// TestGoldenGatePatternCoversTheRealGoldens is the non-vacuity half: the
// pattern must still match every family of real golden files, and the files
// deliberately OUTSIDE the gate must stay outside. A classification regex with
// no canary is how a fleet invariant dies green.
func TestGoldenGatePatternCoversTheRealGoldens(t *testing.T) {
	golden := regexp.MustCompile(goldenPattern)

	// Each family, proven against a real file: an arm matching only paths that
	// no longer exist is a dead arm, and the gate has silently stopped covering
	// what it was written for.
	for _, path := range []string{
		"docs/gitmoji-table.md",
		"internal/notes/testdata/kitchen_sink.golden.md",
		"internal/markdown/testdata/exported-surface.golden.txt",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(path))); err != nil {
			t.Errorf("%s does not exist, so the pattern arm covering it is dead — either the "+
				"file moved (update GOLDEN_PATTERN in scripts/golden-gate.sh AND this test) or "+
				"the golden set shrank (say so in both)", path)
		}
		if !golden.MatchString(path) {
			t.Errorf("GOLDEN_PATTERN does not match %s — a golden the gate was written to "+
				"cover now changes without demanding a stated reason", path)
		}
	}

	// Negative controls. The fleet corpus is deliberately outside: it has no
	// -update on purpose (its test header argues why), and listing it would
	// read as "a trailer makes a hand edit of stored verdicts acceptable". The
	// fuzz corpus is regression inputs, not a format spec. Ordinary sources
	// must never trip a gate about goldens.
	for _, path := range []string{
		"internal/bump/testdata/fleet-corpus.tsv",
		"internal/markdown/testdata/fuzz/FuzzEscapeMentionsNeverLeaksAMention/backslash-eats-the-fence",
		"internal/cli/release_test.go",
	} {
		if golden.MatchString(path) {
			t.Errorf("GOLDEN_PATTERN matches %s, which is not a golden; the gate would demand "+
				"a Golden-change trailer for a change that rewrites no format spec", path)
		}
	}
}
