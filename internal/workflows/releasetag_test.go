package workflows

import (
	"path/filepath"
	"strings"
	"testing"
)

// releaseTagEnv is the one line each build step must carry: the tag the
// verdict decided, handed down to the caller's build script.
const releaseTagEnv = "RELEASE_TAG: ${{ steps.verdict.outputs.tag }}"

// buildSteps are the two artefact shapes release.yml builds. Both are listed
// because the shape a caller ships must not decide whether its build is told
// the tag.
var buildSteps = []string{"Build app + zip", "Build binary + sha256"}

// TestReleaseBuildStepsAreToldWhichTagTheyBuild: the draft's tag is not a git
// ref until a human publishes it, so a `git describe` inside the caller's
// package.sh can only ever name the PREVIOUS release. Measured 2026-09-11:
// facet's draft for v7.0.2 carried an app printing 7.0.1-40-g9d6e6c4, and the
// published v7.0.1 ships one printing 7.0.0-42-g198ef4a — every family release
// so far has shipped an artefact naming its predecessor. The fix is the shape
// a5e3fa3 ratified for GoReleaser (TestGoreleaserIsToldWhichTagItReleases):
// state the tag, never infer it.
//
// Read through code(), so a commented-out env line cannot satisfy the
// assertion — the justifying comment quotes the variable and would otherwise
// pass for the contract it describes.
func TestReleaseBuildStepsAreToldWhichTagTheyBuild(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))

	for _, name := range buildSteps {
		i := strings.Index(body, "name: "+name+"\n")
		if i < 0 {
			t.Fatalf("release.yml has no step named %q — it was renamed and this guard is asserting nothing", name)
		}
		step := body[i:]
		if j := strings.Index(step, "\n      - "); j >= 0 {
			step = step[:j]
		}
		// Positive control: the narrowing really narrowed. Uncut, the slice is
		// the rest of the file and the OTHER build step's env line would
		// satisfy every assertion below.
		for _, other := range buildSteps {
			if other != name && strings.Contains(step, "name: "+other) {
				t.Fatalf("the step slice for %q still contains %q — the boundary cut no longer bites and "+
					"one step's env would answer for both", name, other)
			}
		}
		// Positive control: the premise is that the tag travels through env.
		if !strings.Contains(step, "env:") {
			t.Fatalf("the %q step has no env block; this guard's premise (the tag is handed down through "+
				"env) needs re-deriving", name)
		}
		if !strings.Contains(step, releaseTagEnv) {
			t.Errorf("the %q step is not told which tag it builds (no %q) — the draft's tag is no git ref "+
				"until a human publishes, so the build's own `git describe` names the PREVIOUS release and "+
				"the artefact hanging off the draft misreports its version:\n%s", name, releaseTagEnv, step)
		}
	}
}

// TestReleaseRefusesArtifactInputsBeforeEitherBuildStep: RELEASE_TAG is "" on
// a packages repository — the envelope has no scalar tag — so the value the
// build steps now receive is only safe because neither step can run there.
// The refusal is what makes that true, and only its position relative to
// `Build app + zip` was pinned (TestReleaseRefusesArtifactInputsOnAPackages-
// Repository); the binary arm had no such assertion, and a step reordered
// below it would hand an empty string down as a version.
//
// bite-exempt: pins current behaviour, not a fix — the refusal already sits
// above both build steps, and what changed is that RELEASE_TAG now depends on
// it. The invariant had no defender for the binary arm.
func TestReleaseRefusesArtifactInputsBeforeEitherBuildStep(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))
	const refuse = "name: Refuse an artifact input on a packages repository"
	ri := strings.Index(body, refuse)
	if ri < 0 {
		t.Fatalf("release.yml has no step %q — the refusal was renamed and this guard is asserting nothing", refuse)
	}
	for _, name := range buildSteps {
		bi := strings.Index(body, "name: "+name+"\n")
		if bi < 0 {
			t.Fatalf("release.yml has no step named %q — it was renamed and this guard is asserting nothing", name)
		}
		if ri >= bi {
			t.Errorf("the packages refusal (%d) does not precede %q (%d): on a packages repository the "+
				"verdict's scalar tag is \"\", so a build step reachable there is handed an empty string "+
				"as the version it stamps", ri, name, bi)
		}
	}
}
