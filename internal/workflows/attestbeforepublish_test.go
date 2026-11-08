package workflows

// goreleaser.yml's publish order (t-p15y): nothing is public before its
// attestation exists. The release job uploads onto a draft, attests, then
// publishes; the cask job moves the tap afterwards, over the cask the
// published release carries. v6.1.0 ran the other way — release published
// 05:49:38, cask pushed 05:49:39, attestation created 05:49:47 (run
// 40457384503) — and the install action verifies provenance fail-closed, so
// an attest step that failed would have left a public release no fleet
// repository could install. Every decision below is one line of YAML or
// shell CI stays green over, so each is held here and re-broken by the
// mutation ledger.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The release job's steps in the order the contract needs them, the gh step
// that reads the tag's release and splits the job, the condition every step
// after it carries, and the cask job's pieces.
const (
	goreleaserStep      = "GoReleaser"
	attestStep          = "attest build provenance"
	publishStep         = "publish the release"
	publishedTagGuard   = "the release the tag already has"
	buildCondition      = "steps.existing.outputs.release != 'published'"
	fetchCaskStep       = "fetch the cask from the published release"
	caskPushStep        = "push the cask to the tap"
	caskAsset           = "glyph.rb"
	caskFile            = "dist/homebrew/Casks/glyph.rb"
	attestSubjects      = "subject-path: 'dist/*.tar.gz, dist/checksums.txt, " + caskFile + "'"
	caskJobCondition    = "if: needs.release.outputs.prerelease != 'true'"
	prereleaseJobOutput = "prerelease: ${{ steps.tag.outputs.prerelease }}"
)

func goreleaserWorkflow(t *testing.T) string {
	t.Helper()
	return repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml"))
}

// stepAt is the offset of `- name: <name>` in raw; a step the file does not
// carry, or carries twice, fails the test rather than ordering nothing.
func stepAt(t *testing.T, raw, name string) int {
	t.Helper()
	marker := "- name: " + name + "\n"
	i := strings.Index(raw, marker)
	if i < 0 {
		t.Fatalf("goreleaser.yml has no step named %q — it was renamed and this guard orders nothing", name)
	}
	if strings.Contains(raw[i+len(marker):], marker) {
		t.Fatalf("goreleaser.yml names two steps %q", name)
	}
	return i
}

// jobBlock is the comment-stripped text of the job `id` in body: from its key
// to the next job's key, or to the end of the jobs mapping.
func jobBlock(t *testing.T, body, id string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, "\n  "+id+":\n")
	if !ok {
		t.Fatalf("goreleaser.yml declares no job %q", id)
	}
	if end := topLevelJobKey.FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	if end := nextRootKey.FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	return rest
}

// ghStub is a gh on PATH that records its argv, prints stdout and exits with
// code; the steps under test call gh once each.
func ghStub(t *testing.T, stdout string, code int) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls = filepath.Join(dir, "gh-calls")
	body := recordArgv(calls)
	if stdout != "" {
		body += "printf '%s\\n' " + shq(stdout) + "\n"
	}
	writeStub(t, bin, "gh", body+"exit "+strconv.Itoa(code)+"\n")
	return bin, calls
}

