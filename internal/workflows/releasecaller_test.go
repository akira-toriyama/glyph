package workflows

// The caller side of the reusables: README's "Getting started" step 3 is the
// one home of each caller's shape (t-prxj), and a caller written from it must
// start — grant what the reusable declares and pass what it requires — and
// a release caller passing nothing must get a draft that is the notes alone
// (t-zgrp). Both failed on a README-only adopter (glyph-test-2, 2026-11-04):
// the callers had no README copy, and `install-notes` was required even for
// a repository with nothing to install.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// releaseVerdictEnvelope is the single line's release envelope as the glyph
// stub answers it: one tag, one draft action, the url the notice step reads.
const releaseVerdictEnvelope = `{"tag":"v0.1.0","action":"create","level":"minor","current":"v0.0.0","url":"https://example.invalid/d"}`

// TestReleaseInstallNotesIsOptional: a repository whose release is the notes
// has nothing to install, and a required input it had to fill with a
// placeholder was the first thing a README-only adopter tripped on
// (glyph-test-2, 2026-11-04, under the v6.1.0 reusable). The input is
// declared optional with an empty default, and the verdict step passes
// `--footer-file` only for a non-empty value — never an empty file, which
// would put a `---` line over nothing — and byte for byte when it does.
func TestReleaseInstallNotesIsOptional(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	decl := inputDeclaration(t, code(raw), "install-notes")
	if strings.Contains(decl, "required: true") || !strings.Contains(decl, "required: false") {
		t.Errorf("install-notes is not declared optional — a caller omitting it dies as startup_failure before any job:\n%s", decl)
	}
	if !strings.Contains(decl, `default: ""`) {
		t.Errorf("install-notes has no empty default — the verdict step reads the input as a string and an unset "+
			"optional input is \"\" only by GitHub's default, which the declaration must state:\n%s", decl)
	}

	script := extractRun(t, raw, "Compose the verdict and upsert the rolling DRAFT")
	requireTool(t, "jq")
	const notes = "## Install\n\n`brew install example/tap/thing`\n"
	cases := []struct {
		name   string
		notes  string
		dryRun string
		want   []string
	}{
		{"nothing to install", "", "false", []string{"release", "--json"}},
		{"an install block", notes, "false", []string{"release", "--json", "--footer-file", "<file>"}},
		{"an install block on a dry run", notes, "true", []string{"release", "--dry-run", "--json", "--footer-file", "<file>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin, temp := t.TempDir(), t.TempDir()
			log := filepath.Join(temp, "argv.log")
			writeStub(t, bin, "glyph", recordArgv(log)+"printf '%s' "+shq(releaseVerdictEnvelope)+"\nexit 0\n")
			output := filepath.Join(temp, "output")
			run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RUNNER_TEMP=" + temp,
				"GITHUB_OUTPUT=" + output,
				"GH_TOKEN=t",
				"INSTALL_NOTES=" + c.notes,
				"DRY_RUN=" + c.dryRun,
			})
			if run.exit != 0 {
				t.Fatalf("the verdict step exited %d on a release verdict\n%s", run.exit, run)
			}
			calls := recordedCalls(t, log)
			if len(calls) != 1 {
				t.Fatalf("glyph was called %d time(s), want once: %v", len(calls), calls)
			}
			got := calls[0]
			footer := filepath.Join(temp, "install-notes.md")
			want := slices.Clone(c.want)
			for i, w := range want {
				if w == "<file>" {
					want[i] = footer
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("glyph release was called as %q, want %q", got, want)
			}
			if c.notes == "" {
				if _, err := os.Stat(footer); err == nil {
					t.Errorf("an empty install-notes still wrote %s — the step must pass no footer, not an empty one", footer)
				}
				return
			}
			if b, err := os.ReadFile(footer); err != nil || string(b) != c.notes { //nolint:gosec // the step's own temp file
				t.Errorf("the footer file holds %q (%v), want the input byte for byte %q", b, err, c.notes)
			}
			if !strings.Contains(readIfAny(t, output), "next=v0.1.0\n") {
				t.Errorf("the step did not publish next=v0.1.0 — the stub's envelope was not read:\n%s", readIfAny(t, output))
			}
		})
	}
}

// inputDeclaration returns the block of one workflow_call input — its key
// line and every deeper line — out of a reusable's comment-stripped body. An
// absent input is a hard failure: the assertions over it would hold over
// nothing.
func inputDeclaration(t *testing.T, body, name string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != name+":" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		block := []string{line}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n")
	}
	t.Fatalf("no input %q is declared — this assertion holds over nothing", name)
	return ""
}

// readmeCaller is one fenced caller in README.md: the reusable it calls and
// the YAML a reader copies.
type readmeCaller struct {
	reusable string
	text     string
}

