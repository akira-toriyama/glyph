package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The three reusable workflows that install glyph to run it against a caller's
// repo. Each MUST reach the binary through the shared composite action, never
// through its own inline copy of the download.
var reusables = []string{"lint.yml", "pr-verdict.yml", "release.yml"}

// Tokens that belong ONLY in the composite action .github/actions/install now.
// If any reappears in a reusable's executable body, the security-critical
// install logic has been re-inlined — the exact duplication (and the
// linux/darwin drift) this extraction removed. Checked against comment-stripped
// text so the header prose ("provenance-verified with `gh attestation verify`")
// does not trip it.
var installOnlyTokens = []string{
	"gh attestation verify",
	"releases/download",
	"sha256sum",
	"shasum",
	"curl",
}

// A hand-bumped version default — `default: v0.4.0` — is the lockstep-drift
// source this PR removed: lint.yml sat at v0.4.0 through the v0.5.0 tag while
// callers pinned @v0.5.0. The reusables now derive the version from
// job.workflow_ref, so no reusable may carry a concrete version default.
var hardcodedVersionDefault = regexp.MustCompile(`(?m)default:\s*["']?v[0-9]`)

// fullLineComment matches a YAML line that is only a comment (optional indent,
// then '#'). Trailing inline comments are left in place; none of the sentinels
// below appear in one, and stripping them would risk cutting `#` inside a
// quoted string.
var fullLineComment = regexp.MustCompile(`(?m)^[ \t]*#.*$`)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	// The test runs in internal/workflows; the repo root is two levels up
	// (mirrors internal/gitmoji's golden-file test).
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// code returns the workflow text with whole-line comments removed, so an
// assertion about the executable body is not fooled by documentation prose.
func code(raw string) string {
	return fullLineComment.ReplaceAllString(raw, "")
}

func TestReusablesInstallThroughComposite(t *testing.T) {
	for _, name := range reusables {
		t.Run(name, func(t *testing.T) {
			raw := repoFile(t, filepath.Join(".github", "workflows", name))
			body := code(raw)

			// The install logic must be gone from the executable body...
			for _, tok := range installOnlyTokens {
				if strings.Contains(body, tok) {
					t.Errorf("%s still contains inline install token %q; it belongs only in "+
						".github/actions/install/action.yml (re-inlining reintroduces the "+
						"duplicated, drift-prone download)", name, tok)
				}
			}

			// ...and replaced by a reference to the shared composite action.
			if !strings.Contains(raw, "./.glyph-action/.github/actions/install") {
				t.Errorf("%s does not use the shared install action "+
					"(expected `uses: ./.glyph-action/.github/actions/install`)", name)
			}

			// The self-checkout mechanism must be present: a relative `uses:` in
			// a reusable workflow resolves against the CALLER's workspace, so the
			// action is reachable only by checking out glyph's own source at the
			// pinned commit.
			for _, want := range []string{
				"job.workflow_repository",
				"job.workflow_sha",
				"job.workflow_ref",
			} {
				if !strings.Contains(raw, want) {
					t.Errorf("%s is missing %s; without the self-checkout the install "+
						"action cannot be reached and the version cannot be derived", name, want)
				}
			}

			// No hand-bumped version default may return.
			if loc := hardcodedVersionDefault.FindString(raw); loc != "" {
				t.Errorf("%s carries a hardcoded version default (%q); the version must be "+
					"derived from job.workflow_ref so it cannot drift out of lockstep", name, strings.TrimSpace(loc))
			}
		})
	}
}

// The verdict fields pr-verdict.yml exposes to a caller. Each has to survive
// THREE hops — step -> job -> workflow_call — and a break in any one of them is
// silent: GitHub resolves a dangling ${{ }} to the empty string, so the caller
// sees "" and a gate reading empty-as-none would pass every PR. Nothing in the
// YAML expresses that chain, which is why it is asserted here.
var verdictOutputs = []string{"level", "next", "current", "breaking"}

// TestPRVerdictExposesTheVerdictToCallers guards the wiring sill's api-guard
// gate depends on (t-eb6h): without these outputs the level is computed, posted
// as prose, and discarded, leaving a caller to install glyph itself, re-grep the
// convention, or parse the comment — each of which forks the rules.
func TestPRVerdictExposesTheVerdictToCallers(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "pr-verdict.yml"))
	body := code(raw)

	for _, name := range verdictOutputs {
		// hop 3: workflow_call.outputs.<name>.value <- the job
		wantCall := "value: ${{ jobs.verdict.outputs." + name + " }}"
		if !strings.Contains(body, wantCall) {
			t.Errorf("pr-verdict.yml does not expose %q to callers (expected %q); "+
				"a caller cannot gate on a verdict it cannot read", name, wantCall)
		}
		// hop 2: jobs.verdict.outputs.<name> <- the step
		wantJob := name + ": ${{ steps.verdict.outputs." + name + " }}"
		if !strings.Contains(body, wantJob) {
			t.Errorf("pr-verdict.yml job does not publish %q (expected %q); the "+
				"workflow_call output would silently resolve to the empty string", name, wantJob)
		}
		// hop 1: the step actually writes it
		if !strings.Contains(body, "echo \""+name+"=$") {
			t.Errorf("the compose step never writes %q to $GITHUB_OUTPUT", name)
		}
	}

	// The step the job reads its outputs from must exist under that id.
	if !strings.Contains(body, "id: verdict") {
		t.Error("the compose step lost `id: verdict`; every job output would resolve to the empty string")
	}

	// The verdict and the comment must come from ONE computation. Dropping
	// --json would mean either losing the outputs or adding a second API walk
	// that could disagree with the comment.
	if !strings.Contains(body, "--json") {
		t.Error("pr-verdict.yml no longer calls glyph preview with --json; the body and the " +
			"machine verdict must come from one payload, not two walks that can disagree")
	}

	// `[ x = y ] && flag=true` under `set -e` exits the step whenever the test
	// is false — it would fail the job on every non-major PR. This shipped as a
	// near-miss while writing t-eb6h; pin the shape that replaced it.
	if regexp.MustCompile(`\[\s*"\$level"\s*=\s*"major"\s*\]\s*&&`).MatchString(body) {
		t.Error("breaking is derived with `[ ... ] && flag=true`, which exits under `set -e` " +
			"on every non-major PR; use an if/else")
	}

	// The fork-skip contract has to stay documented: it is the one way a caller
	// gets "" and the one way a naive gate silently passes an uninspected PR.
	if !strings.Contains(raw, "NOT COMPUTED") {
		t.Error("pr-verdict.yml no longer documents that a skipped (fork) job yields empty " +
			"outputs meaning NOT COMPUTED; a caller reading empty as `none` fails open")
	}
}