// TestReleaseGoesPublicOnlyAfterItsAttestation holds the two halves of the
// contract that neither file can state alone: .goreleaser.yaml keeps the
// release a draft (`draft: true` — GoReleaser's own publish would otherwise
// run before any step outside it), and goreleaser.yml publishes with gh only
// after the attest step, as the release job's last step. The publish step is
// executed over a stub gh: the un-draft is one gh call naming the tag, and a
// gh that fails fails the step (and the job: TestGoreleaserStepFailuresFailTheirJob).
// Reverting `draft: true` is one word CI stays green over (mutation row
// release-public-before-its-attestation).
func TestReleaseGoesPublicOnlyAfterItsAttestation(t *testing.T) {
	config := code(repoFile(t, ".goreleaser.yaml"))
	for _, want := range []string{"draft: true", "use_existing_draft: true", "replace_existing_artifacts: true"} {
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(want) + `\s*$`).MatchString(config) {
			t.Errorf(".goreleaser.yaml's release block no longer sets `%s`", want)
		}
	}
	publishesItself := regexp.MustCompile(`(?m)^\s*draft:\s*["']?false`)
	// Positive control: the line 1015774 wrote and t-p15y replaced.
	const canary = "  draft: false"
	if !publishesItself.MatchString(canary) {
		t.Fatalf("the pattern no longer matches the draft line it bans (%q) — the absence asserted below is vacuous", canary)
	}
	if publishesItself.MatchString(config) {
		t.Error(".goreleaser.yaml sets `draft: false`, so GoReleaser publishes the release inside its own run — " +
			"before the attest step can run; v6.1.0's assets were public 9s before their attestation existed (t-p15y)")
	}

	raw := goreleaserWorkflow(t)
	order := []string{goreleaserStep, attestStep, publishStep}
	for i := 1; i < len(order); i++ {
		if stepAt(t, raw, order[i-1]) >= stepAt(t, raw, order[i]) {
			t.Errorf("goreleaser.yml runs %q before %q; the order is upload → attest → publish, "+
				"so nothing is public before its attestation exists", order[i], order[i-1])
		}
	}
	for _, name := range order {
		if p := readStepShape(t, raw, name).parents; len(p) < 2 || p[0] != "steps" || p[1] != "release" {
			t.Errorf("%q sits under %v, want the release job's steps — the order above is only an order inside one job", name, p)
		}
	}
	if steps := releaseSteps(t, raw); steps[len(steps)-1].keys["name"] != publishStep {
		t.Errorf("the release job's last step is %q, want %q: a step after the publish fails the job after its "+
			"point of no return, and the cask job then never runs", steps[len(steps)-1].keys["name"], publishStep)
	}
	attest := readStepShape(t, raw, attestStep)
	if got := attest.keys["uses"]; !strings.HasPrefix(got, "actions/attest-build-provenance@") {
		t.Errorf("the attest step uses %q, want actions/attest-build-provenance", got)
	}
	if !strings.Contains(code(raw), attestSubjects) {
		t.Errorf("the attest step no longer names %q — the install action verifies the tarball it downloads, "+
			"and an attestation over any other set leaves it unverifiable; the cask file is an asset too", attestSubjects)
	}

	// The publish step, executed.
	shape := readStepShape(t, raw, publishStep)
	if shape.keys["env"] != "" || !strings.Contains(stepBlockOf(t, raw, publishStep), "GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}") {
		t.Errorf("the publish step does not hand gh the job's token through env GH_TOKEN")
	}
	if !strings.Contains(stepBlockOf(t, raw, publishStep), "TAG: ${{ github.ref_name }}") {
		t.Errorf("the publish step is not told the tag through env TAG")
	}
	script := extractRun(t, raw, publishStep)
	for _, c := range []struct {
		name   string
		ghExit int
		exit   int
	}{
		{"the un-draft is one gh call", 0, 0},
		{"a gh that fails fails the step", 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, calls := ghStub(t, "", c.ghExit)
			run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GH_TOKEN=stub", "TAG=v9.8.7",
			})
			if run.exit != c.exit {
				t.Fatalf("step exited %d, want %d\n%s", run.exit, c.exit, run)
			}
			want := [][]string{{"release", "edit", "v9.8.7", "--draft=false"}}
			if got := recordedCalls(t, calls); !slices.EqualFunc(got, want, slices.Equal) {
				t.Errorf("the step ran gh as %q, want %q", got, want)
			}
		})
	}
}

// stepBlockOf is the raw text of the named step, from its `- name:` line to
// the next step at the same indent.
func stepBlockOf(t *testing.T, raw, name string) string {
	t.Helper()
	i := stepAt(t, raw, name)
	block := raw[i:]
	if j := strings.Index(block[1:], "\n      - "); j >= 0 {
		block = block[:j+1]
	}
	return block
}

// releaseStep is one step of the release job as the runner reads it: its own
// keys and the lines nested under each.
type releaseStep struct {
	keys   map[string]string
	nested map[string][]string
}

// releaseSteps reads the release job's steps in order from the
// comment-stripped workflow.
func releaseSteps(t *testing.T, raw string) []releaseStep {
	t.Helper()
	var steps []releaseStep
	for _, block := range stepBlocks(jobBlock(t, code(raw), "release")) {
		keys, nested := stepKeys(block)
		steps = append(steps, releaseStep{keys: keys, nested: nested})
	}
	if len(steps) < 3 {
		t.Fatalf("the release job has %d steps — the step splitter's indent assumption broke", len(steps))
	}
	return steps
}

