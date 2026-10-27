package workflows

// Shipping invariants (t-ewre): four :bug: fixes to the distribution layer
// each shipped as a one-file YAML/shell diff with zero tests, and each can be
// reverted by a one-line change that no compiler, no Go test and no smoke can
// see. The dist-gate makes evidence mandatory for FUTURE distribution diffs;
// these tests are the evidence those four never carried, written after the
// fact. Each has a mutation-ledger row re-breaking it, which is where "a fix
// is shown by re-breaking it" is enforced for lines no Go code compiles.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestReleasePublishesAtTagTime pins `draft: false` in .goreleaser.yaml
// (1015774). The same run's homebrew_casks step has ALREADY pushed the new
// version to the tap, so every draft is a window where `brew install` fetches
// a 404 asset, bounded only by how soon a human remembers to publish — cifail
// v0.1.1 sat unpublished for 17 days. Reverting is a one-word change that CI
// stays green over; the failure surfaces after the next tag, in a consumer.
func TestReleasePublishesAtTagTime(t *testing.T) {
	config := code(repoFile(t, ".goreleaser.yaml"))
	if !regexp.MustCompile(`(?m)^\s*draft:\s*false\s*$`).MatchString(config) {
		t.Error(".goreleaser.yaml no longer sets `draft: false`; a draft's assets are 404 to " +
			"anonymous clients while the tap already names the new version — every draft is a " +
			"broken-brew window bounded only by a human remembering to publish (1015774)")
	}
	truthyDraft := regexp.MustCompile(`(?m)^\s*draft:\s*["']?true`)
	// Positive control: the line 1015774 replaced (6cad6ae).
	const canary = "  draft: true"
	if !truthyDraft.MatchString(canary) {
		t.Fatalf("the pattern no longer matches the draft line it bans (%q) — the absence asserted below is vacuous", canary)
	}
	if truthyDraft.MatchString(config) {
		t.Error(".goreleaser.yaml sets a truthy `draft:`; publish at tag time — see the release block's rationale")
	}
}

// TestPrereleaseTagsStayOffLatestAndOffTheTap pins the pair of `auto`s that
// make a release candidate cheap. v2's grammar change cannot be proven by a
// dry run — only by a real release binary that the reusables install and a
// fleet gate executes (t-x3ed) — so v3 ships rc tags first. GoReleaser
// defaults both keys the other way: without `prerelease: auto` every `v*` tag
// publishes as a full release and takes GitHub's `latest` pointer, and without
// `skip_upload: auto` the same tag regenerates Casks/glyph.rb, so an rc of a
// breaking version reaches anyone who types `brew upgrade`. Both are absences,
// invisible to CI, and neither shows up until the tag is already pushed and
// `latest` has already moved for whoever looked in between.
func TestPrereleaseTagsStayOffLatestAndOffTheTap(t *testing.T) {
	config := code(repoFile(t, ".goreleaser.yaml"))

	for _, g := range []struct{ key, why string }{
		{"prerelease", "an rc tag publishes as a full release and takes GitHub's `latest` pointer, " +
			"which is what every \"what should I pin?\" glance reads"},
		{"skip_upload", "an rc tag regenerates the Homebrew cask, so `brew install " +
			"akira-toriyama/tap/glyph` hands out a candidate of a breaking version"},
	} {
		if !regexp.MustCompile(`(?m)^\s*` + g.key + `:\s*auto\s*$`).MatchString(config) {
			t.Errorf(".goreleaser.yaml no longer sets `%s: auto`; GoReleaser's default is the "+
				"other way, so %s", g.key, g.why)
		}
	}

	// Positive control: the guard above asserts a presence, but the regex it
	// asserts with is the part that can rot. Prove the same pattern still finds
	// a key this file demonstrably carries — `draft: false` — spelled the way
	// the guards spell it, so a regex that has stopped matching anything at all
	// fails here instead of passing two absences off as satisfied.
	if !regexp.MustCompile(`(?m)^\s*draft:\s*false\s*$`).MatchString(config) {
		t.Error("the key regex matches nothing in .goreleaser.yaml, not even `draft: false`; " +
			"the two `auto` guards above are asserting against a pattern that can no longer " +
			"match this file's syntax, so they would pass on a stripped config")
	}
}

