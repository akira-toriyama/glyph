package workflows

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// artifactMode is how release.yml spells "this run has an artefact to build",
// for the steps that serve BOTH artefact shapes. The two build steps are
// guarded more narrowly still (`inputs.app != ”` / `inputs.binary != ”`),
// which is why the invariant below is stated over the inputs rather than over
// this exact string: what matters is that a macOS step's CONDITION constrains
// on the artefact inputs at all, because a caller that passes neither — the
// verdict-only mode the live-fire range uses — lands on an ubuntu runner where
// none of the macOS half exists.
const artifactMode = "inputs.app != '' || inputs.binary != ''"

// artifactInputs are the two names a macOS step's `if:` must mention. Checked
// against the CONDITION and not the step body: every build step already names
// `inputs.app` in its `env:`, so a body-wide search would pass for a step with
// no condition at all — the exact vacuity this guard exists to prevent.
var artifactInputs = []string{"inputs.app", "inputs.binary"}

// stepIf pulls a step's `if:` condition out of its block. The reusables write
// conditions on one line; a step with none yields "".
var stepIf = regexp.MustCompile(`(?m)^\s*if:\s*(.+)$`)

func stepCondition(block string) string {
	m := stepIf.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return m[1]
}

// macOSOnly are tokens that only make sense on the macOS runner: the Xcode
// setup, the SwiftPM cache, the two build scripts, and the asset upload that
// has nothing to upload without them. A step carrying one of these and no
// artifact-mode condition is a step that will run on ubuntu and fail there —
// or, in the upload's case, fail on a glob that never expands.
var macOSOnly = []string{
	"maxim-lobanov/setup-xcode",
	"org.swift.swiftpm",
	"./package.sh",
	"./build.sh",
	"gh release upload",
}

// bareRunner matches a `runs-on:` whose value is a literal runner label rather
// than an expression. release.yml must not have one: the runner is decided
// from the artefact inputs, and a literal would put every verdict-only run on
// a macOS host to compute a version number.
var bareRunner = regexp.MustCompile(`(?m)^\s*runs-on:\s*[a-z]`)

// stepBlocks splits a workflow's steps on the list-item indent the reusables
// use, so a condition can be attributed to the step it governs rather than to
// the file. No YAML library: this package has none by design, and the shape it
// reads is stable — the reusables are hand-written and uniformly indented.
func stepBlocks(body string) []string {
	parts := strings.Split(body, "\n      - ")
	if len(parts) < 2 {
		return nil
	}
	return parts[1:]
}

// TestVerdictOnlyStepsSkipTheMacOSHalf is the invariant that keeps the
// verdict-only mode working as release.yml grows. The mode exists so
// glyph-test can call THIS reusable instead of a hand copy of it (t-kcm4);
// the hand copy it replaces had drifted three glyph releases behind the
// workflows around it, so the range was firing a different gun from the fleet.
//
// The failure this catches is quiet in the worst way: a new Swift-side step
// added without the condition is invisible to every artefact-shipping caller
// (they are on macOS and it works) and breaks only the one repo whose job is
// to notice breakage first.
func TestVerdictOnlyStepsSkipTheMacOSHalf(t *testing.T) {
	// Positive control. A guard that asserts a condition is PRESENT dies green
	// if the splitter stops finding steps, so prove on a synthetic block that
	// an unguarded macOS step is actually detected.
	canary := "name: Build app + zip\n        run: |\n          ./package.sh\n"
	if !unguarded(canary) {
		t.Fatal("the detector no longer flags a macOS step with no artifact-mode condition — every case below would pass vacuously")
	}
	// The vacuity trap in its own right: a step that names inputs.app only in
	// its env is NOT guarded, and a body-wide search would have said it was.
	envOnly := "name: Build app + zip\n        env:\n          APP: ${{ inputs.app }}\n        run: |\n          ./package.sh\n"
	if !unguarded(envOnly) {
		t.Fatal("the detector accepts a step whose only mention of the artefact inputs is in its env — it is reading the body, not the condition")
	}
	for _, guarded := range []string{
		"name: Build app + zip\n        if: " + artifactMode + "\n        run: |\n          ./package.sh\n",
		"name: Build app + zip\n        if: steps.verdict.outputs.release == 'true' && inputs.app != ''\n        run: |\n          ./package.sh\n",
	} {
		if unguarded(guarded) {
			t.Fatalf("the detector flags a correctly guarded step — it would fail the real file for the wrong reason:\n%s", guarded)
		}
	}

	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))
	blocks := stepBlocks(body)
	if len(blocks) == 0 {
		t.Fatal("no steps found in release.yml — the splitter's indent assumption broke, and every case below would pass vacuously")
	}
	seen := 0
	for _, b := range blocks {
		if !hasMacOSToken(b) {
			continue
		}
		seen++
		if unguarded(b) {
			t.Errorf("this step needs the macOS runner but its condition (%q) does not constrain on "+
				"inputs.app/inputs.binary, so a verdict-only caller (neither) runs it on ubuntu:\n\t- %s",
				stepCondition(b), strings.TrimRight(firstLines(b, 3), "\n"))
		}
	}
	// Every token above names a step that exists today. If none matched, the
	// steps were renamed or the tokens went stale, and this test is asserting
	// nothing.
	if seen < len(macOSOnly) {
		t.Errorf("only %d of %d macOS-only tokens matched a step — the token list has gone stale and "+
			"this guard is checking less than it claims", seen, len(macOSOnly))
	}
}