// TestPublishedTagIsNotRebuiltOver holds the step that splits the release
// job and the split itself. The step asks one GraphQL question —
// `repository.release(tagName:)`, which returns a draft by its pending tag
// as well as a published release — and writes its answer, none / draft /
// published, to GITHUB_OUTPUT; every step after it, setup-go through the
// publish, runs only while the answer is not `published`, and nothing before
// it is conditional. Over a published tag the job therefore succeeds having
// built, uploaded, attested and published nothing, and the cask job runs —
// the one path that finishes a release whose cask job failed after the
// publish, where a refusal left the cask unpushable by any re-run. The
// refusal to rebuild exists because of `replace_existing_artifacts` — there
// for the draft a failed run leaves — which over a PUBLISHED release would
// swap the public assets for a rebuild's differently stamped bytes. A lookup
// that fails, or answers without the repository or with nothing, fails the
// step before anything is built: `gh release view` would have reported
// "release not found" over a REST failure on a published tag (cli/cli
// FetchRelease v2.103.3 answers with whichever of its two lookups succeeds
// and reports not-found only when both fail, and the draft lookup fails that
// way for every non-draft), and the job would have minted a second release
// on a published tag. Two of the fail-closed legs are shell, not jq, and are
// held by their own cases: `pipefail`, for a gh that exits 1 over a body jq
// can still map, and the `*)` arm, for an empty body that maps to no word.
func TestPublishedTagIsNotRebuiltOver(t *testing.T) {
	requireTool(t, "jq")
	raw := goreleaserWorkflow(t)
	at := stepAt(t, raw, publishedTagGuard)
	if at <= stepAt(t, raw, moduleMajorStep) || at >= stepAt(t, raw, goreleaserStep) {
		t.Errorf("the release-state step does not sit between the tag check and GoReleaser")
	}
	guard := readStepShape(t, raw, publishedTagGuard)
	if guard.keys["id"] != "existing" {
		t.Errorf("the release-state step has id %q, want existing — every later step reads steps.existing", guard.keys["id"])
	}

	// The split: the steps before and including the state step run on every
	// attempt; every step after it carries the condition, verbatim.
	steps := releaseSteps(t, raw)
	split := slices.IndexFunc(steps, func(s releaseStep) bool { return s.keys["name"] == publishedTagGuard })
	if split < 0 {
		t.Fatalf("the release job's steps do not include %q", publishedTagGuard)
	}
	for i, s := range steps {
		label := s.keys["name"]
		if label == "" {
			label = s.keys["uses"]
		}
		cond, has := s.keys["if"]
		switch {
		case i <= split && has:
			t.Errorf("step %q (before the state step) carries `if: %s`; the checkout, the tag check and the state "+
				"step run on every attempt", label, cond)
		case i > split && cond != buildCondition:
			t.Errorf("step %q carries `if: %s`, want exactly `%s`: over a published tag it would run — a rebuild "+
				"over public assets, an attest over an empty dist, a publish of nothing — or never run", label, cond, buildCondition)
		}
	}
	if n := len(steps) - split - 1; n != 6 {
		t.Errorf("%d steps follow the state step, want 6 (setup-go, build, notes, GoReleaser, attest, publish) — "+
			"a step added here needs the condition and this count", n)
	}

	// The state step, executed over a stub gh answering as the API does and
	// the real jq that reads it.
	shape := stepBlockOf(t, raw, publishedTagGuard)
	for _, want := range []string{"GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}", "REPO: ${{ github.repository }}", "TAG: ${{ github.ref_name }}"} {
		if !strings.Contains(shape, want) {
			t.Errorf("the state step's env lacks %q", want)
		}
	}
	script := extractRun(t, raw, publishedTagGuard)
	cases := []struct {
		name     string
		ghStdout string
		ghExit   int
		exit     int
		output   string // GITHUB_OUTPUT after the step
		line     string // a substring of one stdout line
	}{
		{"no release yet builds", `{"data":{"repository":{"release":null}}}`, 0, 0, "release=none\n", "no release for v9.8.7 yet"},
		{"a draft is reused", `{"data":{"repository":{"release":{"isDraft":true}}}}`, 0, 0, "release=draft\n", "a draft release for v9.8.7 exists"},
		{"a published release skips the build", `{"data":{"repository":{"release":{"isDraft":false}}}}`, 0, 0, "release=published\n", "::notice::v9.8.7 is already published"},
		// gh dead or offline: no answer, exit 1 — the pipeline fails at gh.
		{"a gh that fails with no answer fails the step", "", 1, 1, "", ""},
		// A GraphQL error: gh prints the body and exits 1, and jq's error over
		// the null repository is the pipeline's last non-zero status.
		{"a GraphQL error fails the step", `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND"}]}`, 1, 5, "", ""},
		// A GraphQL error beside a body jq maps to a word (the release field
		// forbidden, the repository named): jq exits 0 with "none", so only
		// `pipefail` carries gh's 1 — without it the step builds a second
		// release onto whatever the tag has.
		{"a gh that fails over a partial answer fails the step", `{"data":{"repository":{"release":null}},"errors":[{"type":"FORBIDDEN","path":["repository","release"]}]}`, 1, 1, "", ""},
		// gh exits 0 with no body: jq maps nothing, the word is "", and only
		// the `*)` arm refuses it — without it the step writes `release=` and
		// every later step reads "" != 'published'.
		{"an empty answer from a gh that succeeds fails the step", "", 0, 1, "", "::error::the lookup of v9.8.7's release answered"},
		{"an answer without the repository fails the step", `{"data":{"repository":null}}`, 0, 5, "", ""},
		{"an empty answer fails the step", `{}`, 0, 5, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin, calls := ghStub(t, c.ghStdout, c.ghExit)
			outputs := filepath.Join(t.TempDir(), "github-output")
			run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GH_TOKEN=stub", "REPO=akira-toriyama/glyph", "TAG=v9.8.7", "GITHUB_OUTPUT=" + outputs,
			})
			if run.exit != c.exit {
				t.Fatalf("step exited %d, want %d\n%s", run.exit, c.exit, run)
			}
			if got := readIfAny(t, outputs); got != c.output {
				t.Errorf("GITHUB_OUTPUT holds %q, want %q — the six later steps read this word", got, c.output)
			}
			if c.line != "" && !strings.Contains(run.stdout, c.line) {
				t.Errorf("stdout carries no %q\n%s", c.line, run)
			}
			got := recordedCalls(t, calls)
			if len(got) != 1 {
				t.Fatalf("the step called gh %d times, want once: %q", len(got), got)
			}
			argv := got[0]
			if len(argv) < 2 || argv[0] != "api" || argv[1] != "graphql" {
				t.Errorf("the step ran gh %q, want `gh api graphql …`", argv)
			}
			for _, bound := range []string{"owner=akira-toriyama", "name=glyph", "tag=v9.8.7"} {
				if i := slices.Index(argv, bound); i < 1 || argv[i-1] != "-F" {
					t.Errorf("gh argv %q does not bind %q with -F", argv, bound)
				}
			}
			q := slices.IndexFunc(argv, func(a string) bool { return strings.HasPrefix(a, "query=") })
			if q < 0 || !strings.Contains(argv[q], "release(tagName: $tag)") || !strings.Contains(argv[q], "isDraft") {
				t.Errorf("gh argv %q carries no query asking repository.release(tagName:) for isDraft", argv)
			}
		})
	}
}

