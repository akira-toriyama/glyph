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

// fetchDepthFull is actions/checkout's full-history setting as a workflow
// writes it. Matched as its own YAML key so a `fetch-depth: 0` inside a quoted
// string or a heredoc cannot satisfy it.
var fetchDepthFull = regexp.MustCompile(`(?m)^\s*fetch-depth:\s*0\s*$`)

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
			// Presence is demanded of the executable BODY, not the raw text: a
			// commented caller stub quoting the same string would otherwise
			// satisfy the requirement while the real step is gone (t-szx1).
			if !strings.Contains(body, "./.glyph-action/.github/actions/install") {
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
				if !strings.Contains(body, want) {
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
	// Deliberately RAW where every other presence check reads code(): this one
	// demands documentation, and documentation living in a comment is the
	// requirement being met, not the loophole (t-szx1).
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

// gitVersionSort matches git's version sort in either direction — the heart of
// any shell re-derivation of a version order (`git tag --sort=v:refname`).
var gitVersionSort = regexp.MustCompile(`--sort=-?v:refname`)

// TestReleaseNotesWalkBaseIsResolvedByGlyph guards where the notes step's walk
// base comes from: the binary, never shell (t-s5n4).
//
// At tag-push time the new tag is already the highest v*, so the base is the
// highest version tag STRICTLY below it — a version comparison. goreleaser.yml
// used to re-derive that answer with awk over `git tag --sort=v:refname`, which
// is a refname sort and not a version order: the exact defect 012bfba removed
// from the binary's own resolver, hand-copied back in shell, where the Go
// regression tests could not see it. Measured on tags {v1.0.0-rc1, v1.0.0,
// v1.0.1}: the shell answered v1.0.0-rc1 as v1.0.1's predecessor (released
// commits re-folded into the notes), and an EMPTY predecessor for the
// lowest-sorted tag (exit 1 — no binaries, no cask, no attestation, behind a
// tag that already existed). `--since-tag=below:` is the binary answering the
// same question with every tag parsed and compared as a version.
func TestReleaseNotesWalkBaseIsResolvedByGlyph(t *testing.T) {
	workflow := repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml"))
	body := code(workflow)

	// The affirmative half: the notes walk hands the predecessor question to
	// the binary, bound to the tag being cut.
	if !strings.Contains(body, `--since-tag="below:$GITHUB_REF_NAME"`) {
		t.Error(`goreleaser.yml no longer walks --since-tag="below:$GITHUB_REF_NAME"; the walk base ` +
			"must be resolved by glyph itself — a shell derivation re-inherits the tag-order defect " +
			"012bfba removed from the binary (t-s5n4)")
	}

	// Positive control for the absence below: the pattern must still recognise
	// the retired derivation, or the guard is asserting nothing.
	retired := `prev="$(git tag --list 'v*' --sort=v:refname | awk -v cur="$GITHUB_REF_NAME" '...')"`
	if !gitVersionSort.MatchString(retired) {
		t.Fatal("canary: gitVersionSort no longer matches the retired shell derivation it exists to keep out")
	}
	if loc := gitVersionSort.FindString(body); loc != "" {
		t.Errorf("goreleaser.yml's executable body reads git's tag sort (%q): that is a refname "+
			"sort, not a version order — with a prerelease tag present it hands back a wrong or "+
			"empty walk base; resolve the base inside glyph (--since-tag=below:) instead", loc)
	}
}

func TestCompositeActionIsSingleSource(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "actions", "install", "action.yml"))
	// Every presence check below reads the comment-stripped body: the header's
	// usage example is a comment quoting much of the same text, so a raw match
	// would keep passing after the executable half was deleted (t-szx1).
	body := code(raw)

	if !strings.Contains(body, "using: composite") {
		t.Error("install action is not `using: composite`")
	}

	// The install internals live here and only here.
	for _, want := range []string{
		"gh attestation verify",
		"--signer-workflow akira-toriyama/glyph/.github/workflows/goreleaser.yml",
		"releases/download",
	} {
		if !strings.Contains(body, want) {
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
		if !strings.Contains(body, want) {
			t.Errorf("install action is missing %q — it must auto-detect OS/arch to serve the Linux and macOS jobs alike", want)
		}
	}
}

// TestPresenceChecksCannotBeSatisfiedByAComment is the non-vacuity canary for
// every presence assertion above that reads code(...). Measured in the
// 2026-07-22 audit: five presence checks in this file read the RAW text, so a
// whole-line comment quoting the required string satisfied them — the guarded
// step could be deleted and the requirement stayed green off its own
// documentation. This pins the mechanism that closed that: code() drops a
// whole-line comment entirely, and keeps the same bytes when they are
// executable. Trailing INLINE comments survive code() by design (see
// fullLineComment) — that residual acceptance is documented there, not here.
func TestPresenceChecksCannotBeSatisfiedByAComment(t *testing.T) {
	const doc = "# using: composite\n  #   uses: ./.glyph-action/.github/actions/install\nname: x\n"
	for _, tok := range []string{"using: composite", "./.glyph-action/.github/actions/install"} {
		if strings.Contains(code(doc), tok) {
			t.Errorf("code() kept %q from a whole-line comment; every presence check reading "+
				"code() can be satisfied by documentation again", tok)
		}
	}
	// Positive control: the same token outside a comment must survive, or
	// code() has started eating the executable body and every presence check
	// above would fail closed instead.
	if !strings.Contains(code("runs:\n  using: composite\n"), "using: composite") {
		t.Error("code() dropped an executable line; the presence checks above are now asserting against mutilated text")
	}
}

// glyphRef matches ANY reference to one of glyph's own reusable workflows or to
// its install action, capturing the ref it is pinned to. Anchored on the "@",
// never on a trailing "# pin a release tag": of the four commented caller stubs
// in the reusables, one (pr-verdict.yml's gating example) carries no trailing
// comment at all, and a matcher that needed one would silently skip it.
var glyphRef = regexp.MustCompile(`akira-toriyama/glyph/\.github/[^@\s]+@(\S+)`)

// versionPlaceholder is the literal the caller stubs must use instead of a
// concrete tag. Precedent: docs/DESIGN.md has used it since v0.6.0 (14bf397)
// and has needed zero maintenance across the eleven releases since.
const versionPlaceholder = "vX.Y.Z"

// concreteTag is what an EXECUTABLE `uses:` must carry — a real release tag.
var concreteTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// workflowFiles lists every .yml under .github/workflows, so a new caller or a
// new reusable is covered without editing this test.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("no workflow files found under %s", dir)
	}
	return names
}

