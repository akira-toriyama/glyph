package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// moduleMajorStep is goreleaser.yml's first step after checkout: the tag's
// major against go.mod's /vN suffix.
const moduleMajorStep = "the tag's major is the module path's"

// TestReleaseRefusesATagWhoseMajorTheModulePathDoesNotCarry runs that step —
// its own shell over a go.mod and a ref name — for the tags that were cut and
// the ones that must not be. Go resolves a module by the /vN in go.mod, and a
// tag supplies nothing: v6.0.0 was cut over /v5, as v2.0.0 and v3.0.0 were
// over no suffix (#222), and every gate was green each time, because
// TestModulePathCarriesTheMajorAndReadmeSpellsIt reads no tag by design
// (measured 2026-10-26: proxy.golang.org rejects /v6@v6.0.0 and serves v5.1.0
// for /v5/@latest). The tag job is the one place that holds both the tag and
// the tree, so it refuses there; a doctor check was rejected (t-nag8). The
// step's position is asserted too: a refusal after GoReleaser has run is a
// release with assets behind a tag Go cannot resolve — the shipped defect
// with a red job attached (mutation row
// release-tags-a-major-the-module-path-does-not-carry).
func TestReleaseRefusesATagWhoseMajorTheModulePathDoesNotCarry(t *testing.T) {
	raw := repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml"))
	shape := readStepShape(t, raw, moduleMajorStep)
	if p := shape.parents; len(p) < 2 || p[0] != "steps" || p[1] != "release" {
		t.Fatalf("the step sits under %v, want the release job's steps", p)
	}
	// Before the notes step's `go build` and before GoReleaser: the refusal
	// must leave nothing behind the tag.
	at := strings.Index(raw, "- name: "+moduleMajorStep+"\n")
	for _, later := range []string{"- name: " + notesStep + "\n", "- name: GoReleaser\n"} {
		if j := strings.Index(raw, later); j < 0 || j < at {
			t.Errorf("%q does not follow the module-major step (step at %d, it at %d); a refusal "+
				"after it leaves release assets behind a tag Go cannot resolve", strings.TrimSpace(later), at, j)
		}
	}

	script := extractRun(t, raw, moduleMajorStep)
	shell := unspecifiedShell
	if shape.keys["shell"] == "bash" {
		shell = bashShell
	}
	env := func(tag string) []string {
		return []string{"PATH=" + os.Getenv("PATH"), "GITHUB_REF_NAME=" + tag}
	}

	cases := []struct {
		name   string
		tag    string
		module string // go.mod's module path; "" writes a go.mod with no module line
		exit   int
	}{
		{"v6.0.1 over /v6 passes", "v6.0.1", "github.com/akira-toriyama/glyph/v6", 0},
		{"a release candidate names its release's major", "v6.0.1-rc.1", "github.com/akira-toriyama/glyph/v6", 0},
		{"v1.2.3 over no suffix passes", "v1.2.3", "github.com/akira-toriyama/glyph", 0},
		{"v6.0.0 over /v5 is refused, the shipped miss", "v6.0.0", "github.com/akira-toriyama/glyph/v5", 1},
		{"v2.0.0 over no suffix is refused, #222's miss", "v2.0.0", "github.com/akira-toriyama/glyph", 1},
		{"a ref that is not a version tag is refused", "main", "github.com/akira-toriyama/glyph/v6", 1},
		{"a go.mod with no module line is refused", "v6.0.1", "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			// A comment between `module` and `go`, as this repository's go.mod
			// carries one.
			gomod := "// floor\ngo 1.26.0\n"
			if c.module != "" {
				gomod = "module " + c.module + "\n\n// floor\ngo 1.26.0\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o600); err != nil {
				t.Fatal(err)
			}
			run := runStep(t, script, shell, dir, env(c.tag))
			if run.exit != c.exit {
				t.Fatalf("exit %d, want %d\n%s", run.exit, c.exit, run)
			}
			// A refusal is an ::error:: annotation naming the tag, so the job's
			// summary says which coordinate is wrong without opening the step.
			if c.exit != 0 && (!strings.Contains(run.stdout, "::error::") || !strings.Contains(run.stdout, c.tag)) {
				t.Errorf("no ::error:: line names %s\n%s", c.tag, run)
			}
		})
	}

	// Positive control over the real tree: the step reads go.mod as this
	// repository writes it, and a tag of the path's own major passes — so the
	// pass arm above is the real go.mod's, not a fixture's.
	path := modulePath(t)
	major := regexp.MustCompile(`/v([2-9][0-9]*)$`).FindStringSubmatch(path)
	if major == nil {
		t.Fatalf("go.mod's module path %q carries no /vN; TestModulePathCarriesTheMajorAndReadmeSpellsIt holds that", path)
	}
	run := runStep(t, script, shell, filepath.Join("..", ".."), env("v"+major[1]+".0.0"))
	if run.exit != 0 {
		t.Errorf("over this tree's go.mod (%s) a v%s.0.0 tag is refused (exit %d); the step no longer reads "+
			"the module line as the file writes it\n%s", path, major[1], run.exit, run)
	}
}