// TestGoreleaserStepFailuresFailTheirJob: no step of goreleaser.yml, in
// either job, carries `continue-on-error`. One rule, because every step is
// one the next cannot run after: a step failed and made green keeps its
// job's `success()` for the steps behind it, so a lookup that failed builds
// over a published tag, a GoReleaser that failed after creating the draft
// (a draft with 0 assets, measured at v2.20.0 on glyph-test-2) is attested
// and published as it stands — every fleet install then 404s and every
// later run takes the published path, red for ever — a failed signing or
// un-draft hands the cask job a draft or an unattested release, and a
// failed fetch or push is a cask job "re-run failed jobs" never offers. The
// job must fail at the step so a re-run lands on the draft.
func TestGoreleaserStepFailuresFailTheirJob(t *testing.T) {
	body := code(goreleaserWorkflow(t))
	jobs := jobIDsIn(body)
	if len(jobs) != 2 {
		t.Fatalf("goreleaser.yml declares jobs %q, want the release and cask jobs", jobs)
	}
	steps := 0
	for _, id := range jobs {
		for _, s := range stepBlocks(jobBlock(t, body, id)) {
			steps++
			keys, _ := stepKeys(s)
			label := keys["name"]
			if label == "" {
				label = keys["uses"]
			}
			if v, ok := keys["continue-on-error"]; ok {
				t.Errorf("the %s job's step %q carries `continue-on-error: %s`: its failure would leave the job green and "+
					"the steps behind it running over what it left half-done", id, label, v)
			}
		}
	}
	if steps < 11 {
		t.Fatalf("goreleaser.yml has %d steps across its jobs, want at least 11 — the step splitter read past them", steps)
	}
}

