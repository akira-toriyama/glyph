package workflows

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// release.yml's caller-facing outputs (t-c0c5). pr-verdict.yml grew outputs so
// callers stop re-deriving a verdict the job already computed; release.yml is
// the other reusable with a verdict, and the same argument lands the same four
// names — level, next, current, action. The guards here hold the three layers
// of workflow_call plumbing together (declaration → job → step), because each
// layer defaults to "" in silence: a renamed step id or a dropped job mapping
// does not fail the run, it just hands every caller empty strings that the
// contract says mean NOT COMPUTED — a wiring bug wearing the contract's own
// escape hatch as a disguise.
//
// The one output deliberately ABSENT is the draft's URL: with the API handle
// in hand, auto-publishing the draft is a two-line caller step, and the human
// act of publishing (and therefore cutting the tag) is the safety net §4/§6
// rest on.

// releaseOutputs are the four names, in declaration order.
var releaseOutputs = []string{"level", "next", "current", "action"}

// TestReleaseOutputsAreWiredThroughAllThreeLayers: for each name, the
// workflow_call declaration maps to the release job, the job maps to the
// verdict step, and the step body WRITES the name on both answer arms — the
// none verdict included, because a caller gating "did nothing ship" needs the
// none answer as much as the release one, and an arm that forgets to write
// leaves "" claiming the verdict was never computed when it was.
func TestReleaseOutputsAreWiredThroughAllThreeLayers(t *testing.T) {
	body := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	for _, name := range releaseOutputs {
		if want := fmt.Sprintf("value: ${{ jobs.release.outputs.%s }}", name); !strings.Contains(body, want) {
			t.Errorf("release.yml's workflow_call outputs do not declare %q (want %q) — callers cannot "+
				"see a verdict the run computes", name, want)
		}
		if want := fmt.Sprintf("%s: ${{ steps.verdict.outputs.%s }}", name, name); !strings.Contains(body, want) {
			t.Errorf("release.yml's job does not map output %q from the verdict step (want %q) — the "+
				"declaration above it reads an unset job output as \"\", silently", name, want)
		}
	}

	// The step writes each output on BOTH arms. The none arm and the release
	// arm each carry one write per name: `emit <name>` for the envelope-read
	// values, a literal `next=` for the tag (empty on none, the tag on
	// release). Counting writes rather than parsing arms keeps the guard
	// robust to reshuffling inside the step while still failing when one arm
	// loses a write.
	for _, name := range []string{"level", "current", "action"} {
		if got := strings.Count(body, "emit "+name); got != 2 {
			t.Errorf("the verdict step writes %q %d time(s), want 2 (the none arm and the release arm) — "+
				"the arm that lost its write answers \"\" and the contract reads that as NOT COMPUTED", name, got)
		}
	}
	if got := strings.Count(body, `echo "next=`); got != 2 {
		t.Errorf("the verdict step writes next= %d time(s), want 2 (empty on the none arm, the tag on "+
			"the release arm)", got)
	}
}

// releaseOutputDecl matches an output NAME declared under release.yml's
// workflow_call outputs block — the two-space-deeper key line above a
// `description:`/`value:` pair. Used to assert an ABSENCE, so it carries a
// positive control in the test below.
var releaseOutputDecl = regexp.MustCompile(`(?m)^      (url|html_url):\s*$`)

// TestReleaseOutputsNeverExposeTheDraftURL: the absence that is the decision.
// The run demonstrably HAS the URL — the notice step reads `.url` from the
// same envelope — so not exposing it is a choice, and this guard is what makes
// the choice survivable: an output added later under either obvious name goes
// red here with the reason, instead of quietly arming every caller with the
// two-line auto-publish §4/§6 exist to prevent.
func TestReleaseOutputsNeverExposeTheDraftURL(t *testing.T) {
	body := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))

	// Positive control 1: the regex still recognises a declaration line of the
	// forbidden shape, on a synthetic instance.
	if releaseOutputDecl.FindString("      url:\n") == "" {
		t.Fatal("releaseOutputDecl no longer matches an output declaration line; the absence check " +
			"below would pass vacuously — re-derive it from the outputs block's indentation")
	}
	// Positive control 2: the value being withheld is really there to withhold
	// — the workflow reads the draft's url out of the very same envelope.
	if !strings.Contains(body, `jq -r '.url // empty'`) {
		t.Fatal("release.yml no longer reads the draft url from the verdict envelope; this guard's " +
			"premise (the url exists and is deliberately not an output) needs re-deriving")
	}

	if m := releaseOutputDecl.FindString(body); m != "" {
		t.Errorf("release.yml declares %q as an output — the draft's API handle makes auto-publish a "+
			"two-line caller step, and publishing (cutting the tag) must stay a human act (§4/§6)",
			strings.TrimSpace(m))
	}
}
