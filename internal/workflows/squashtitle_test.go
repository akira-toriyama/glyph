package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const lintTitleStep = "Lint the squash title"

// TestLintGateAlsoJudgesTheSquashTitle pins the second half of the lint gate:
// the squash title. A squash merge records the PR title as the subject of the
// commit that lands on main — CONTRIBUTING ratifies that title as a commit
// subject — and the BASE..HEAD range can never see it, because the range holds
// the pre-squash commits while the title exists as no commit at all until the
// squash mints it. Measured across the fleet before the step existed, 158
// landed subjects had failed the grammar this way, each one read by the
// release walk's fallback as a silent none. Losing this step does not red
// anything: the range job stays green and the hole simply reopens, which is
// why the mutation ledger re-breaks it by name.
//
// The step's condition is what TestLintTitleStepExitsGlyphsVerdict cannot see,
// running the script alone. `!cancelled()` runs it behind a red range step, so
// a pull failing both carries both findings in one red run (DESIGN §3); a step
// with a bare event condition is skipped there, the way glyph-test-2 run
// 41809643056 hid the title's finding. No `continue-on-error:` keeps either
// step's exit the job's conclusion, measured on glyph-test-2 #8 through #11
// (2026-11-04): both red, range red beside a green title, and range green beside
// a red title all conclude failure; both green concludes success.
func TestLintGateAlsoJudgesTheSquashTitle(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "lint.yml"))
	body := code(raw)

	if !strings.Contains(body, `glyph lint --pr "$PR_NUMBER"`) {
		t.Errorf("lint.yml no longer runs `glyph lint --pr` — the squash title is back to being " +
			"the one merge-candidate subject no gate checks, and its violations resurface only " +
			"as silently dropped bumps in the next release walk")
	}
	shape := readStepShape(t, raw, lintTitleStep)
	if shape.keys["run"] != "|" {
		t.Fatalf("canary: the reader found keys %v on the title step, which has a `run: |` — the checks below "+
			"would hold over a reader that sees nothing", shape.keys)
	}
	if got := shape.keys["if"]; got != "${{ !cancelled() && github.event_name == 'pull_request' }}" {
		t.Errorf("the title step's condition is %q, want ${{ !cancelled() && github.event_name == 'pull_request' }} — "+
			"without `!cancelled()` a red range step skips it and the title's finding waits for a second red run; "+
			"without the event a push event, which has no pull_request.number, makes it refuse every direct push "+
			"the push arm exists to annotate", got)
	}
	if v, ok := shape.keys["continue-on-error"]; ok {
		t.Errorf("the title step carries `continue-on-error: %s` — the job passes whatever the step exits, so a "+
			"title the grammar refuses merges green", v)
	}
	if v, ok := shape.keys["shell"]; ok {
		t.Errorf("the title step names `shell: %s` — TestLintTitleStepExitsGlyphsVerdict reproduces the unspecified "+
			"shell's `bash -e {0}`; teach it the new invocation", v)
	}
	if !strings.Contains(body, "PR_NUMBER: ${{ github.event.pull_request.number }}") {
		t.Errorf("the squash-title step no longer reads the PR number from the event payload — " +
			"whatever it lints now, it is not this pull request's title")
	}

	// The read needs a token permission the range lint never did, in BOTH
	// halves of this file — and the two halves fail differently (measured in
	// .github#186): drop it from the executable grant and the squash-title
	// read 403s at runtime, because a reusable can only downgrade the
	// caller's token; drop it from the commented caller stub and every caller
	// written from it dies as startup_failure before any job starts, because
	// the reusable now declares more than the caller grants.
	if !strings.Contains(body, "pull-requests: read") {
		t.Errorf("lint.yml's permissions no longer include pull-requests: read — a reusable can " +
			"only downgrade the caller's token, so the squash-title read 403s at runtime " +
			"(exit 4, a loud infra failure on every pull request) even from a caller that " +
			"still grants the permission")
	}
	if !strings.Contains(raw, "#     pull-requests: read") {
		t.Errorf("the commented caller stub no longer grants pull-requests: read — the stub is " +
			"what the fleet's canonical caller is written from, and a caller granting less " +
			"than this reusable declares never reaches the title step: the run dies as " +
			"startup_failure before any job starts (measured in .github#186)")
	}
}