// TestGoreleaserAttestsWhatItUploads pins the attest step and the grants it
// runs under. Deleting the step, or the `id-token: write` the keyless signing
// needs, reds nothing here while the install action keeps verifying
// fail-closed — so the next tag is a release the whole fleet cannot install
// (t-j0es: both measured surviving the suite at 2f9428a). The step runs on
// exactly the runs GoReleaser ran on: its `if:` is the build condition and
// nothing else. The grants are read from the release job's own `permissions:`
// block, the one place they are declared
// (TestGoreleaserGrantsStayInTheJobThatUsesThem holds the layout).
func TestGoreleaserAttestsWhatItUploads(t *testing.T) {
	body := code(goreleaserWorkflow(t))
	attest := readStepShape(t, goreleaserWorkflow(t), attestStep)
	if got := attest.keys["uses"]; !strings.HasPrefix(got, "actions/attest-build-provenance@") {
		t.Fatalf("goreleaser.yml's attest step uses %q, want actions/attest-build-provenance — without it every "+
			"later install fails `gh attestation verify`", got)
	}
	if got := attest.keys["if"]; got != buildCondition {
		t.Errorf("the attest step carries `if: %s`, want exactly `%s`: it signs on every run that uploaded, and "+
			"any other condition is a run that uploads and does not sign", got, buildCondition)
	}
	if !strings.Contains(body, attestSubjects) {
		t.Errorf("the attest step no longer names %q", attestSubjects)
	}
	grants := jobGrants(t, body, "release")
	for _, scope := range []string{"id-token", "attestations"} {
		if !slices.Contains(grants, scope+": write") {
			t.Errorf("the release job's permissions block %q lacks `%s: write`; the attest step then fails with every "+
				"asset already uploaded, or never signs — and the install action verifies fail-closed", grants, scope)
		}
	}
}

// jobGrants is the sorted `scope: level` list of the job's own `permissions:`
// block in the comment-stripped body — `{}` is an empty list, and a job with
// no block fails the test: it would inherit the workflow level, and
// goreleaser.yml's grants nothing.
func jobGrants(t *testing.T, body, id string) []string {
	t.Helper()
	job := jobBlock(t, body, id)
	if regexp.MustCompile(`(?m)^    permissions:\s*\{\s*\}\s*$`).MatchString(job) {
		return []string{}
	}
	_, rest, ok := strings.Cut(job, "\n    permissions:\n")
	if !ok {
		t.Fatalf("the %s job declares no `permissions:` block of its own, so it runs under the workflow level's — "+
			"`permissions: {}`, which grants nothing", id)
	}
	var grants []string
	for line := range strings.SplitSeq(rest, "\n") {
		m := jobGrant.FindStringSubmatch(line)
		if m == nil {
			break
		}
		grants = append(grants, m[1]+": "+m[2])
	}
	slices.Sort(grants)
	return grants
}

// jobGrant is one scope line of a job-level permissions block: six-space
// indented, a trailing comment allowed.
var jobGrant = regexp.MustCompile(`^      ([a-z-]+):\s*(read|write|none)\b`)

// writeGrant is any `<scope>: write` line at any depth.
var writeGrant = regexp.MustCompile(`(?m)^\s*[a-z-]+:\s*write\b`)

