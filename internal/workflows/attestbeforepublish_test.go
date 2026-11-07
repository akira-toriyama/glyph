package workflows

// goreleaser.yml's publish order (t-p15y): nothing is public before its
// attestation exists. The release job uploads onto a draft, attests, then
// publishes; the cask job moves the tap afterwards. v6.1.0 ran the other way
// — release published 05:49:38, cask pushed 05:49:39, attestation created
// 05:49:47 (run 40457384503) — and the install action verifies provenance
// fail-closed, so an attest step that failed would have left a public
// release no fleet repository could install. Every decision below is one
// line of YAML or shell CI stays green over, so each is held here and
// re-broken by the mutation ledger.

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

// The release job's steps in the order the contract needs them, the guard
// that keeps a published tag from being rebuilt over, and the cask job's
// pieces.
const (
	goreleaserStep      = "GoReleaser"
	attestStep          = "attest build provenance"
	publishStep         = "publish the release"
	caskHandoffStep     = "hand the cask file to the cask job"
	publishedTagGuard   = "the tag has no published release yet"
	caskPushStep        = "push the cask to the tap"
	caskArtifact        = "homebrew-cask"
	caskFile            = "dist/homebrew/Casks/glyph.rb"
	attestSubjects      = "subject-path: 'dist/*.tar.gz, dist/checksums.txt'"
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
// after the attest step, and hands the cask on only after that. The publish
// step is executed over a stub gh: the un-draft is one gh call naming the tag,
// and a gh that fails fails the step (a release left a draft is re-runnable;
// one reported published that is not would strand the cask job). Reverting
// `draft: true` is one word CI stays green over (mutation row
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
	order := []string{goreleaserStep, attestStep, publishStep, caskHandoffStep}
	for i := 1; i < len(order); i++ {
		if stepAt(t, raw, order[i-1]) >= stepAt(t, raw, order[i]) {
			t.Errorf("goreleaser.yml runs %q before %q; the order is upload → attest → publish → cask, "+
				"so nothing is public before its attestation exists", order[i], order[i-1])
		}
	}
	for _, name := range order {
		if p := readStepShape(t, raw, name).parents; len(p) < 2 || p[0] != "steps" || p[1] != "release" {
			t.Errorf("%q sits under %v, want the release job's steps — the order above is only an order inside one job", name, p)
		}
	}
	attest := readStepShape(t, raw, attestStep)
	if got := attest.keys["uses"]; !strings.HasPrefix(got, "actions/attest-build-provenance@") {
		t.Errorf("the attest step uses %q, want actions/attest-build-provenance", got)
	}
	if !strings.Contains(code(raw), attestSubjects) {
		t.Errorf("the attest step no longer names %q — the install action verifies the tarball it downloads, "+
			"and an attestation over any other set leaves it unverifiable", attestSubjects)
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

// TestPublishedTagIsNotRebuiltOver runs the gh step that opens the release
// job: a tag whose release is already public is refused before anything is
// built, a draft is announced and reused, and no release at all passes. The
// refusal exists because of `replace_existing_artifacts` — there for the
// draft a failed run leaves — which over a PUBLISHED release would swap the
// public assets for a rebuild's differently stamped bytes, which the shipped
// attestation and the tap's cask do not name. The message names the one
// re-run that is still meaningful (the failed cask job). The step sits
// between the tag check and GoReleaser.
func TestPublishedTagIsNotRebuiltOver(t *testing.T) {
	raw := goreleaserWorkflow(t)
	at := stepAt(t, raw, publishedTagGuard)
	if at <= stepAt(t, raw, moduleMajorStep) || at >= stepAt(t, raw, goreleaserStep) {
		t.Errorf("the published-tag guard does not sit between the tag check and GoReleaser")
	}
	script := extractRun(t, raw, publishedTagGuard)
	cases := []struct {
		name     string
		ghStdout string
		ghExit   int
		exit     int
		line     string // a substring of one stdout line
	}{
		{"no release yet passes", "", 1, 0, "no release for v9.8.7 yet"},
		{"a draft is reused", "true", 0, 0, "a draft release for v9.8.7 exists"},
		{"a published release refuses the rebuild", "false", 0, 1, "::error::v9.8.7 already has a published release"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin, calls := ghStub(t, c.ghStdout, c.ghExit)
			run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GH_TOKEN=stub", "TAG=v9.8.7",
			})
			if run.exit != c.exit {
				t.Fatalf("step exited %d, want %d\n%s", run.exit, c.exit, run)
			}
			if !strings.Contains(run.stdout, c.line) {
				t.Errorf("stdout carries no %q\n%s", c.line, run)
			}
			want := [][]string{{"release", "view", "v9.8.7", "--json", "isDraft", "--jq", ".isDraft"}}
			if got := recordedCalls(t, calls); !slices.EqualFunc(got, want, slices.Equal) {
				t.Errorf("the step asked gh %q, want %q", got, want)
			}
			if c.exit != 0 && !strings.Contains(run.stdout, "re-run the failed cask job") {
				t.Errorf("the refusal does not name the re-run that is still meaningful\n%s", run)
			}
		})
	}
}