// TestInstallActionRetriesSpanARealOutage pins the retry widths in the install
// composite action (bcd93e3): `--retry 3` (~7s) measurably lost to a real
// GitHub outage — a verdict job died on 504x4 downloading the binary (t-yj1b)
// — so the curls carry `--retry 5 --retry-connrefused` and the attestation
// gate retries 5 times on a growing sleep. This action is the entry point of
// every fleet repo's lint/pr-verdict/release, so a shrunk width is invisible
// until an outage turns the whole fleet red at once.
func TestInstallActionRetriesSpanARealOutage(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "actions", "install", "action.yml")))

	// Both downloads — the tarball and its checksums — carry the widened curl.
	if got := strings.Count(body, "--retry 5 --retry-connrefused"); got < 2 {
		t.Errorf("install action carries %d curl(s) with `--retry 5 --retry-connrefused`, want 2 "+
			"(binary + checksums); --retry 3's ~7s window measurably lost to a real outage (t-yj1b)", got)
	}

	// The attestation gate keeps its five bounded attempts. That the fifth
	// failure still fails the step is TestInstallActionInstallsOnlyWhatItVerified's,
	// which runs it.
	for _, want := range []string{
		"for i in 1 2 3 4 5",
		`sleep "$((i * 3))"`,
		`[ "$i" -eq 5 ]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install action's attestation retry lost %q; the Sigstore/attestations API rides "+
				"the same infrastructure that just proved able to stay down past a short retry", want)
		}
	}
}

// TestNotesSoftExitShipsAPlaceholder pins the release-notes action's arm that
// absorbs `glyph notes`' soft no-release exit 1 (7d8d733): under `set -e` a
// bare invocation kills the whole job when the range below the tag holds
// nothing release-worthy — no binaries, no cask, no attestation, behind a tag
// that already exists. The contract is honoured instead: 1 ships a
// placeholder body, and a real failure keeps its code. Which exits ship and
// which fail is TestNotesStepShipsEveryVerdictOnTheRange's, which runs the
// step.
func TestNotesSoftExitShipsAPlaceholder(t *testing.T) {
	body := code(repoFile(t, filepath.FromSlash(releaseNotesAction)))

	for _, want := range []string{
		"|| status=$?",                 // capture without dying under set -e
		`if [ "$status" -eq 1 ]`,       // the soft no-release arm...
		"No release-worthy changes in", // ...ships a body that says so
		`elif [ "$status" -ne 0 ]`,     // real failures keep failing
		`exit "$status"`,               // with their own code, not a laundered 1
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the release-notes action lost %q; `glyph notes` exits 1 on an infra-only "+
				"range by contract, and without this arm `set -e` turns that verdict into a job "+
				"abort behind a tag that already exists (7d8d733)", want)
		}
	}
}

// notesStep is goreleaser.yml's step that renders the release body by running
// the release-notes action over the binary buildStep built; renderStep is the
// action's one step, which the harness below runs.
const (
	buildStep          = "build glyph for its own release notes"
	notesStep          = "release notes via glyph itself"
	releaseNotesAction = ".github/actions/release-notes/action.yml"
	renderStep         = "Render release notes (glyph notes below the tag, placeholder on 1 and 3)"
)

// TestGoreleaserRendersItsNotesThroughTheAction holds goreleaser.yml to the
// one home of the tag-time notes rule: its notes step runs the action over the
// binary the step before built, hands it the pushed tag and the file
// GoReleaser reads, and keeps no walk of its own. A second copy of the rule is
// how six consumers came to fail the job on 3 while glyph's own copy shipped
// (t-4fpn).
func TestGoreleaserRendersItsNotesThroughTheAction(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml"))
	if got := readStepShape(t, raw, notesStep).keys["uses"]; got != "$/.github/actions/release-notes" {
		t.Fatalf("goreleaser.yml's %q step uses %q, want the release-notes action next door", notesStep, got)
	}
	var block string
	for _, b := range stepBlocks(code(raw)) {
		if strings.Contains(b, "name: "+notesStep+"\n") {
			block = b
		}
	}
	for _, want := range []string{
		"glyph: ${{ runner.temp }}/glyph", // the binary buildStep built, never an installed release
		"tag: ${{ github.ref_name }}",
		"output: ${{ runner.temp }}/release-notes.md",
		"token: ${{ secrets.GITHUB_TOKEN }}",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("goreleaser.yml's notes step no longer passes %q to the action", want)
		}
	}
	body := code(raw)
	if strings.Contains(body, "notes --since-tag") {
		t.Error("goreleaser.yml walks the range itself; the rule lives in the release-notes action " +
			"alone, and a second copy is the one that drifts")
	}
	if !strings.Contains(body, "--release-notes=${{ runner.temp }}/release-notes.md") {
		t.Error("GoReleaser no longer reads the file the action writes")
	}
}

// notesFixture lays out one run of the action's step: a stub glyph in bin/
// that records its argv, answers glyphExit and, on 0, prints rendered; an
// empty runner.temp; and the files the step writes to.
type notesFixture struct {
	dir, bin, temp, stub, calls, outputs, envelope string
}

func newNotesFixture(t *testing.T, glyphExit int, rendered string) notesFixture {
	t.Helper()
	dir := t.TempDir()
	f := notesFixture{
		dir:      dir,
		bin:      filepath.Join(dir, "bin"),
		temp:     filepath.Join(dir, "runner-temp"),
		calls:    filepath.Join(dir, "glyph-calls"),
		outputs:  filepath.Join(dir, "github-output"),
		envelope: fmt.Sprintf(`{"error":{"code":%d,"message":"stub refusal"}}`, glyphExit),
	}
	for _, d := range []string{f.bin, f.temp} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.calls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	say := "printf '%s\\n' " + shq(f.envelope) + " >&2\n"
	if glyphExit == 0 {
		say = "printf '%s' " + shq(rendered) + "\n"
	}
	writeStub(t, f.bin, "glyph", recordArgv(f.calls)+say+fmt.Sprintf("exit %d\n", glyphExit))
	f.stub = filepath.Join(f.bin, "glyph")
	return f
}

// env is the step's environment: the runner's own variables, and the four the
// action's env: block maps from its inputs (TestReleaseNotesActionWiresItsInputsAndOutputs
// holds that mapping).
func (f notesFixture) env(tag, refName, output, glyph string) []string {
	return []string{
		"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RUNNER_TEMP=" + f.temp,
		"GITHUB_OUTPUT=" + f.outputs,
		"GITHUB_REF_NAME=" + refName,
		"TAG=" + tag,
		"OUTPUT=" + output,
		"GLYPH=" + glyph,
		"GH_TOKEN=stub-token",
	}
}

// notesScript is the action's step, with the shell the runner starts it under.
func notesScript(t *testing.T) (script string, shell []string) {
	t.Helper()
	raw := repoFile(t, filepath.FromSlash(releaseNotesAction))
	script = extractRun(t, raw, renderStep)
	shell = unspecifiedShell
	if readStepShape(t, raw, renderStep).keys["shell"] == "bash" {
		shell = bashShell
	}
	return script, shell
}

// TestNotesStepShipsEveryVerdictOnTheRange runs the release-notes action's
// step — its own shell, a stub glyph standing where the caller's build or the
// install action put the real one — over each exit `glyph notes` can hand it.
// At tag time the range is immutable, so a verdict on it is permanent: a
// re-run answers the same, and a failed job leaves a tag with no release
// assets. The step therefore ships on 1 (its placeholder byte for byte,
// unchanged by t-bcdz) and on 3, which is how the wedge escape's own tag
// answers: the escape is a tag cut where glyph's refusal sends it (DESIGN §4)
// — at or past the refused commit, or a squash-merged pull's merge point —
// and below: walks from the predecessor, so that walk reaches the commit
// again (t-bcdz, measured 2026-10-20 at ec53ed3 on a throwaway repository:
// the step exited 3 over an empty body). 4 clears on a re-run and 2 is a
// broken invocation, so both keep failing with their own code. The rendered
// case is the positive control: the body is the stub's stdout, so the stub
// ran and the placeholders replace the file glyph wrote (mutation row
// notes-wedge-exit-kills-the-escape-tag-release).
func TestNotesStepShipsEveryVerdictOnTheRange(t *testing.T) {
	script, shell := notesScript(t)
	const (
		tag      = "v9.8.7"
		rendered = "## Fixes\n\n- the body glyph rendered\n"
	)
	cases := []struct {
		name       string
		glyphExit  int
		stepExit   int
		body       string // release-notes.md, when the step ships
		annotation string // one whole line of the step's stdout
	}{
		{"a rendered body ships as rendered", 0, 0, rendered, ""},
		{"nothing release-worthy ships the placeholder that says so", 1, 0,
			"No release-worthy changes in " + tag + ".\n",
			"::warning::no release-worthy commits below " + tag + " — publishing with a placeholder body"},
		{"the wedge escape's own range ships the placeholder that says why", 3, 0,
			"No release notes for " + tag + ": the walk below it reaches a commit glyph refuses, and this tag was cut " +
				"where glyph's refusal sends it (at or past that commit, or a squash-merged pull's merge point) to clear " +
				"the wedge. The release job's log carries glyph's error, which names the commit and that point.\n",
			"::warning::the walk below " + tag + " reaches a commit glyph refuses, and the tag was cut where the refusal " +
				"sends it (at or past that commit, or a squash-merged pull's merge point) to clear the wedge — publishing " +
				"with a placeholder body; glyph's error above names the commit and that point"},
		{"a transient failure fails the job for the re-run that clears it", 4, 4, "", "::error::glyph notes failed (exit 4)"},
		{"a broken invocation fails the job", 2, 2, "", "::error::glyph notes failed (exit 2)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newNotesFixture(t, c.glyphExit, rendered)
			run := runStep(t, script, shell, f.dir, f.env("", tag, "", f.stub))

			if run.exit != c.stepExit {
				t.Fatalf("glyph notes exited %d and the step %d, want %d\n%s", c.glyphExit, run.exit, c.stepExit, run)
			}
			want := []string{"notes", "--since-tag=below:" + tag}
			if got := recordedCalls(t, f.calls); len(got) != 1 || !slices.Equal(got[0], want) {
				t.Errorf("the step ran glyph as %q, want %q once\n%s", got, want, run)
			}
			if c.annotation != "" && !hasLine(run.stdout, c.annotation) {
				t.Errorf("the step printed no line %q\n%s", c.annotation, run)
			}
			if c.glyphExit != 0 && !strings.Contains(run.stderr, f.envelope) {
				t.Errorf("glyph's stderr did not reach the job log — the exit-3 placeholder sends its reader "+
					"there for the commit, and a failed job is diagnosed from it:\n%s", run)
			}
			file := filepath.Join(f.temp, "release-notes.md")
			if c.stepExit != 0 {
				if got := readIfAny(t, f.outputs); got != "" {
					t.Errorf("a failed step still wrote outputs %q; the step after would read a file that holds no verdict", got)
				}
				return
			}
			if got := readIfAny(t, file); got != c.body {
				t.Errorf("release-notes.md holds %q, want %q", got, c.body)
			}
			if want, got := fmt.Sprintf("file=%s\nstatus=%d\n", file, c.glyphExit), readIfAny(t, f.outputs); got != want {
				t.Errorf("GITHUB_OUTPUT holds %q, want %q", got, want)
			}
		})
	}
}

// TestNotesStepResolvesItsInputs runs the step over the ways a caller can
// name what it works on, and over the three calls it refuses before anything
// is written: the tag is the `tag:` input, else the ref the job runs on; the
// body goes where `output:` says, else into runner.temp; glyph is the
// `glyph:` input, a path or a name on PATH. A refusal is exit 2 — a broken
// invocation, in glyph's own vocabulary — and never the soft exit 1 a failed
// redirect would otherwise hand the arm that reads glyph's status (measured
// in review: an `output:` under a directory that does not exist printed the
// nothing-release-worthy warning with glyph never run).
func TestNotesStepResolvesItsInputs(t *testing.T) {
	script, shell := notesScript(t)
	const tag = "v9.8.7"
	cases := []struct {
		name                        string
		tag, refName, output, glyph string // the step's inputs; <temp> stands for runner.temp, "" for unset (glyph: the stub by path)
		exit                        int
		file                        string // where the body lands, under runner.temp
		annotation                  string
	}{
		{name: "the tag: input wins over the ref the job runs on", tag: tag, refName: "a-branch", file: "release-notes.md"},
		{name: "output: names the file", refName: tag, output: "<temp>/CHANGELOG.md", file: "CHANGELOG.md"},
		{name: "glyph: names a binary found on PATH", refName: tag, glyph: "glyph", file: "release-notes.md"},
		{name: "no tag anywhere refuses before glyph runs", exit: 2,
			annotation: "::error::glyph release-notes: no tag — pass tag:, or run on a tag ref"},
		{name: "no glyph refuses before anything is written", refName: tag, glyph: "not-a-glyph", exit: 2,
			annotation: "::error::glyph release-notes: not-a-glyph is not on PATH and not an executable file — " +
				"install glyph first (akira-toriyama/glyph/.github/actions/install), or pass glyph:"},
		{name: "an output nobody can write refuses before glyph runs", refName: tag, output: "<temp>/missing/notes.md", exit: 2,
			annotation: "::error::glyph release-notes: cannot write <temp>/missing/notes.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newNotesFixture(t, 0, "## Fixes\n")
			at := func(s string) string { return strings.ReplaceAll(s, "<temp>", f.temp) }
			glyph := f.stub
			if c.glyph != "" {
				glyph = c.glyph
			}
			run := runStep(t, script, shell, f.dir, f.env(c.tag, c.refName, at(c.output), glyph))

			if run.exit != c.exit {
				t.Fatalf("the step exited %d, want %d\n%s", run.exit, c.exit, run)
			}
			if c.annotation != "" && !hasLine(run.stdout, at(c.annotation)) {
				t.Errorf("the step printed no line %q\n%s", at(c.annotation), run)
			}
			calls := recordedCalls(t, f.calls)
			if c.exit != 0 {
				if len(calls) != 0 {
					t.Errorf("a refused call still ran glyph as %q\n%s", calls, run)
				}
				if got := readIfAny(t, f.outputs); got != "" {
					t.Errorf("a refused call still wrote outputs %q", got)
				}
				if got := readIfAny(t, filepath.Join(f.temp, "release-notes.md")); got != "" {
					t.Errorf("a refused call still wrote a body %q", got)
				}
				return
			}
			want := []string{"notes", "--since-tag=below:" + tag}
			if len(calls) != 1 || !slices.Equal(calls[0], want) {
				t.Errorf("the step ran glyph as %q, want %q once\n%s", calls, want, run)
			}
			file := filepath.Join(f.temp, c.file)
			if got := readIfAny(t, file); got != "## Fixes\n" {
				t.Errorf("%s holds %q, want the rendered body", file, got)
			}
			if want, got := "file="+file+"\nstatus=0\n", readIfAny(t, f.outputs); got != want {
				t.Errorf("GITHUB_OUTPUT holds %q, want %q", got, want)
			}
		})
	}
}

// TestReleaseNotesActionWiresItsInputsAndOutputs holds the manifest half the
// harness cannot run: the step's env: maps each input to the variable the
// script reads, the step carries the id the outputs name, and each output's
// value reads that step. Broken, a consumer's `steps.<id>.outputs.file` is
// empty and GoReleaser is handed no notes file at all (measured in review:
// renaming the id left every executed test green).
func TestReleaseNotesActionWiresItsInputsAndOutputs(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(releaseNotesAction))
	shape := readStepShape(t, raw, renderStep)
	if got := shape.keys["id"]; got != "notes" {
		t.Errorf("the action's step has id %q, want notes — the outputs below read steps.notes", got)
	}
	if got := shape.keys["shell"]; got != "bash" {
		t.Errorf("the action's step runs under shell %q, want bash: the harness runs it as `bash -eo pipefail`", got)
	}
	body := code(raw)
	for _, want := range []string{
		"TAG: ${{ inputs.tag }}",
		"OUTPUT: ${{ inputs.output }}",
		"GLYPH: ${{ inputs.glyph }}",
		"GH_TOKEN: ${{ inputs.token }}",
		"value: ${{ steps.notes.outputs.file }}",
		"value: ${{ steps.notes.outputs.status }}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the action no longer wires %q; the script reads a variable nothing sets, or a caller reads an output nothing fills", want)
		}
	}
	for _, input := range []string{"tag", "output", "glyph", "token"} {
		if !strings.Contains(body, "\n  "+input+":\n") {
			t.Errorf("the action declares no input %q, which its env: block maps", input)
		}
	}
}

// ldflagsX matches one `-X path.Var=` injection as .goreleaser.yaml and
// build.sh write it (build.sh single-quotes the argument).
var ldflagsX = regexp.MustCompile(`-X '?([A-Za-z0-9_./~-]+)\.([A-Za-z_][A-Za-z0-9_]*)=`)

// TestLdflagsNameTheRealVersionSymbols pins the injection target the release,
// source and nix builds write three times over, as literals, in files no test
// compiles. Go's -X silently ignores an unknown symbol: move internal/version
// or rename a var and all three builds go green while every released binary
// reports "dev" — measured with a doctored -X (exit 0, zero warnings, `glyph
// dev`). The smoke in build.yml runs a plain `go build`, so it cannot see it
// either. The path itself is not written here: it is read from go.mod, so the
// major suffix is declared once (t-f2sk).
func TestLdflagsNameTheRealVersionSymbols(t *testing.T) {
	wantPkg := modulePath(t) + "/internal/version"

	// The package the path names must exist where the path says — read through
	// the module layout itself, so a moved package fails here.
	versionGo, err := os.ReadFile(filepath.Join("..", "..", "internal", "version", "version.go"))
	if err != nil {
		t.Fatalf("internal/version/version.go is unreadable (%v); if the package moved, every "+
			"-X injection in .goreleaser.yaml and build.sh is now a silent no-op", err)
	}

	declared := func(file, sym string) {
		if !regexp.MustCompile(`(?m)^\t` + sym + ` `).Match(versionGo) {
			t.Errorf("%s injects %q, which internal/version/version.go no longer declares; "+
				"-X ignores an unknown symbol silently", file, sym)
		}
	}

	// .goreleaser.yaml spells the full path in every injection.
	goreleaser := ldflagsX.FindAllStringSubmatch(repoFile(t, ".goreleaser.yaml"), -1)
	// Non-vacuity: the file injects Version, Commit and Date today. A matcher
	// gone stale would report zero and the loop would prove nothing.
	if len(goreleaser) < 3 {
		t.Fatalf(".goreleaser.yaml carries %d -X injection(s), want at least 3 — either the "+
			"injections were dropped (released binaries report \"dev\") or ldflagsX no longer "+
			"matches how they are written", len(goreleaser))
	}
	for _, m := range goreleaser {
		if m[1] != wantPkg {
			t.Errorf(".goreleaser.yaml injects into %q, want %q; -X ignores an unknown symbol "+
				"silently, so a stale path ships binaries that report \"dev\"", m[1], wantPkg)
		}
		declared(".goreleaser.yaml", m[2])
	}
	// The release stamp is the tag itself. GoReleaser's {{.Version}} drops the
	// v: the v5.0.0 asset answers `glyph version --json` with "5.0.0" (measured
	// 2026-10-11 on the downloaded darwin_arm64 asset; v3.3.0-rc.2's answers
	// "3.3.0-rc.2"), where build.sh, `go install …@v5.0.0` and README all say
	// v5.0.0. This is the one guard of the tag stamp: the install action
	// accepts a v-less stamp too, as the legacy form of every release through
	// v5.0.0, so a revert to {{.Version}} would install without complaint.
	if want := "-X " + wantPkg + ".Version={{.Tag}}"; !strings.Contains(repoFile(t, ".goreleaser.yaml"), want) {
		t.Errorf(".goreleaser.yaml does not inject %q — any other template stamps released binaries with "+
			"something that is not their tag ({{.Version}} drops the v: the v5.0.0 asset reports \"5.0.0\"), and "+
			"the install action, which accepts the v-less form as legacy, would not notice", want)
	}

	// build.sh routes the same path through one PKG= variable, so the literal
	// lives once and the injections name ${PKG}.
	sh := repoFile(t, "build.sh")
	if !strings.Contains(sh, `PKG="`+wantPkg+`"`) {
		t.Errorf("build.sh's PKG no longer names %q; its -X injections are silent no-ops and "+
			"every source build reports \"dev\"", wantPkg)
	}
	shInjections := regexp.MustCompile(`-X '\$\{PKG\}\.([A-Za-z_][A-Za-z0-9_]*)=`).FindAllStringSubmatch(sh, -1)
	if len(shInjections) < 3 {
		t.Fatalf("build.sh carries %d ${PKG} -X injection(s), want at least 3 — either the "+
			"injections were dropped or the matcher no longer fits how they are written", len(shInjections))
	}
	for _, m := range shInjections {
		declared("build.sh", m[1])
	}

	// flake.nix routes the path through one `v =` binding. It is the third
	// build that stamps a binary and the only one no other guard reads, so a
	// rewrite that misses it ships `glyph dev` from every nix build.
	flake := repoFile(t, "flake.nix")
	if !strings.Contains(flake, `v = "`+wantPkg+`"`) {
		t.Errorf("flake.nix's `v` no longer names %q; its -X injections are silent no-ops and "+
			"every nix build reports \"dev\"", wantPkg)
	}
	flakeInjections := regexp.MustCompile(`-X \$\{v\}\.([A-Za-z_][A-Za-z0-9_]*)=`).FindAllStringSubmatch(flake, -1)
	// Two, not three: nix injects Version and Commit but not Date, because a
	// build timestamp would make the derivation non-reproducible.
	if len(flakeInjections) < 2 {
		t.Fatalf("flake.nix carries %d ${v} -X injection(s), want at least 2 — either the "+
			"injections were dropped or the matcher no longer fits how they are written", len(flakeInjections))
	}
	for _, m := range flakeInjections {
		declared("flake.nix", m[1])
	}
}

// modulePath returns go.mod's module path, the one place the major suffix is
// declared. Every other spelling in the tree is checked against this.
func modulePath(t *testing.T) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^module (\S+)$`).FindStringSubmatch(repoFile(t, "go.mod"))
	if m == nil {
		t.Fatalf("go.mod declares no module path")
	}
	return m[1]
}

// TestModulePathCarriesTheMajorAndReadmeSpellsIt holds the two halves of Go's
// major-version rule together: from v2 on, the module path must end in /vN, and
// every documented `go install` must spell that same path.
//
// Missed twice — neither the v1->v2 nor the v2->v3 step touched go.mod, so the
// proxy stayed frozen at v1.0.0 and README's `go install …@latest` handed out a
// two-major-old binary that rejects today's commit convention (t-f2sk). Nothing
// went red: the proxy is not consulted by any build, test or release here, and
// none of fleet-preflight's probes (lint, bump, the release body) consults it.
//
// Know what this does NOT catch: it reads no tags, so a v6 tag cut over a /v5
// go.mod passes here — and did (v6.0.0, after v2.0.0 and v3.0.0 over no
// suffix). Comparing the module's major against the tag being cut needs the
// tag, which only the tag job holds: goreleaser.yml's first step refuses it
// there, before any asset is built
// (TestReleaseRefusesATagWhoseMajorTheModulePathDoesNotCarry). What this holds
// is the copy drift — go.mod is the one declaration and README,
// .goreleaser.yaml, build.sh and flake.nix are checked against it, never
// against each other.
func TestModulePathCarriesTheMajorAndReadmeSpellsIt(t *testing.T) {
	path := modulePath(t)

	major := regexp.MustCompile(`/v([2-9][0-9]*)$`).FindStringSubmatch(path)
	if major == nil {
		t.Fatalf("go.mod's module path is %q, which carries no /vN major suffix. Go treats a "+
			"path without one as v0/v1 forever: the proxy serves v1.0.0 to `go install @latest` "+
			"and 404s the real tags. Cutting a v2+ tag does not change that — go.mod does", path)
	}

	// Non-vacuity: README must actually carry a `go install` line, or the
	// assertion below proves nothing about a file that stopped documenting it.
	readme := repoFile(t, "README.md")
	installs := regexp.MustCompile(`(?m)^go install (\S+)@`).FindAllStringSubmatch(readme, -1)
	if len(installs) == 0 {
		t.Fatalf("README.md documents no `go install` line; if the install route was dropped on " +
			"purpose, drop this half of the guard with it")
	}
	for _, m := range installs {
		if !strings.HasPrefix(m[1], path+"/") {
			t.Errorf("README.md documents `go install %s@…`, which is not under the module path "+
				"%q. That command installs whatever the unsuffixed path last resolved to — for "+
				"glyph, v1.0.0 — not this tree", m[1], path)
		}
	}
}
