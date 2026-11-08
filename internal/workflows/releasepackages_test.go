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

// release.yml's packages mode (t-dc9e; DESIGN §4.1, "The reusables"): a
// repository that declares [[packages]] keeps one rolling draft per line and
// answers through the `packages` output — one JSON array of per-line verdicts
// — while the four scalars are "" by contract. Two decisions live in the YAML
// alone and nothing in Go would notice their loss, so they are guarded here.

// packagesEmit captures the jq that hands the envelope's array through — the
// verdict step's emit_packages helper. Re-derive it if the helper is reshaped;
// the guard below fails loud rather than vacuously when it stops matching.
var packagesEmit = regexp.MustCompile(`emit_packages\(\) \{ echo "packages=\$\(jq -r '([^']*)'`)

// TestReleasePackagesOutputStripsBodyAndURL: each line's url is withheld for
// the reason TestReleaseOutputsNeverExposeTheDraftURL gives for the scalar one
// — with the handle in hand, auto-publishing a line's draft is a two-line
// caller step, and publishing must stay a human act — and the body because
// the draft already holds it and N bodies could meet the 1 MB output cap.
// Positive control: the notice step reads every line's url out of the same
// envelope, so the value is really there to withhold.
func TestReleasePackagesOutputStripsBodyAndURL(t *testing.T) {
	body := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	if !strings.Contains(body, `.packages[] | select(.url // "" != "")`) {
		t.Fatal("release.yml no longer reads each line's url from the verdict envelope; this guard's " +
			"premise (the per-line url exists and is deliberately not an output) needs re-deriving")
	}
	m := packagesEmit.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("release.yml has no emit_packages helper of the expected shape — the packages output is " +
			"gone, or the helper was reshaped; re-derive packagesEmit before trusting this guard")
	}
	if !strings.Contains(m[1], "del(.body, .url)") {
		t.Errorf("the packages output hands the array through without stripping body and url (jq: %s) — "+
			"a line's url arms every caller with auto-publish, and N bodies meet the 1 MB output cap", m[1])
	}
}