// TestCallerStubsCarryNoConcreteVersion pins the split that makes the stubs
// maintainable: a COMMENTED caller stub shows @vX.Y.Z, an EXECUTABLE `uses:`
// shows a real tag.
//
// A concrete version in a stub cannot be kept current by any schedule of edits.
// A release tag is cut on a tree that was frozen BEFORE it (35–62s earlier on
// v0.10.0/v0.10.1/v0.10.2), so a version written into a reusable names the
// PREVIOUS release the moment it ships — the stubs read @v0.10.0 while the tag
// was v0.10.2. The placeholder is not laziness; it is the only honest value.
func TestCallerStubsCarryNoConcreteVersion(t *testing.T) {
	stubs := map[string]int{}

	for _, name := range workflowFiles(t) {
		raw := repoFile(t, filepath.Join(".github", "workflows", name))
		for i, line := range strings.Split(raw, "\n") {
			m := glyphRef.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			ref, lineno := m[1], i+1
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				stubs[name]++
				if ref != versionPlaceholder {
					t.Errorf("%s:%d — commented caller stub pins the concrete ref %q; use @%s. "+
						"A tag is cut on a commit that already exists, so any version written in a "+
						"comment names the previous release the moment it ships (these read @v0.10.0 "+
						"under the v0.10.2 tag). Substituting the tag is the reader's job.",
						name, lineno, ref, versionPlaceholder)
				}
				continue
			}
			// Executable: this one really is resolved by Actions.
			if ref == versionPlaceholder {
				t.Errorf("%s:%d — executable `uses:` is pinned to the placeholder @%s, which is "+
					"not a ref and cannot resolve; pin a concrete release tag", name, lineno, versionPlaceholder)
			} else if !concreteTag.MatchString(ref) {
				t.Errorf("%s:%d — executable `uses:` is pinned to %q, which is not a vX.Y.Z release "+
					"tag; the binary version is derived from the tag, so a moving ref breaks lockstep",
					name, lineno, ref)
			}
		}
	}

	// Non-vacuity: every reusable documents its caller, so a stub that quietly
	// disappeared (taking the invariant with it) fails here rather than passing.
	for _, name := range reusables {
		if stubs[name] == 0 {
			t.Errorf("%s carries no commented caller stub any more; the stub is how a consuming "+
				"repo learns the shape of the caller — this test has nothing left to guard", name)
		}
	}
}