// readmeCallers returns README's fenced yaml blocks that call a glyph
// reusable, keyed by the reusable's file name. Two blocks calling one
// reusable would be two homes, and fail.
func readmeCallers(t *testing.T) map[string]readmeCaller {
	t.Helper()
	callerUses := regexp.MustCompile(`(?m)^\s*uses:\s*akira-toriyama/glyph/\.github/workflows/([a-z-]+\.yml)@vX\.Y\.Z`)
	callers := map[string]readmeCaller{}
	for _, block := range fencedYAML(repoFile(t, "README.md")) {
		m := callerUses.FindStringSubmatch(block.text)
		if m == nil {
			continue
		}
		if prior, dup := callers[m[1]]; dup {
			t.Fatalf("README.md holds two yaml fences calling %s — one home per caller:\n%s\n---\n%s", m[1], prior.text, block.text)
		}
		callers[m[1]] = readmeCaller{m[1], block.text}
	}
	return callers
}

// yamlGrants reads every `permissions:` block of a YAML text — workflow level
// and job level alike, block form only, the shape the reusables and README
// write — into the strongest level seen per scope.
func yamlGrants(text string) map[string]string {
	grants := map[string]string{}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "permissions:" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" || strings.HasPrefix(strings.TrimSpace(next), "#") {
				continue
			}
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			scope, level, ok := strings.Cut(strings.TrimSpace(next), ":")
			if !ok {
				continue
			}
			level = strings.Fields(level + " x")[0] // the level alone, a trailing comment dropped
			if grants[scope] != "write" {
				grants[scope] = level
			}
		}
	}
	return grants
}

// TestReadmeCallersGrantWhatTheReusablesDeclare holds README's three callers
// to the reusables they call: every scope a reusable declares — at the
// workflow level or on its job — is granted at least as high, and every
// input it marks required is named under `with:`. A caller granting less
// dies as startup_failure before any job (akira-toriyama/.github#186), one
// omitting a required input the same and with no error surfaced anywhere
// (glyph-test3, 2026-08-26) — the two failures `glyph doctor` reports and a
// README-only adopter met on glyph-test-2 (2026-11-04) because the two
// callers had no README copy to be written from. The pr-verdict caller is
// also held to its name: the definition occupies pr-verdict.yml in this
// repository, and a caller synced to that path overwrote it once
// (2026-07-17).
func TestReadmeCallersGrantWhatTheReusablesDeclare(t *testing.T) {
	// Positive control for the grant reader: a job-level elevation is seen,
	// and a trailing comment does not become part of the level.
	control := yamlGrants("permissions:\n  contents: read\njobs:\n  j:\n    permissions:\n      contents: read\n      pull-requests: write  # the comment\n")
	if control["contents"] != "read" || control["pull-requests"] != "write" {
		t.Fatalf("yamlGrants reads %v from the control text — every verdict below would be this reader's", control)
	}

	callers := readmeCallers(t)
	for _, name := range reusables {
		caller, ok := callers[name]
		if !ok {
			t.Errorf("README.md has no yaml fence calling %s@vX.Y.Z — a README-only adopter cannot wire it", name)
			continue
		}
		reusable := code(repoFile(t, filepath.Join(".github", "workflows", name)))
		declared, granted := yamlGrants(reusable), yamlGrants(caller.text)
		if len(declared) == 0 {
			t.Fatalf("%s declares no permissions block the reader can see — the comparison below would be vacuous", name)
		}
		for scope, level := range declared {
			got := granted[scope]
			if got == "write" || got == level {
				continue
			}
			t.Errorf("README's %s caller grants %s: %q, but the reusable declares %s: %s — a caller granting less "+
				"than its reusable dies as startup_failure before any job", name, scope, got, scope, level)
		}
		for input := range requiredInputs(reusable) {
			if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(input) + `:`).MatchString(caller.text) {
				t.Errorf("README's %s caller never names the required input %s under with: — GitHub kills the run "+
					"as startup_failure and surfaces no error anywhere", name, input)
			}
		}
	}

	if pv, ok := callers["pr-verdict.yml"]; ok {
		first, _, _ := strings.Cut(pv.text, "\n")
		if !strings.Contains(first, ".github/workflows/version-preview.yml") || strings.Contains(pv.text, "workflows/pr-verdict.yml\n") {
			t.Errorf("README's pr-verdict caller must be named version-preview.yml on its first line, never "+
				"pr-verdict.yml — the definition occupies that name here, and a caller synced to it overwrote "+
				"the reusable on 2026-07-17:\n%s", pv.text)
		}
	}
}

// requiredInputs lists the inputs a reusable's workflow_call block marks
// `required: true`, by the nearest input key above the line.
func requiredInputs(body string) map[string]bool {
	required := map[string]bool{}
	current := ""
	inInputs := false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case trimmed == "inputs:" && indent == 4:
			inInputs = true
		case inInputs && indent <= 4 && trimmed != "":
			inInputs = false
		case inInputs && indent == 6 && strings.HasSuffix(trimmed, ":"):
			current = strings.TrimSuffix(trimmed, ":")
		case inInputs && trimmed == "required: true" && current != "":
			required[current] = true
		}
	}
	return required
}
