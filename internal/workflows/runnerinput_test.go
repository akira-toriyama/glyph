package workflows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runnerInputReusables are the reusables whose one job a caller may move to its
// own runner. release.yml is absent on purpose: its runner is decided from the
// artefact inputs (TestReleaseRunnerFollowsTheArtifactInputs).
var runnerInputReusables = []string{"lint.yml", "pr-verdict.yml"}

// runsOnLine matches every `runs-on:` key in a comment-stripped body, capturing
// its value.
var runsOnLine = regexp.MustCompile(`(?m)^\s*runs-on:[ \t]*(.*)$`)

// runsOnRunnerInput is the only value a runner-input reusable's job may carry.
// The fallback half is for a caller forwarding an unset repository variable,
// which arrives as the empty string.
const runsOnRunnerInput = "${{ inputs.runner || 'ubuntu-latest' }}"

// inputBlock returns the lines under `<name>:` inside workflow_call.inputs —
// the six-space key and its eight-space children — or "" when the reusable
// declares no such input.
func inputBlock(body, name string) string {
	_, rest, ok := strings.Cut(body, "\n      "+name+":\n")
	if !ok {
		return ""
	}
	var b strings.Builder
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "        ") {
			break
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// TestRunnerInputDefaultsToTheHostedImage pins the two halves of the runner
// input: the job runs where the caller says, and a caller that says nothing is
// exactly where it was before the input existed.
//
// Both halves fail quietly. A literal `runs-on` ignores the input, so a
// repository that moved its lint to its own runner keeps spending hosted
// minutes with nothing red anywhere. A default other than ubuntu-latest moves
// every caller in the fleet at the pin move — none of them passes `with:` —
// onto a label most of them have no runner for.
func TestRunnerInputDefaultsToTheHostedImage(t *testing.T) {
	// Positive controls: both readers must find what they are pointed at, or
	// the assertions below pass over nothing.
	const canary = "on:\n  workflow_call:\n    inputs:\n      runner:\n        type: string\n        default: ubuntu-latest\n      other:\n        default: x\njobs:\n  j:\n    runs-on: macos-26\n"
	if got := inputBlock(canary, "runner"); !strings.Contains(got, "default: ubuntu-latest") || strings.Contains(got, "default: x") {
		t.Fatalf("inputBlock returned %q for the canary — it must hold the runner input's own lines and stop at the next input", got)
	}
	if m := runsOnLine.FindAllStringSubmatch(canary, -1); len(m) != 1 || m[0][1] != "macos-26" {
		t.Fatalf("runsOnLine found %v in the canary, want exactly one `macos-26`", m)
	}

	for _, name := range runnerInputReusables {
		t.Run(name, func(t *testing.T) {
			body := code(repoFile(t, filepath.Join(".github", "workflows", name)))

			block := inputBlock(body, "runner")
			if block == "" {
				t.Fatalf("%s declares no `runner` input — a caller cannot move this job off the "+
					"GitHub-hosted image", name)
			}
			if !strings.Contains(block, "\n        default: ubuntu-latest\n") && !strings.HasPrefix(block, "        default: ubuntu-latest\n") {
				t.Errorf("%s: the `runner` input does not default to ubuntu-latest:\n%s\nevery fleet "+
					"caller passes no `with:`, so the default is where all of them run", name, block)
			}

			runs := runsOnLine.FindAllStringSubmatch(body, -1)
			if len(runs) != 1 || strings.TrimSpace(runs[0][1]) != runsOnRunnerInput {
				t.Errorf("%s: runs-on is %v, want exactly one `%s` — a literal ignores the caller's "+
					"runner, and dropping the fallback leaves a forwarded empty variable with no runner at all",
					name, runs, runsOnRunnerInput)
			}
		})
	}
}
