package cli

import (
	"slices"
	"strings"
	"testing"

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
	"lint":    nil,
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
		{"a clean message on stdin", ":bug: fix a crash\n"},
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