// TestGoreleaserAttestsWhatItUploads pins the attest step and the grants it
// runs under. Deleting the step, or the `id-token: write` the keyless signing
// needs, reds nothing here while the install action keeps verifying
// fail-closed — so the next tag is a release the whole fleet cannot install
// (t-j0es: both measured surviving the suite at 2f9428a). The grants are read
// where the release job gets them: the workflow level, unless the job
// declares its own block, which then has to carry them.
func TestGoreleaserAttestsWhatItUploads(t *testing.T) {
	body := code(goreleaserWorkflow(t))
	attest := readStepShape(t, goreleaserWorkflow(t), attestStep)
	if got := attest.keys["uses"]; !strings.HasPrefix(got, "actions/attest-build-provenance@") {
		t.Fatalf("goreleaser.yml's attest step uses %q, want actions/attest-build-provenance — without it every "+
			"later install fails `gh attestation verify`", got)
	}
	// The step runs on every run that reaches it: no `if:` deciding whether,
	// no `continue-on-error:` making a failed signing a green job.
	for _, key := range []string{"if", "continue-on-error"} {
		if v, ok := attest.keys[key]; ok {
			t.Errorf("the attest step carries `%s: %s`; whether the release is attested is not conditional", key, v)
		}
	}
	if !strings.Contains(body, attestSubjects) {
		t.Errorf("the attest step no longer names %q", attestSubjects)
	}
	grants := body
	if top, _, ok := strings.Cut(body, "\njobs:\n"); ok {
		grants = top
	}
	release := jobBlock(t, body, "release")
	if regexp.MustCompile(`(?m)^    permissions:`).MatchString(release) {
		grants = release
	}
	for _, scope := range []string{"id-token", "attestations"} {
		re := regexp.MustCompile(`(?m)^\s*` + scope + `:\s*write\b`)
		if !re.MatchString(grants) {
			t.Errorf("the release job does not run under `%s: write`; the attest step then fails with every "+
				"asset already uploaded, or never signs — and the install action verifies fail-closed", scope)
		}
	}
}

// TestCaskGoesOutOnlyAfterThePublish: the tap names the release's assets, so
// the push is its own job, after the release job (`needs:`), over the cask
// file that job wrote (an artifact — a rebuild would stamp a new build date
// and a regenerated cask would carry the wrong sha256). Dropping `needs:`
// runs the two jobs side by side and reopens the brew-404 window a draft once
// held open for 17 days (cifail v0.1.1). The push step is executed over a
// real git and a local bare tap: it commits the cask under the hub's
// update-tap identity and subject, pushes once, is a no-op over a tap that
// already carries the cask (a re-run whose push landed), and refuses an empty
// deploy key before cloning anything.
func TestCaskGoesOutOnlyAfterThePublish(t *testing.T) {
	raw := goreleaserWorkflow(t)
	body := code(raw)
	cask := jobBlock(t, body, "cask")
	if !regexp.MustCompile(`(?m)^    needs:\s*release\s*$`).MatchString(cask) {
		t.Error("the cask job does not `needs: release`; it would push the tap while the release job is still " +
			"uploading onto a draft — `brew install` fetching 404s until the publish, or for ever if it fails")
	}
	if !strings.Contains(cask, "uses: actions/download-artifact@") || !strings.Contains(cask, "name: "+caskArtifact) {
		t.Errorf("the cask job does not download the %q artifact", caskArtifact)
	}
	handoff := stepBlockOf(t, raw, caskHandoffStep)
	for _, want := range []string{"uses: actions/upload-artifact@", "name: " + caskArtifact, "path: " + caskFile, "if-no-files-found: error"} {
		if !strings.Contains(handoff, want) {
			t.Errorf("the handoff step lost %q", want)
		}
	}
	push := stepBlockOf(t, raw, caskPushStep)
	if !strings.Contains(push, "CASK: ${{ runner.temp }}/cask/glyph.rb") || !strings.Contains(cask, "path: ${{ runner.temp }}/cask") {
		t.Error("the push step's CASK does not point into the directory the download step fills")
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
	newTap := func(t *testing.T) (bare string) {
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
		git(t, seed, "add", "Casks")
		git(t, seed, "commit", "--quiet", "-m", ":package:(tap)= bump other to v1.0.0")
		git(t, seed, "push", "--quiet", bare, "HEAD:main")
		return bare
	}
	const rendered = "cask \"glyph\" do\n  version \"9.8.7\"\nend\n"
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

	t.Run("the cask lands in the tap as one lint-clean commit", func(t *testing.T) {
		bare := newTap(t)
		r, temp := run(t, bare, "-----BEGIN OPENSSH PRIVATE KEY-----\nstub\n-----END OPENSSH PRIVATE KEY-----")
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

	t.Run("an empty deploy key is refused before anything is cloned", func(t *testing.T) {
		bare := newTap(t)
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
