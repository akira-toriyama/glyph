package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// namingFlagBase names the minimal otherwise-valid invocation of each command
// that takes a string flag, so the empty form under test is the ONLY thing
// wrong with the run. A command whose tested flag is itself the input-source
// selector is run with that flag alone (see the sources set below).
//
// The commands are listed by hand; the FLAGS are not. That split is the point:
// a new string flag is covered the moment it is registered, and a new command
// fails the completeness check below until someone says how to invoke it.
var namingFlagBase = map[string][]string{
	"lint":    {"--pr", "1"},
	"fmt":     nil,
	"bump":    {"--pr", "1"},
	"notes":   {"--pr", "1"},
	"release": {"--dry-run"},
	"preview": {"--pr", "1"},
	"doctor":  nil,
}

// sources are the flags that SELECT the input rather than qualify it. Each is
// tested alone: combining it with the base args would trip cobra's own
// mutual-exclusion check and the run would exit 2 for the wrong reason.
var sources = []string{"range", "message", "since-tag"}

// TestEveryStringFlagRejectsItsEmptyForm is the mechanism, not the fix.
//
// The defect it generalises is one flag deep: `--current=` — what a workflow
// emits when it templates an unset variable — was checked for emptiness rather
// than for having been GIVEN, so glyph fell back to the default the flag was
// passed to override and printed a version nobody asked for, at exit 0, with
// nothing on stderr. Every other naming flag had the same shape, and two of
// them (`--target`, `--footer-file`) decide what a published release points at
// and contains.
//
// So the assertion is not "these seven flags are guarded" but "every string
// flag glyph has rejects its empty form", enumerated from the command tree at
// run time. A guard someone forgets to add to a new flag fails here rather than
// waiting for a workflow to notice it silently.
func TestEveryStringFlagRejectsItsEmptyForm(t *testing.T) {
	// A stand-in API that fails the test on any request: these guards must run
	// before glyph touches the network, and this is also what keeps the table
	// hermetic when it is run against a tree whose guards are missing.
	usePR(t, walkServer(t, nil))

	root := newRootCmd()
	covered := 0
	for _, cmd := range root.Commands() {
		base, known := namingFlagBase[cmd.Name()]
		var stringFlags []*pflag.Flag
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			// Only string flags NAME something. A bool cannot be given empty,
			// and --since-tag's NoOptDefVal means a bare `--since-tag` carries
			// "auto" rather than "" — the empty form is still an explicit
			// `--since-tag=`, which is what this table passes.
			if f.Value.Type() == "string" {
				stringFlags = append(stringFlags, f)
			}
		})
		if len(stringFlags) == 0 {
			continue
		}
		if !known {
			t.Errorf("%s takes string flag(s) but has no row in namingFlagBase; add the minimal "+
				"valid invocation so its empty forms are covered", cmd.Name())
			continue
		}

		for _, f := range stringFlags {
			t.Run(cmd.Name()+" --"+f.Name+"=", func(t *testing.T) {
				args := []string{cmd.Name()}
				if !slices.Contains(sources, f.Name) {
					args = append(args, base...)
				}
				args = append(args, "--"+f.Name+"=")

				code, stdout, stderr := runGlyph(t, args...)
				if code != 2 {
					t.Errorf("exited %d, want 2 (usage — the caller named nothing)\nstderr: %s", code, stderr)
				}
				// The assertion that actually protects a release: a rejected
				// invocation must not emit a verdict, a version or a body.
				if stdout != "" {
					t.Errorf("a rejected invocation wrote a payload:\n%s", stdout)
				}
				if !strings.Contains(stderr, f.Name) {
					t.Errorf("the diagnostic does not name --%s, so a caller cannot tell which flag "+
						"was wrong:\nstderr: %s", f.Name, stderr)
				}
			})
			covered++
		}
	}
	if covered == 0 {
		t.Fatal("no string flag was exercised — the enumeration found nothing and this test pins nothing")
	}
}