// TestLintTitleStepExitsGlyphsVerdict runs the title step's script under the
// unspecified-shell invocation with a glyph stub. Its exit is the half of the
// job's conclusion this step owns, and the finding glyph wrote reaches the log.
func TestLintTitleStepExitsGlyphsVerdict(t *testing.T) {
	requireTool(t, "jq")
	script := extractRun(t, repoFile(t, filepath.FromSlash(lintWorkflow)), lintTitleStep)

	for _, c := range []struct {
		name, stderr, says string
		exit               int
	}{
		{"a title the grammar claims", "", "title OK", 0},
		{
			"a title the grammar refuses",
			"::error::stub finding\n" + `{"error":{"code":3,"message":"stub verdict"}}` + "\n",
			"retitle the pull request", 3,
		},
		{"a pull the API will not answer", `{"error":{"code":4,"message":"stub verdict"}}` + "\n", "::error::glyph lint: stub verdict", 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			stubs := filepath.Join(dir, "stubs")
			if err := os.Mkdir(stubs, 0o755); err != nil {
				t.Fatal(err)
			}
			stderrFile := filepath.Join(dir, "glyph-stderr")
			if err := os.WriteFile(stderrFile, []byte(c.stderr), 0o600); err != nil {
				t.Fatal(err)
			}
			glyphLog := filepath.Join(dir, "glyph-calls")
			writeStub(t, stubs, "glyph", recordArgv(glyphLog)+"cat "+shq(stderrFile)+" >&2\nexit "+strconv.Itoa(c.exit)+"\n")

			run := runStep(t, script, unspecifiedShell, dir, []string{
				"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
				"HOME=" + dir,
				"RUNNER_TEMP=" + dir,
				"PR_NUMBER=7",
			})
			if run.exit != c.exit {
				t.Errorf("glyph's exit %d left the title step at %d — the job's conclusion is that exit, or a "+
					"refused title merges green\n%s", c.exit, run.exit, run)
			}
			want := [][]string{{"lint", "--pr", "7"}}
			if calls := recordedCalls(t, glyphLog); !slices.EqualFunc(calls, want, slices.Equal) {
				t.Errorf("the title step called glyph %v, want exactly %v\n%s", calls, want, run)
			}
			if !strings.Contains(run.stdout, c.says) {
				t.Errorf("the title step's log lacks %q\n%s", c.says, run)
			}
			if c.stderr != "" && !strings.Contains(run.stderr, c.stderr) {
				t.Errorf("the title step did not replay glyph's stream, so its annotations never reach the run\n%s", run)
			}
		})
	}
}

// pullRequestTypes reads the `types:` list under a caller's `pull_request:`,
// comment lines between the two included.
var pullRequestTypes = regexp.MustCompile(`(?m)^\s*pull_request:\s*(?:#.*\n\s*)*types:\s*\[([^\]]*)\]`)

// TestLintCallersRefireOnARetitle holds every copy of the lint caller to one
// trigger list. GitHub runs a workflow on opened, synchronize and reopened unless
// `types:` says otherwise, so a bare `pull_request:` never re-fires on a retitle:
// on glyph-test-2 #4 a red title edited to a clean one stayed red (run
// 41810130438), and a green pull edited to a sigil-less title stayed green and
// mergeable with that subject (run 41810951764), while a control workflow on
// `edited` fired on all three retitles. Declared, the check follows the title on
// one head: #10 went red, green, red across two retitles (runs 41816405170,
// 41816778876, 41816884433). DESIGN §4's API-lag fallback reads the title as a
// linted subject only as far as this holds. README and the stub are what a caller
// is written from; commit-lint.yml is the fleet's, kept by fleet-sync.
//
// version-preview.yml keeps its shorter list on purpose: the preview never reads
// the title. Only `lint --pr` does (internal/cli/cmd_lint.go), and the sticky
// comments on those pulls hold the commits' subjects and none of the titles.
func TestLintCallersRefireOnARetitle(t *testing.T) {
	var readme string
	for _, block := range fencedYAML(repoFile(t, "README.md")) {
		if strings.HasPrefix(block.text, "# .github/workflows/commit-lint.yml\n") {
			readme = block.text
		}
	}
	if readme == "" {
		t.Fatal("no yaml fence in README.md opens with `# .github/workflows/commit-lint.yml` — the lint caller " +
			"moved, and the comparison below has lost a copy")
	}

	want := []string{"opened", "synchronize", "reopened", "edited"}
	for _, caller := range []struct{ name, text string }{
		{"README's lint caller", readme},
		{"lint.yml's commented stub", commentYAML(repoFile(t, filepath.FromSlash(lintWorkflow))).text},
		{"commit-lint.yml", code(repoFile(t, filepath.Join(".github", "workflows", "commit-lint.yml")))},
	} {
		m := pullRequestTypes.FindStringSubmatch(caller.text)
		if m == nil {
			t.Errorf("%s declares no types under pull_request: — GitHub's default list leaves out `edited`, so a "+
				"retitle never re-fires the squash-title lint", caller.name)
			continue
		}
		var got []string
		for item := range strings.SplitSeq(m[1], ",") {
			got = append(got, strings.TrimSpace(item))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s triggers on %v, want %v — the title is the subject a squash lands, and its lint must see "+
				"every event that changes it", caller.name, got, want)
		}
	}
}