// TestGoreleaserGrantsStayInTheJobThatUsesThem: goreleaser.yml's workflow
// level grants nothing, the release job declares the three writes its steps
// use and nothing else, and the cask job declares `contents: read` alone —
// it reads the published release's cask asset and writes nothing through
// GITHUB_TOKEN (the push authenticates with the tap's deploy key). With two
// jobs, a write at the workflow level reaches the cask job as well — a job
// that holds the tap's deploy key — and zizmor's excessive-permissions, the
// hub's required gate, refuses it (1.31.1 on #347, run 41830717292, exit 14,
// after the single-job file had passed for a year). zizmor accepts any grant
// declared at the job level, so the cask job's read-only block and the
// release job's exact set are held here only.
func TestGoreleaserGrantsStayInTheJobThatUsesThem(t *testing.T) {
	body := code(goreleaserWorkflow(t))
	top, _, ok := strings.Cut(body, "\njobs:\n")
	if !ok {
		t.Fatal("goreleaser.yml has no `jobs:` mapping")
	}
	// Positive control: the matcher the absences below rest on finds the
	// file's three real write grants, wherever they are declared.
	if found := writeGrant.FindAllString(body, -1); len(found) != 3 {
		t.Fatalf("writeGrant finds %d write grant(s) in goreleaser.yml (%q), want 3 — the absences asserted "+
			"below would be vacuous", len(found), found)
	}

	if !regexp.MustCompile(`(?m)^permissions:\s*\{\s*\}\s*$`).MatchString(top) {
		t.Error("goreleaser.yml's workflow level is not `permissions: {}`; whatever it grants reaches every job, " +
			"the cask job included")
	}
	if found := writeGrant.FindAllString(top, -1); len(found) > 0 {
		t.Errorf("goreleaser.yml grants %q at the workflow level — zizmor's excessive-permissions reds the "+
			"required gate over a write there, and the cask job runs under it", found)
	}

	want := []string{"attestations: write", "contents: write", "id-token: write"}
	if got := jobGrants(t, body, "release"); !slices.Equal(got, want) {
		t.Errorf("the release job's grants are %q, want exactly %q: the draft and its publish (contents), "+
			"keyless signing (id-token) and the attestation itself (attestations)", got, want)
	}
	if got := jobGrants(t, body, "cask"); !slices.Equal(got, []string{"contents: read"}) {
		t.Errorf("the cask job's grants are %q, want exactly [\"contents: read\"]: it downloads the published "+
			"release's cask with GITHUB_TOKEN and pushes with the tap's deploy key, so any write there is a "+
			"credential nothing in the job uses", got)
	}
	for _, id := range jobIDsIn(body) {
		jobGrants(t, body, id)
	}
}