// TestLintStdinFalseIsUsageNotAVerdict: `--stdin=false` satisfies cobra's
// one-required group (it asks pflag's Changed), then matches no VALUE arm — so
// the run used to fall through to an empty --message and answer with 3, the
// gate code the fleet's commit-lint job hard-fails on, about a commit nobody
// submitted. It also swallowed a perfectly good message: the table covers both,
// because "no input mode was selected" must not depend on what is on stdin.
func TestLintStdinFalseIsUsageNotAVerdict(t *testing.T) {
	for _, tc := range []struct{ name, stdin string }{
		{"empty stdin", ""},
		{"a clean message on stdin", ":bug:~ fix a crash\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setStdin(t, tc.stdin)
			code, _, stderr := runGlyph(t, "lint", "--stdin=false")
			if code != 2 {
				t.Fatalf("lint --stdin=false exited %d, want 2 (usage — no input mode was selected)\nstderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, "stdin") {
				t.Errorf("the diagnostic does not name --stdin:\nstderr: %s", stderr)
			}
		})
	}
}

// TestLintRepoRefusesLocalModes pins the input-source grammar lint shares with
// bump and notes (markInputSourceFlags's reasoning): --repo feeds the one
// API-backed mode (--pr), and combined with a local mode it would be silently
// ignored — glyph does not ignore input silently.
func TestLintRepoRefusesLocalModes(t *testing.T) {
	for _, args := range [][]string{
		{"lint", "--repo", "o/r", "--range", "HEAD~1..HEAD"},
		{"lint", "--repo", "o/r", "--message", ":bug:~ fix a crash"},
		{"lint", "--repo", "o/r", "--stdin"},
	} {
		t.Run(args[3], func(t *testing.T) {
			code, _, stderr := runGlyph(t, args...)
			if code != 2 {
				t.Fatalf("%v exited %d, want 2 (usage — --repo rides only with --pr)\nstderr: %s", args, code, stderr)
			}
			if !strings.Contains(stderr, "repo") {
				t.Errorf("the diagnostic does not name --repo:\nstderr: %s", stderr)
			}
		})
	}
}