// TestReleaseRunnerFollowsTheArtifactInputs pins the other half: the steps may
// be guarded perfectly and the job still burn a macOS runner for a repo that
// builds nothing.
func TestReleaseRunnerFollowsTheArtifactInputs(t *testing.T) {
	const canary = "    runs-on: macos-26"
	if !bareRunner.MatchString(canary) {
		t.Fatalf("bareRunner no longer matches a literal runner (%q) — the case below would pass vacuously", canary)
	}
	if bareRunner.MatchString("    runs-on: ${{ inputs.app != '' && 'macos-26' || 'ubuntu-latest' }}") {
		t.Fatal("bareRunner matches an expression form — it would fail the real file for the wrong reason")
	}

	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))
	if bareRunner.MatchString(body) {
		t.Error("release.yml pins a literal runner, so a verdict-only caller — one that builds nothing and " +
			"needs no Xcode — spends the family's scarcest minutes to compute a version number. The runner " +
			"is decided from the artefact inputs, in the job's runs-on expression")
	}
	if !strings.Contains(body, artifactMode) {
		t.Errorf("release.yml no longer mentions %q anywhere — the verdict-only mode is gone, "+
			"and with it the range's ability to fire the real reusable", artifactMode)
	}
}

// TestArtifactArityAcceptsNeither runs the validation step's own shell rather
// than reading it. "At most one" and "exactly one" differ by a single boolean
// and read almost identically in a diff; the only honest check is to feed the
// script the four input combinations and look at what it does.
func TestArtifactArityAcceptsNeither(t *testing.T) {
	script := extractRun(t, repoFile(t, filepath.Join(".github", "workflows", "release.yml")), "Validate the artifact inputs")

	cases := []struct {
		name       string
		app, bin   string
		wantExit   int
		wantOutput string
	}{
		{"app only", "Facet", "", 0, ""},
		{"binary only", "", "glance", 0, ""},
		{"neither is verdict-only", "", "", 0, "::notice::verdict-only"},
		{"both is still an error", "Facet", "glance", 1, "::error::"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "APP="+c.app, "BINARY="+c.bin)
			out, err := cmd.CombinedOutput()
			exit := 0
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				exit = ee.ExitCode()
			case err != nil:
				t.Fatalf("running the validation step: %v", err)
			}
			if exit != c.wantExit {
				t.Errorf("app=%q binary=%q exited %d, want %d\n%s", c.app, c.bin, exit, c.wantExit, out)
			}
			if c.wantOutput != "" && !strings.Contains(string(out), c.wantOutput) {
				t.Errorf("app=%q binary=%q printed no %q — the mode is silent about itself:\n%s",
					c.app, c.bin, c.wantOutput, out)
			}
		})
	}
}

func hasMacOSToken(block string) bool {
	for _, tok := range macOSOnly {
		if strings.Contains(block, tok) {
			return true
		}
	}
	return false
}

// unguarded reports a step that needs macOS but whose CONDITION does not
// constrain on the artefact inputs — so a verdict-only run, which is on
// ubuntu, would execute it.
func unguarded(block string) bool {
	if !hasMacOSToken(block) {
		return false
	}
	cond := stepCondition(block)
	for _, in := range artifactInputs {
		if strings.Contains(cond, in) {
			return false
		}
	}
	return true
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// extractRun pulls the shell out of the named step's `run: |` block and
// dedents it, so a test can execute exactly what the runner executes. A step
// that cannot be found is a hard failure: a silently empty script would make
// every case below pass.
func extractRun(t *testing.T, body, stepName string) string {
	t.Helper()
	i := strings.Index(body, "name: "+stepName)
	if i < 0 {
		t.Fatalf("step %q not found in the workflow — this test is asserting nothing", stepName)
	}
	rest := body[i:]
	j := strings.Index(rest, "run: |")
	if j < 0 {
		t.Fatalf("step %q has no `run: |` block", stepName)
	}
	const indent = "          " // ten spaces: a reusable's step-script body
	var out []string
	for _, line := range strings.Split(rest[j+len("run: |"):], "\n")[1:] {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		if !strings.HasPrefix(line, indent) {
			break
		}
		out = append(out, strings.TrimPrefix(line, indent))
	}
	script := strings.Join(out, "\n")
	if strings.TrimSpace(script) == "" {
		t.Fatalf("step %q yielded an empty script — the indent assumption broke", stepName)
	}
	return script
}