// TestCaskGoesOutOnlyAfterThePublish: the tap names the release's assets, so
// the push is its own job, after the release job (`needs:`), over the cask
// file that job uploaded onto the release (.goreleaser.yaml `extra_files` —
// a rebuild would stamp a new build date and a regenerated cask would carry
// the wrong sha256; a run artifact belongs to one run, where the release is
// reachable from a re-run attempt and a fresh run alike). Dropping `needs:`
// runs the two jobs side by side and reopens the brew-404 window a draft once
// held open for 17 days (cifail v0.1.1). The fetch step is executed over a
// stub gh: one `gh release download` naming the tag, the repository (the job
// checks nothing out), the asset and the directory the push step reads. The
// push step is executed over a real git and a local bare tap: it commits the
// cask under the hub's update-tap identity and subject, pushes once, is a
// no-op over a tap that already carries the cask (a re-run whose push
// landed), moves a tap that carries an older glyph, refuses to move one that
// carries a newer glyph (a re-run of an older release's cask job after a
// later release reached the tap; the hub's update-tap refuses the same
// backward move), and refuses an empty deploy key before cloning anything.
func TestCaskGoesOutOnlyAfterThePublish(t *testing.T) {
	raw := goreleaserWorkflow(t)
	body := code(raw)
	cask := jobBlock(t, body, "cask")
	if !regexp.MustCompile(`(?m)^    needs:\s*release\s*$`).MatchString(cask) {
		t.Error("the cask job does not `needs: release`; it would push the tap while the release job is still " +
			"uploading onto a draft — `brew install` fetching 404s until the publish, or for ever if it fails")
	}
	for _, s := range stepBlocks(cask) {
		keys, _ := stepKeys(s)
		if v, ok := keys["if"]; ok {
			t.Errorf("the cask job's step %q carries `if: %s`; the job runs whole or not at all — the job-level `if:` "+
				"is the one condition, and a step that stays home leaves the tap behind the release", keys["name"], v)
		}
	}
	config := code(repoFile(t, ".goreleaser.yaml"))
	if !regexp.MustCompile(`(?m)^  extra_files:\n    - glob: ` + regexp.QuoteMeta(caskFile) + `\s*$`).MatchString(config) {
		t.Errorf(".goreleaser.yaml's release block does not upload %s as `extra_files`; the cask job then has "+
			"nothing to fetch and a release whose cask job failed can never reach the tap", caskFile)
	}
	if filepath.Base(caskFile) != caskAsset {
		t.Fatalf("the asset GoReleaser names (%s) is not the pattern the fetch step downloads (%s)", filepath.Base(caskFile), caskAsset)
	}
	push := stepBlockOf(t, raw, caskPushStep)
	if !strings.Contains(push, "CASK: ${{ runner.temp }}/cask/"+caskAsset) {
		t.Error("the push step's CASK does not point into the directory the fetch step fills")
	}

	// The fetch step, executed.
	fetch := extractRun(t, raw, fetchCaskStep)
	for _, want := range []string{"GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}", "REPO: ${{ github.repository }}", "TAG: ${{ github.ref_name }}"} {
		if !strings.Contains(stepBlockOf(t, raw, fetchCaskStep), want) {
			t.Errorf("the fetch step's env lacks %q", want)
		}
	}
	for _, c := range []struct {
		name   string
		ghExit int
		exit   int
	}{
		{"the fetch is one gh release download", 0, 0},
		{"a gh that fails fails the fetch", 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, calls := ghStub(t, "", c.ghExit)
			temp := t.TempDir()
			run := runStep(t, fetch, unspecifiedShell, temp, []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GH_TOKEN=stub", "REPO=akira-toriyama/glyph", "TAG=v9.8.7", "RUNNER_TEMP=" + temp,
			})
			if run.exit != c.exit {
				t.Fatalf("step exited %d, want %d\n%s", run.exit, c.exit, run)
			}
			want := [][]string{{"release", "download", "v9.8.7", "--repo", "akira-toriyama/glyph", "--pattern", caskAsset, "--dir", filepath.Join(temp, "cask")}}
			if got := recordedCalls(t, calls); !slices.EqualFunc(got, want, slices.Equal) {
				t.Errorf("the step ran gh as %q, want %q", got, want)
			}
		})
	}

	// The push step, executed.
	requireTool(t, "git")
	script := extractRun(t, raw, caskPushStep)
	git := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=seed", "GIT_AUTHOR_EMAIL=seed@example.invalid", "GIT_COMMITTER_NAME=seed", "GIT_COMMITTER_EMAIL=seed@example.invalid"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	caskAt := func(version string) string { return "cask \"glyph\" do\n  version \"" + version + "\"\nend\n" }
	// newTap seeds a bare tap with another cask and, when glyphVersion is not
	// empty, a glyph cask at that version.
	newTap := func(t *testing.T, glyphVersion string) (bare string) {
		t.Helper()
		dir := t.TempDir()
		bare = filepath.Join(dir, "homebrew-tap.git")
		seed := filepath.Join(dir, "seed")
		git(t, dir, "init", "--quiet", "--bare", "--initial-branch=main", bare)
		git(t, dir, "init", "--quiet", "--initial-branch=main", seed)
		if err := os.MkdirAll(filepath.Join(seed, "Casks"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(seed, "Casks", "other.rb"), []byte("cask \"other\" do\nend\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if glyphVersion != "" {
			if err := os.WriteFile(filepath.Join(seed, "Casks", "glyph.rb"), []byte(caskAt(glyphVersion)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		git(t, seed, "add", "Casks")
		git(t, seed, "commit", "--quiet", "-m", ":package:(tap)= bump other to v1.0.0")
		git(t, seed, "push", "--quiet", bare, "HEAD:main")
		return bare
	}
	rendered := caskAt("9.8.7")
	run := func(t *testing.T, bare, key string) (stepRun, string) {
		t.Helper()
		temp := t.TempDir()
		caskPath := filepath.Join(temp, "downloaded.rb")
		if err := os.WriteFile(caskPath, []byte(rendered), 0o600); err != nil {
			t.Fatal(err)
		}
		return runStep(t, script, unspecifiedShell, temp, []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + temp, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"RUNNER_TEMP=" + temp, "HOMEBREW_TAP_DEPLOY_KEY=" + key, "TAG=v9.8.7", "CASK=" + caskPath, "TAP_URL=" + bare,
		}), temp
	}
	const stubKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nstub\n-----END OPENSSH PRIVATE KEY-----"

	t.Run("the cask lands in the tap as one lint-clean commit", func(t *testing.T) {
		bare := newTap(t, "")
		r, temp := run(t, bare, stubKey)
		if r.exit != 0 {
			t.Fatalf("step exited %d\n%s", r.exit, r)
		}
		if got := git(t, bare, "log", "-1", "--format=%s%n%an <%ae>", "main"); got != ":package:(tap)= bump glyph to v9.8.7\ngithub-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>" {
			t.Errorf("the tap's head is\n%s\nwant the hub's update-tap subject and identity", got)
		}
		if got := git(t, bare, "show", "main:Casks/glyph.rb"); got != strings.TrimSpace(rendered) {
			t.Errorf("the tap's Casks/glyph.rb is %q, want the downloaded cask", got)
		}
		if got := git(t, bare, "rev-list", "--count", "main"); got != "2" {
			t.Errorf("the tap has %s commits, want 2 (the seed and the cask)", got)
		}
		info, err := os.Stat(filepath.Join(temp, "tap-deploy-key"))
		if err != nil {
			t.Fatalf("the step wrote no deploy key file under RUNNER_TEMP: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("the deploy key file is %v, want 0600 — ssh refuses a readable private key", info.Mode().Perm())
		}
		if !strings.Contains(script, `export GIT_SSH_COMMAND="ssh -i $key -o IdentitiesOnly=yes`) {
			t.Error("the step does not hand git the deploy key through GIT_SSH_COMMAND — the push would use the runner's own identity")
		}

		r2, _ := run(t, bare, "stub-key")
		if r2.exit != 0 || !strings.Contains(r2.stdout, "already carries this cask") {
			t.Errorf("a re-run over a tap that already carries the cask exited %d\n%s\nwant 0 with nothing to push", r2.exit, r2)
		}
		if got := git(t, bare, "rev-list", "--count", "main"); got != "2" {
			t.Errorf("the re-run added a commit (%s on the tap, want 2)", got)
		}
	})

	t.Run("a tap carrying an older glyph is moved forward", func(t *testing.T) {
		bare := newTap(t, "9.8.6")
		r, _ := run(t, bare, stubKey)
		if r.exit != 0 {
			t.Fatalf("step exited %d\n%s", r.exit, r)
		}
		if got := git(t, bare, "show", "main:Casks/glyph.rb"); got != strings.TrimSpace(rendered) {
			t.Errorf("the tap's Casks/glyph.rb is %q, want the 9.8.7 cask", got)
		}
	})

	t.Run("a tap carrying a newer glyph is not moved back", func(t *testing.T) {
		bare := newTap(t, "9.8.10")
		r, _ := run(t, bare, stubKey)
		if r.exit != 1 {
			t.Fatalf("step exited %d, want 1\n%s", r.exit, r)
		}
		if !strings.Contains(r.stdout, "::error::the tap carries glyph 9.8.10, newer than v9.8.7") {
			t.Errorf("no ::error:: names the newer version the tap carries\n%s", r)
		}
		if got := git(t, bare, "show", "main:Casks/glyph.rb"); got != strings.TrimSpace(caskAt("9.8.10")) {
			t.Errorf("the tap's Casks/glyph.rb is %q, want the 9.8.10 cask untouched", got)
		}
		if got := git(t, bare, "rev-list", "--count", "main"); got != "1" {
			t.Errorf("the refusal added a commit (%s on the tap, want 1)", got)
		}
	})

	t.Run("an empty deploy key is refused before anything is cloned", func(t *testing.T) {
		bare := newTap(t, "")
		r, temp := run(t, bare, "")
		if r.exit != 1 {
			t.Fatalf("step exited %d, want 1\n%s", r.exit, r)
		}
		if !strings.Contains(r.stdout, "::error::HOMEBREW_TAP_DEPLOY_KEY is empty") {
			t.Errorf("no ::error:: names the missing secret\n%s", r)
		}
		if _, err := os.Stat(filepath.Join(temp, "tap")); err == nil {
			t.Error("the step cloned the tap with no key to push with")
		}
		if got := git(t, bare, "rev-list", "--count", "main"); got != "1" {
			t.Errorf("the tap moved (%s commits, want 1)", got)
		}
	})
}