// TestMachineOutputFlagHasOneSpelling enumerates the flag from the command tree
// rather than from a list, so a new command that invents a third spelling fails
// here instead of in a caller's shell.
//
// glyph shipped two names for one thing until v1.0.0: `--json` on six commands
// and `--ndjson` on `version` and `hook install`. `--ndjson` was a misnomer as
// well as a split — printCompact writes ONE object, never a stream — and the
// cost landed on callers, who had to remember which subcommand took which:
// measured before the rename, `version --json` and `bump --ndjson` BOTH exited
// 2 with `unknown flag`.
func TestMachineOutputFlagHasOneSpelling(t *testing.T) {
	var walk func(cmd *cobra.Command, path string)
	var machine []string
	walk = func(cmd *cobra.Command, path string) {
		if f := cmd.Flags().Lookup("ndjson"); f != nil {
			t.Errorf("%s registers --ndjson. The machine-output flag is spelled --json on every "+
				"command that has one; --ndjson also names a format glyph does not emit "+
				"(printCompact writes one object, not a stream)", path)
		}
		if f := cmd.Flags().Lookup("json"); f != nil {
			machine = append(machine, path)
		}
		for _, sub := range cmd.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	root := newRootCmd()
	walk(root, "glyph")

	// NON-VACUITY. A tree walk that reached nothing would report no --ndjson
	// anywhere and pass, which is the failure mode this whole file is written
	// against. The floor is the six commands that always had --json plus the
	// two renamed into it.
	const floor = 7
	if len(machine) < floor {
		t.Fatalf("the walk found --json on only %d commands (%v); at least %d have it, so the "+
			"walk is not reaching the command tree and the --ndjson assertion above proved "+
			"nothing", len(machine), machine, floor)
	}

	// The two renamed commands are named explicitly: they are the ones the
	// rename was FOR, and a regression that put --ndjson back on either would
	// otherwise only show up in the negative assertion, which reads as an
	// absence rather than as these two commands.
	for _, want := range []string{"glyph version", "glyph hook install"} {
		if !slices.Contains(machine, want) {
			t.Errorf("%s does not register --json; it was renamed from --ndjson in v1.0.0 and "+
				"must keep the one spelling", want)
		}
	}
}

// TestSinceTagNeedsARepo: the walk resolves PRs over the API, so the repository
// must be known up front — no repo is the caller's input, usage (2), before any
// request or git walk runs.
func TestSinceTagNeedsARepo(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 2 {
		t.Fatalf("bump --since-tag with no repository exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "repo") {
		t.Fatalf("the error should name the missing repository input:\n%s", stderr)
	}
}

// TestSinceTagIsExclusiveWithTheOtherSources: --range, --pr and --since-tag are
// three sources for the same answer; combining any two would silently pick one.
func TestSinceTagIsExclusiveWithTheOtherSources(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)
	for _, extra := range [][]string{
		{"--range", base + "..HEAD"},
		{"--pr", "7"},
	} {
		args := append([]string{"bump", "--since-tag"}, extra...)
		if code, _, _ := runGlyph(t, args...); code != 2 {
			t.Fatalf("bump --since-tag with %v should exit 2 (usage)", extra)
		}
	}
}

// TestSinceTagExplicitEmptyIsUsage: `--since-tag=` (a workflow templating an
// unset variable) must not silently degrade to auto — the caller NAMED a tag
// and the name is empty. Usage, before anything runs.
func TestSinceTagExplicitEmptyIsUsage(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph") // so the repo guard cannot mask the tag guard
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag=")
	if code != 2 {
		t.Fatalf("bump --since-tag= exited %d, want 2 (usage — an empty tag is not auto)", code)
	}
	if !strings.Contains(stderr, "since-tag") {
		t.Fatalf("the error should name the flag:\n%s", stderr)
	}
}

// TestSinceTagOptionShapedTagIsUsage: symmetric with checkRangeFlag — an
// option-shaped or range-shaped tag is the caller's input, rejected as usage
// (2) before git runs, not surfaced as a retryable-looking API failure (4).
func TestSinceTagOptionShapedTagIsUsage(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph") // so the repo guard cannot mask the tag guard
	dir, _ := testRepo(t)
	t.Chdir(dir)
	for _, tag := range []string{"-v0.1.0", "v0.1.0..HEAD"} {
		code, _, stderr := runGlyph(t, "bump", "--since-tag="+tag)
		if code != 2 {
			t.Fatalf("bump --since-tag=%q exited %d, want 2 (usage)", tag, code)
		}
		if !strings.Contains(stderr, "since-tag") {
			t.Fatalf("the error for %q should name --since-tag:\n%s", tag, stderr)
		}
	}
}

// TestSinceTagSpaceFormIsGuided: --since-tag takes its optional value only in
// the = form (pflag's NoOptDefVal grammar), so `--since-tag v0.1.0` parses as a
// bare --since-tag plus a stray positional. That must not walk the WRONG range
// silently — it is a usage error that spells out the = form.
func TestSinceTagSpaceFormIsGuided(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag", "v0.1.0")
	if code != 2 {
		t.Fatalf("bump --since-tag v0.1.0 (space form) exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "--since-tag=v0.1.0") {
		t.Fatalf("the error should spell out the = form:\n%s", stderr)
	}
}

// TestRepoWithRangeIsUsage: --repo configures the API-backed sources (--pr,
// --since-tag); combined with the purely local --range it used to be silently
// ignored. No silent ignores — it is a usage error.
func TestRepoWithRangeIsUsage(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)

	code, _, _ := runGlyph(t, "bump", "--range", base+"..HEAD", "--repo", "akira-toriyama/glyph")
	if code != 2 {
		t.Fatalf("bump --range with --repo exited %d, want 2 (usage — --repo would be silently ignored)", code)
	}
}

// TestSinceTagBelowNeedsAVersion: the bound is compared UNDER, so it must name
// a version — and a workflow templating an unset variable produces the bare
// prefix, which must die as the caller's input (usage, 2) instead of resolving
// to a tag it did not mean or surfacing as a retryable-looking git failure (4).
func TestSinceTagBelowNeedsAVersion(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	for _, bound := range []string{"below:", "below:main", "below:v1.0"} {
		code, _, stderr := runGlyph(t, "bump", "--since-tag="+bound)
		if code != 2 {
			t.Errorf("bump --since-tag=%s exited %d, want 2 (usage)", bound, code)
			continue
		}
		if !strings.Contains(stderr, "version-shaped") {
			t.Errorf("the error for %q should say a version-shaped tag is needed:\n%s", bound, stderr)
		}
	}
}