// changelogDisable matches a `disable:` set truthy anywhere in .goreleaser.yaml.
// The only block that carries one today is `changelog:`; a future pipe that
// legitimately needs `disable: true` should narrow this, not delete it.
var changelogDisable = regexp.MustCompile(`(?m)^\s*disable:\s*["']?true`)

// TestReleaseNotesReachGoReleaser guards the pairing that shipped v0.8.2 with an
// empty release body (t-bb72).
//
// GoReleaser loads --release-notes INSIDE the changelog pipe: changelog.Run
// reads ctx.ReleaseNotesFile and returns early before generating anything, while
// changelog.Skip short-circuits the whole pipe on `changelog.disable`. So
// `disable: true` does not merely turn off generation — it discards the notes
// file too, and the release ships empty. The two halves (workflow passes the
// flag, config keeps the pipe alive) are only correct together, and nothing in
// either file can express that on its own.
func TestReleaseNotesReachGoReleaser(t *testing.T) {
	workflow := repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml"))
	config := repoFile(t, ".goreleaser.yaml")

	// Half one: the workflow generates the notes with glyph and hands them over.
	body := code(workflow)
	for _, want := range []string{"glyph notes", "--release-notes"} {
		if !strings.Contains(body, want) {
			t.Errorf("goreleaser.yml no longer contains %q; glyph's own releases must be "+
				"described by `glyph notes`, not GoReleaser's raw commit subjects (t-8hqn)", want)
		}
	}

	// Half two: the changelog pipe stays enabled so that flag is actually read.
	if loc := changelogDisable.FindString(code(config)); loc != "" {
		t.Errorf(".goreleaser.yaml sets %q; that skips the changelog pipe, which is the "+
			"pipe that loads --release-notes — the release body ships EMPTY (v0.8.2 did). "+
			"Neuter the built-in generator with a filter instead of disabling the pipe.",
			strings.TrimSpace(loc))
	}

	// ...and the built-in generator stays neutered, so a run WITHOUT the flag
	// yields an empty body rather than falling back to raw subjects.
	if !strings.Contains(code(config), `- ".*"`) {
		t.Error(".goreleaser.yaml no longer excludes every commit from the built-in changelog; " +
			"without that filter a goreleaser run that misses --release-notes silently falls back " +
			"to raw commit subjects (the unescaped-@mention surface t-8hqn removed)")
	}
}

func TestCompositeActionIsSingleSource(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "actions", "install", "action.yml"))

	if !strings.Contains(raw, "using: composite") {
		t.Error("install action is not `using: composite`")
	}

	// The install internals live here and only here.
	for _, want := range []string{
		"gh attestation verify",
		"--signer-workflow akira-toriyama/glyph/.github/workflows/goreleaser.yml",
		"releases/download",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("install action is missing %q — it is meant to be the single source of the install logic", want)
		}
	}

	// The github context is NOT available when a composite action's manifest is
	// loaded — a `${{ github.* }}` expression in a live scalar (even an input
	// description string, which GitHub still template-evaluates) fails the load
	// with "Unrecognized named-value: 'github'" the first time the action is used.
	// That shipped once in v0.6.0 and only a canary caught it, because nothing
	// loads the manifest until a workflow actually calls the action. Checked
	// against comment-stripped text: GitHub's YAML parser drops comments before
	// the template evaluator runs, so a github expression in the header's usage
	// example (a comment) is fine — only a live one breaks the load.
	body := code(raw)
	if strings.Contains(body, "${{ github.") || strings.Contains(body, "${{github.") {
		t.Error("install action.yml references the github context in a `${{ github.* }}` " +
			"expression outside a comment; that context is unavailable at manifest-load time " +
			"and fails the action load. Pass what you need as an input from the calling workflow step instead.")
	}

	// It must serve BOTH runner families: the whole point of extracting it was
	// that the inline copies had diverged (linux_amd64 + sha256sum vs
	// darwin_arm64 + shasum). Collapsing it back to one platform would re-break
	// whichever job runs on the other.
	for _, want := range []string{"linux", "darwin", "sha256sum", "shasum", "RUNNER_OS", "RUNNER_ARCH"} {
		if !strings.Contains(raw, want) {
			t.Errorf("install action is missing %q — it must auto-detect OS/arch to serve the Linux and macOS jobs alike", want)
		}
	}
}
