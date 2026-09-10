package workflows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// release.yml's packages mode (t-dc9e; DESIGN §4.1, "The reusables"): a
// repository that declares [[packages]] keeps one rolling draft per line and
// answers through the `packages` output — one JSON array of per-line verdicts
// — while the four scalars are "" by contract. Two decisions live in the YAML
// alone and nothing in Go would notice their loss, so they are guarded here.

// packagesEmit captures the jq that hands the envelope's array through — the
// verdict step's emit_packages helper. Re-derive it if the helper is reshaped;
// the guard below fails loud rather than vacuously when it stops matching.
var packagesEmit = regexp.MustCompile(`emit_packages\(\) \{ echo "packages=\$\(jq -r '([^']*)'`)

// TestReleasePackagesOutputStripsBodyAndURL: each line's url is withheld for
// the reason TestReleaseOutputsNeverExposeTheDraftURL gives for the scalar one
// — with the handle in hand, auto-publishing a line's draft is a two-line
// caller step, and publishing must stay a human act — and the body because
// the draft already holds it and N bodies could meet the 1 MB output cap.
// Positive control: the notice step reads every line's url out of the same
// envelope, so the value is really there to withhold.
func TestReleasePackagesOutputStripsBodyAndURL(t *testing.T) {
	body := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	if !strings.Contains(body, `.packages[] | select(.url // "" != "")`) {
		t.Fatal("release.yml no longer reads each line's url from the verdict envelope; this guard's " +
			"premise (the per-line url exists and is deliberately not an output) needs re-deriving")
	}
	m := packagesEmit.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("release.yml has no emit_packages helper of the expected shape — the packages output is " +
			"gone, or the helper was reshaped; re-derive packagesEmit before trusting this guard")
	}
	if !strings.Contains(m[1], "del(.body, .url)") {
		t.Errorf("the packages output hands the array through without stripping body and url (jq: %s) — "+
			"a line's url arms every caller with auto-publish, and N bodies meet the 1 MB output cap", m[1])
	}
}

// TestReleaseRefusesArtifactInputsOnAPackagesRepository: `app` / `binary`
// name one artifact for one draft, and a packages repository has one draft
// per line and no line the artifact would belong to. The refusal sits AFTER
// the verdict step — the declaration is read from the envelope, because
// glyph is glyph.toml's only reader and a grep here would fork the grammar —
// and BEFORE the first build step, gated on both the packages output and an
// artifact input so a single-line caller never meets it.
func TestReleaseRefusesArtifactInputsOnAPackagesRepository(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	body := code(raw)
	const (
		refuse  = "name: Refuse an artifact input on a packages repository"
		verdict = "name: Compose the verdict and upsert the rolling DRAFT"
		build   = "name: Build app + zip"
	)
	ri, vi, bi := strings.Index(body, refuse), strings.Index(body, verdict), strings.Index(body, build)
	if ri < 0 || vi < 0 || bi < 0 {
		t.Fatalf("could not find all three steps in release.yml (refuse=%d, verdict=%d, build=%d) — a rename "+
			"moved one and this guard is asserting nothing", ri, vi, bi)
	}
	if vi >= ri || ri >= bi {
		t.Errorf("the packages refusal is out of place (verdict=%d, refuse=%d, build=%d): it must follow the "+
			"verdict (the envelope is where the declaration is read) and precede the build (an artifact "+
			"nothing can attach must not be built)", vi, ri, bi)
	}
	step := body[ri:bi]
	const gate = "if: steps.verdict.outputs.packages != '' && (inputs.app != '' || inputs.binary != '')"
	if !strings.Contains(step, gate) {
		t.Errorf("the packages refusal is not gated as %q — gated wider it refuses the single line's "+
			"callers, gated narrower a monorepo builds an artifact no draft can take", gate)
	}
	script := extractRun(t, raw, "Refuse an artifact input on a packages repository")
	if !strings.Contains(script, "::error::") || !strings.Contains(script, "exit 1") {
		t.Errorf("the packages refusal does not fail the run with an ::error:: annotation — a refusal "+
			"that does not stop the run is a warning nobody reads:\n%s", script)
	}
}