// fleetPinScrape is the fleet pin-audit's OWN scrape pattern, copied verbatim
// from akira-toriyama/.github .github/workflows/glyph-pin-audit.yml — the first
// `grep -oE` of the pipeline inside the audit step's
// `done < <(printf '%s\n' "$body" | grep -vE '^[[:space:]]*#' | grep -oE …)`
// line (the second -oE there only trims the match down to "@vX.Y.Z"). Go's RE2
// accepts the POSIX classes unchanged, so this is the same matcher byte for byte.
var fleetPinScrape = regexp.MustCompile(`uses:[[:space:]]*akira-toriyama/glyph/[^@[:space:]]*@v[0-9][0-9.]*`)

// TestNaiveGrepSeesNoPinInTheReusables encodes WHY the concrete version is
// absent, so a well-meant "refresh the stubs to the current tag" cannot undo it
// by accident.
//
// The audit's real pipeline drops comment lines FIRST (`grep -vE
// '^[[:space:]]*#'`) precisely because stale stubs used to be reported as fleet
// drift. This test applies the scrape to the RAW text, comment filter removed:
// with the placeholder in place the reusables hold no version for a naive grep
// to find, so the audit is correct whether or not the filter is there — and the
// grep trap (wrong in BOTH directions: false drift from the stubs, and a real
// pin masked by them) is closed at the source rather than worked around
// downstream.
func TestNaiveGrepSeesNoPinInTheReusables(t *testing.T) {
	// Positive control: a matcher that matches nothing would pass this test
	// vacuously for ever. Prove it still bites on the shape it is meant to find.
	const canary = "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.2"
	if !fleetPinScrape.MatchString(canary) {
		t.Fatalf("the audit scrape pattern no longer matches a real pin (%q); the rest of this "+
			"test would pass vacuously — re-copy the pattern from glyph-pin-audit.yml", canary)
	}

	for _, name := range reusables {
		raw := repoFile(t, filepath.Join(".github", "workflows", name))
		if found := fleetPinScrape.FindAllString(raw, -1); len(found) > 0 {
			t.Errorf("%s exposes %d glyph pin(s) to the fleet pin-audit's scrape pattern (%q). "+
				"A reusable DEFINES the pin; it must never CARRY one, not even in a comment: the "+
				"audit reads every repo's YAML, and a version here is either false drift (the stub "+
				"is stale by construction) or masks a real pin. Use @%s.",
				name, len(found), strings.Join(found, ", "), versionPlaceholder)
		}
	}
}

// TestWalkingReusablesCheckOutFullHistory pins `fetch-depth: 0` on every
// reusable whose job runs a glyph command that WALKS.
//
// actions/checkout defaults to fetch-depth: 1 — a shallow clone. glyph now
// reports that as an incomplete walk and refuses the irreversible half of a
// release on it, so losing this line no longer produces a silently wrong
// version. What it produces instead is a repository that can never publish:
// every release run declines to touch the draft and says why, for ever, because
// nothing about a workflow heals itself the way API lag does.
//
// The line is one token in a YAML file that no test compiles and no reviewer
// misses the absence of, and it is only ever noticed by its consequence. That
// is the shape this package exists for.
func TestWalkingReusablesCheckOutFullHistory(t *testing.T) {
	// Positive control: prove the matcher still finds the real thing, so a
	// pattern that has stopped matching cannot pass as four clean files.
	const canary = "          fetch-depth: 0"
	if !fetchDepthFull.MatchString(canary) {
		t.Fatalf("fetchDepthFull no longer matches a real setting (%q) — every case below would pass vacuously", canary)
	}

	// Asserted against the comment-stripped body: the reusables carry commented
	// caller stubs, and a `fetch-depth: 0` sitting in one of those is
	// documentation, not a checkout option any runner obeys.
	for _, name := range []string{"lint.yml", "pr-verdict.yml", "release.yml"} {
		t.Run(name, func(t *testing.T) {
			body := code(repoFile(t, filepath.Join(".github", "workflows", name)))
			if !strings.Contains(body, "actions/checkout") {
				t.Skip("no checkout step to constrain")
			}
			if !fetchDepthFull.MatchString(body) {
				t.Errorf("%s checks out without `fetch-depth: 0`, so the runner gets actions/checkout's "+
					"default depth of 1 — a shallow clone. glyph cannot tell a commit that never landed "+
					"on the released branch from one git does not have, so it reports the walk as "+
					"incomplete and declines to delete or lower the rolling draft. Unlike API lag that "+
					"state never clears on its own: the repository simply stops being able to release.", name)
			}
		})
	}
}

