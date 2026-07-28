package workflows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// prTrigger matches the `pull_request` key of a workflow's own `on:` block — two
// spaces of indent, the key on its own line. Matched as a key rather than as a
// substring so a `pull_request` inside a step's expression cannot pass for a
// trigger.
var prTrigger = regexp.MustCompile(`(?m)^  pull_request:?\s*$`)

// TestCheckScriptAccountsForEveryPRGate is the mechanical half of scripts/check.sh's
// header claim, which is that a green run there means a green CI.
//
// The claim was false in both directions, and the reason it stayed false is that
// nothing could tell: check.sh never mirrored build.yml's `bite` job, and it ran
// govulncheck, which build.yml does not contain (t-7tj3). A prose header cannot
// notice a workflow appearing next to it, so this test asks the one question that
// keeps the header honest without freezing the script's shape — is every gate that
// runs on a pull request at least ACCOUNTED FOR, either mirrored or listed as not
// mirrored with a reason?
//
// It deliberately does not check HOW a gate is handled. Deciding that is a
// judgement (reproducing zizmor's policy in shell would be a second source of
// truth for a fleet rule, and glyph has no .toml for taplo to read), and a test
// that encoded the judgement would have to be edited every time the judgement was
// revisited. What it refuses is the SILENT case: a new PR gate that check.sh has
// never heard of, which is how the header went stale in the first place.
func TestCheckScriptAccountsForEveryPRGate(t *testing.T) {
	// Positive control: a trigger matcher that matched nothing would let every
	// workflow skip the loop below and pass this test vacuously for ever.
	const canary = "on:\n  push:\n    branches: [main]\n  pull_request:\n"
	if !prTrigger.MatchString(canary) {
		t.Fatalf("prTrigger no longer matches a real `on:` block (%q); every workflow would be "+
			"skipped and this test would pass vacuously — re-derive it from a workflow's on: block", canary)
	}

	script := repoFile(t, filepath.Join("scripts", "check.sh"))
	checked := 0
	for _, name := range workflowFiles(t) {
		raw := repoFile(t, filepath.Join(".github", "workflows", name))
		if !prTrigger.MatchString(raw) {
			continue // a reusable (workflow_call) or a tag-triggered build gates no pull request
		}
		checked++
		if !strings.Contains(script, name) {
			t.Errorf("%s runs on every pull request, and scripts/check.sh never mentions it. "+
				"Its header claims a green run there means a green CI, so every PR gate has to be "+
				"either mirrored (add it, and add its name to MIRRORS so the run reconciles) or "+
				"listed in the NOT MIRRORED block WITH THE REASON. A gate the script has not heard "+
				"of is exactly how that claim went stale.", name)
		}
	}
	if checked == 0 {
		t.Fatal("no workflow under .github/workflows matched the pull_request trigger — either the " +
			"trigger shape changed or the directory moved, and this test is now checking nothing")
	}
}

// TestCheckScriptDeclaresNoSilentSkip pins the shape of the defect rather than one
// instance of it. The old script printed "(skipped — not installed; CI runs it)"
// for each optional linter and then finished with its success line, so a machine
// with neither installed reported a green verdict over two gates it never ran —
// which is what govulncheck did here, every run, unremarked.
//
// A gate is now either run or fatal. This test does not ask which; it refuses the
// third option of announcing an omission and carrying on, in any wording that
// still contains the word.
func TestCheckScriptDeclaresNoSilentSkip(t *testing.T) {
	script := repoFile(t, filepath.Join("scripts", "check.sh"))
	for i, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the header explains the retired behaviour on purpose
		}
		if strings.Contains(trimmed, "skipped") {
			t.Errorf("scripts/check.sh:%d announces a skip: %q. A CI gate that cannot run here must "+
				"be a hard error, not a line in the log followed by a success message — that is the "+
				"exact shape that let govulncheck never run and still report green (t-7tj3). If the "+
				"gate genuinely cannot be mirrored, move it to the NOT MIRRORED block with its reason.",
				i+1, trimmed)
		}
	}
}

// TestCheckScriptRunsBiteUnderGoModsToolchain guards the one gate here that runs
// under a Go the script chooses rather than the one every other gate resolves.
//
// go-bite exports GOTOOLCHAIN=local, correctly: in CI, setup-go has already
// installed the toolchain go.mod names. Locally nothing has. `nix develop` supplies
// nixpkgs' current go (measured: 1.26.4 against go.mod's `toolchain go1.26.5`) and a
// mise install leaks a GOROOT for a different version into the shell, so the driver
// and its GOROOT disagree and every package fails to compile — `cannot build
// bitescan`, no verdict, and the run never reaches its ✓ line (t-wmwb).
//
// The fix that suggests itself is `unset GOROOT`, and it is the one this test
// refuses. It compiles, so it looks right, but it leaves bite judging the diff with
// nixpkgs' go while CI judges it with go.mod's — a loud failure swapped for a silent
// divergence, in the script whose entire purpose is the claim that they agree. So
// the requirement is positive: hand go-bite the toolchain root the other gates
// resolved, which is what setup-go hands it in CI.
func TestCheckScriptRunsBiteUnderGoModsToolchain(t *testing.T) {
	script := repoFile(t, filepath.Join("scripts", "check.sh"))

	var invocation string
	for _, stmt := range executableStatements(script) {
		if strings.Contains(stmt, `sh "$BITE"`) {
			invocation = stmt
			break
		}
	}
	if invocation == "" {
		t.Fatal(`no executable statement in scripts/check.sh runs sh "$BITE" — either the bite gate ` +
			"was removed (it is a mirrored gate, so MIRRORS must lose it too) or it is invoked some " +
			"other way and this test is now checking nothing")
	}

	if !strings.Contains(script, "go env GOROOT") {
		t.Error("scripts/check.sh no longer derives a GOROOT from `go env GOROOT`. That call is how the " +
			"script learns which toolchain go.mod's `toolchain` line resolved to under GOTOOLCHAIN=auto — " +
			"the same one setup-go installs in CI. Without it the bite gate has no way to run under CI's Go.")
	}
	for _, key := range []string{"GOROOT=", "PATH="} {
		if !strings.Contains(invocation, key) {
			t.Errorf("the bite invocation in scripts/check.sh does not set %s: %q\n\n"+
				"go-bite exports GOTOOLCHAIN=local, so it runs whatever `go` it finds against whatever "+
				"GOROOT it inherits. Locally those are two different Go versions and it cannot build at "+
				"all (t-wmwb). Clearing GOROOT instead would let it build under nixpkgs' go while CI uses "+
				"go.mod's — this script's header claims a green run here means a green CI, and that trade "+
				"makes the claim quietly false. Pass GOROOT=\"$(go env GOROOT)\" and prepend its bin to PATH.",
				strings.TrimSuffix(key, "="), invocation)
		}
	}
}

// executableStatements drops comment lines and joins backslash continuations, so a
// guard reads one shell command as one string. Without the join, an invocation
// spread over two lines looks like two statements and a guard on it matches neither
// half.
func executableStatements(script string) []string {
	var out []string
	pending := ""
	for line := range strings.SplitSeq(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if head, continued := strings.CutSuffix(line, `\`); continued {
			pending += head
			continue
		}
		out = append(out, pending+line)
		pending = ""
	}
	if pending != "" {
		out = append(out, pending)
	}
	return out
}