// TestReleaseRefusesArtifactInputsOnAPackagesRepository: `app` / `binary`
// name one artifact for one draft, and a packages repository has one draft
// per line and no line the artifact would belong to. The refusal sits AFTER
// the install — glyph is glyph.toml's only reader, so the declaration is read
// through its own envelope over an empty range, never a grep that would fork
// the grammar — and BEFORE the verdict step, so no draft is touched: asked of
// the verdict envelope, as it first was, the drafts had already been
// rewritten with the misconfigured caller's footer when the run went red
// (glyph-monorepo-test-2 run 41805408808, `binary: probe`, 2026-11-04). It
// is gated on an artifact input alone, so a verdict-only caller never runs
// it; the step runs here against a glyph stub answering each shape the real
// binary does (measured 2026-11-08 with the tree's binary: three lines on a
// packages checkout, no `packages` key on a single line, both at exit 1; exit
// 2 and an error envelope on stderr with no glyph.toml).
func TestReleaseRefusesArtifactInputsOnAPackagesRepository(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "release.yml"))
	body := code(raw)
	const (
		refuse  = "name: Refuse an artifact input on a packages repository"
		install = "uses: $/.github/actions/install"
		verdict = "name: Compose the verdict and upsert the rolling DRAFT"
	)
	ri, ii, vi := strings.Index(body, refuse), strings.Index(body, install), strings.Index(body, verdict)
	if ri < 0 || ii < 0 || vi < 0 {
		t.Fatalf("could not find all three steps in release.yml (refuse=%d, install=%d, verdict=%d) — a rename "+
			"moved one and this guard is asserting nothing", ri, ii, vi)
	}
	if ii >= ri || ri >= vi {
		t.Errorf("the packages refusal is out of place (install=%d, refuse=%d, verdict=%d): it must follow the "+
			"install (glyph reads the declaration) and precede the verdict (a refusal after the upsert has "+
			"already rewritten every line's draft)", ii, ri, vi)
	}
	step := body[ri:vi]
	const gate = "if: inputs.app != '' || inputs.binary != ''"
	if !strings.Contains(step, gate) {
		t.Errorf("the packages refusal is not gated as %q — gated on the verdict's outputs it never fires "+
			"before the verdict, gated wider it costs every verdict-only caller a probe", gate)
	}
	if strings.Contains(step, "steps.verdict") {
		t.Errorf("the packages refusal reads steps.verdict, which has not run yet where the refusal stands:\n%s", step)
	}

	script := extractRun(t, raw, "Refuse an artifact input on a packages repository")
	requireTool(t, "jq")
	cases := []struct {
		name     string
		stdout   string
		stderr   string
		exit     int
		wantExit int
		wantOut  string
	}{
		{"three lines declared", `{"current":"","level":"","commits":[],"packages":[{"path":"camp","level":"none"},{"path":"travel","level":"none"},{"path":"travel/onsen","level":"none"}]}`, "", 1, 1, "::error::this repository declares [[packages]] (3 line(s)"},
		{"the single line", `{"current":"v0.0.0","level":"none","commits":[]}`, "", 1, 0, ""},
		{"no glyph.toml", "", `{"error":{"code":2,"message":"no glyph.toml at /work"}}`, 2, 2, "no glyph.toml at /work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin, temp := t.TempDir(), t.TempDir()
			log := filepath.Join(temp, "argv.log")
			writeStub(t, bin, "glyph", recordArgv(log)+
				"printf '%s' "+shq(c.stdout)+"\nprintf '%s' "+shq(c.stderr)+" >&2\nexit "+strconv.Itoa(c.exit)+"\n")
			run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RUNNER_TEMP=" + temp,
				"APP=",
				"BINARY=probe",
			})
			if run.exit != c.wantExit {
				t.Errorf("exited %d, want %d\n%s", run.exit, c.wantExit, run)
			}
			if c.wantOut != "" && !strings.Contains(run.stdout+run.stderr, c.wantOut) {
				t.Errorf("printed no %q:\n%s", c.wantOut, run)
			}
			calls := recordedCalls(t, log)
			if len(calls) != 1 || !slices.Equal(calls[0], []string{"bump", "--range", "HEAD..HEAD", "--json"}) {
				t.Errorf("glyph was called %v, want one `bump --range HEAD..HEAD --json` — the empty range is "+
					"what makes the probe local and free", calls)
			}
		})
	}
}

// TestReleaseScalarTagIsReadWithEmptyNotNull: the release arm's `tag` is
// read from an envelope that, on a packages repository, has no scalar tag —
// and `jq -r .tag` prints the literal `null` for an absent key, which then
// rides into `next=` as four characters a caller's `!= ”` gate reads as a
// tag. Measured on glyph-monorepo-test's first live run (2026-09-10): the
// read-back refused `next=null`. Positive control: the envelope is read
// with jq -r at all (a rewrite to another reader needs this re-derived).
func TestReleaseScalarTagIsReadWithEmptyNotNull(t *testing.T) {
	script := extractRun(t, repoFile(t, filepath.Join(".github", "workflows", "release.yml")),
		"Compose the verdict and upsert the rolling DRAFT")
	if !strings.Contains(script, `jq -r`) {
		t.Fatal("the verdict step no longer reads its envelope with jq -r; this guard's premise " +
			"(an absent key prints as the literal null) needs re-deriving for the new reader")
	}
	if !strings.Contains(script, `tag="$(jq -r '.tag // empty'`) {
		t.Errorf("the release arm does not read the scalar tag as `.tag // empty` — on a packages "+
			"repository the key is absent and a bare read hands every caller next=null:\n%s", script)
	}
	if strings.Contains(script, `jq -r .tag `) {
		t.Errorf("the verdict step still carries a bare `jq -r .tag` read somewhere — an absent key "+
			"prints as null there:\n%s", script)
	}
}