// TestCaskPushUsesDeployKey guards the tap-credential pairing (t-6bhz).
//
// The cask push works only when BOTH halves agree: goreleaser.yml exports
// HOMEBREW_TAP_DEPLOY_KEY into GoReleaser's env, and .goreleaser.yaml's
// repository block resolves that env into an SSH git push
// (repository.git.private_key). Either half alone fails at release time —
// a missing env is a template error, a token-style repository block would
// silently go back to the account-scoped API path the deploy key replaced.
// Nothing in either file can express the pairing on its own, same class as
// TestReleaseNotesReachGoReleaser.
func TestCaskPushUsesDeployKey(t *testing.T) {
	workflow := code(repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml")))
	config := code(repoFile(t, ".goreleaser.yaml"))

	// Half one: the workflow hands the key to GoReleaser.
	if !strings.Contains(workflow, "HOMEBREW_TAP_DEPLOY_KEY: ${{ secrets.HOMEBREW_TAP_DEPLOY_KEY }}") {
		t.Error("goreleaser.yml no longer exports HOMEBREW_TAP_DEPLOY_KEY; the cask push " +
			"template in .goreleaser.yaml resolves that env and fails without it (t-6bhz)")
	}

	// Half two: the config spends it on an SSH push to the tap.
	for _, want := range []string{
		`url: "git@github.com:akira-toriyama/homebrew-tap.git"`,
		`private_key: "{{ .Env.HOMEBREW_TAP_DEPLOY_KEY }}"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf(".goreleaser.yaml no longer contains %q; the cask must be pushed over "+
				"SSH with the tap's write deploy key, not a token (t-6bhz)", want)
		}
	}

	// And the retired PAT must not creep back into either executable body —
	// its account-scoped blast radius is what the deploy key removed.
	for name, body := range map[string]string{"goreleaser.yml": workflow, ".goreleaser.yaml": config} {
		if strings.Contains(body, "HOMEBREW_TAP_TOKEN") {
			t.Errorf("%s references the retired HOMEBREW_TAP_TOKEN; the tap credential is "+
				"the deploy key now (t-6bhz)", name)
		}
	}
}

// The job id inside each reusable, and what a rename costs. These are not
// internal names: GitHub composes a called job's check name as
// "<caller job> / <called job>", so the id here is the SECOND half of the
// status-check context every consumer configures, and glyph owns it.
//
// Measured 2026-08-10 over the non-archived fleet (`gh api
// repos/akira-toriyama/<r>/branches/main/protection`): 21 repositories require
// a context whose second half is `lint` — 20 spell it "lint / lint" and
// .github spells it "commit-lint / lint". The CALLER half varies across the
// fleet and the reusable half does not, which is exactly why the pin belongs
// in glyph's tree rather than in any one consumer's.
//
// None of the three jobs carries a `name:`, so the id IS the name; adding one
// would move the contract without touching a line this test reads, which is
// why the guard below rejects it too.
var reusableJobIDs = map[string]string{
	"lint.yml":       "lint",
	"release.yml":    "release",
	"pr-verdict.yml": "verdict",
}

// topLevelJobKey matches a job id: a two-space-indented key directly under
// `jobs:`. Anchored to the line so a `lint:` appearing at any other depth — a
// step's `with:` entry, a nested map — cannot be mistaken for a job.
var topLevelJobKey = regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):[ \t]*$`)

// jobIDsIn returns the job ids declared under `jobs:` in a workflow body,
// which must already be comment-stripped.
func jobIDsIn(body string) []string {
	i := strings.Index(body, "\njobs:\n")
	if i < 0 {
		return nil
	}
	rest := body[i+len("\njobs:\n"):]
	// The jobs mapping ends at the first line that starts a new top-level key.
	if end := regexp.MustCompile(`(?m)^[A-Za-z0-9_-]+:`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	var ids []string
	for _, m := range topLevelJobKey.FindAllStringSubmatch(rest, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// TestReusableJobIDsAreTheContextsConsumersRequire pins each reusable's job id,
// and pins that each declares exactly ONE job.
//
// A rename never goes red where it is written. The renamed job reports under a
// context nothing requires, the required context never reports at all, and the
// pull request sits at "Expected — waiting for status to be reported" forever:
// 21 repositories unable to merge until a human edits branch protection in each
// one. Adding a SECOND job to a reusable has the mirror-image cost — every
// consumer gains a check name it never configured — so the count is pinned with
// the names.
//
// This is the same class as `lint --stdin=false` exiting 3: a gate that answers
// something other than what its consumers read, while nothing in the tree
// records what they read. The three ids were load-bearing before this test and
// asserted nowhere; `verdict` was reachable only as a substring of
// TestPRVerdictExposesTheVerdictToCallers's expression strings, which pins the
// outputs wiring and says nothing about the id being a published name.
//
// bite-exempt: a guard over an invariant the tree already holds — the ids are
// correct today and the point is that nothing said so, so it cannot fail
// against the pre-change source. What it must do instead is fail under a
// rename, and that is measured by the mutation ledger row
// reusable-job-id-renamed-out-from-under-the-fleet.patch.
func TestReusableJobIDsAreTheContextsConsumersRequire(t *testing.T) {
	for _, name := range reusables {
		t.Run(name, func(t *testing.T) {
			raw := repoFile(t, filepath.Join(".github", "workflows", name))
			body := code(raw)
			want := reusableJobIDs[name]

			got := jobIDsIn(body)
			if len(got) != 1 || got[0] != want {
				t.Fatalf("%s declares jobs %v, want exactly [%s] — the job id is the second "+
					"half of the check-run context every caller configures "+
					"(measured 2026-08-10: 21 fleet repositories require one ending in `/ lint`, "+
					"under two different caller halves). A rename does not fail here; it makes the "+
					"required context stop reporting, and every one of those repositories blocks on "+
					"a check that will never arrive",
					name, got, want)
			}

			// A `name:` on the job would override the id in the context without
			// changing the id itself, moving the fleet's contract past this guard.
			if jobNameOverride(body, want) {
				t.Errorf("%s gives job %q an explicit `name:`; the check-run context is composed "+
					"from the display name when one exists, so this silently renames the context "+
					"21 repositories require while leaving the job id above untouched", name, want)
			}
		})
	}
}

// jobNameOverride reports whether the named job declares its own `name:` key —
// a four-space-indented `name:` inside that job's block, before the next job.
func jobNameOverride(body, job string) bool {
	i := strings.Index(body, "\n  "+job+":\n")
	if i < 0 {
		return false
	}
	rest := body[i+1:]
	if end := topLevelJobKey.FindStringIndex(rest[len("  "+job+":\n"):]); end != nil {
		rest = rest[:len("  "+job+":\n")+end[0]]
	}
	return regexp.MustCompile(`(?m)^    name:[ \t]`).MatchString(rest)
}

// TestJobIDsInFindsARealJobsBlock is the positive control for jobIDsIn: a
// scanner that silently returned nothing would make the guard above vacuous on
// every file at once. It asserts the parser both FINDS a known job and REFUSES
// the shapes that look like one — a deeper key, and a workflow with no `jobs:`.
//
// bite-exempt: a positive control by definition. It exists so the guard above
// cannot pass over an empty match set, which means it must hold both before and
// after the change.
func TestJobIDsInFindsARealJobsBlock(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "lint.yml")))
	if got := jobIDsIn(body); len(got) != 1 || got[0] != "lint" {
		t.Fatalf("jobIDsIn on the real lint.yml returned %v, want [lint] — the scanner "+
			"must actually match, or every assertion built on it passes over nothing", got)
	}

	const nested = "on:\n  workflow_call:\n    inputs:\n      lint:\n        type: string\n"
	if got := jobIDsIn(nested); got != nil {
		t.Fatalf("jobIDsIn found %v in a workflow with no jobs: block, want none", got)
	}

	const decoy = "jobs:\n  real:\n    steps:\n      - run: true\n        name: not-a-job\n"
	if got := jobIDsIn("\n" + decoy); len(got) != 1 || got[0] != "real" {
		t.Fatalf("jobIDsIn returned %v for a jobs block with one job and a nested key, want [real]", got)
	}
}
