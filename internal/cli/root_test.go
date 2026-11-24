package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
	"github.com/spf13/cobra"
)

// TestFinishSilentInterrupt: both adapters classify a canceled context as a
// Silent CodeInterrupted — the user's own Ctrl-C. finish must return 130 and
// write NO stderr envelope: the abort is the user's own doing, and a release
// job grepping stderr for {"error":...} must not see one.
func TestFinishSilentInterrupt(t *testing.T) {
	_, stderr, restore := captureOut()
	t.Cleanup(restore)
	code := finish(core.Interrupted())
	if code != 130 {
		t.Fatalf("finish(interrupted) = %d, want 130", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a Silent error must write no envelope, stderr got %q", stderr.String())
	}
}

// TestFinishBareErrorIsAPI: a non-*core.Error reaching finish is one nobody
// classified, so it is 4 with the envelope carrying its message unchanged —
// "unclassified ⇒ API, never usage" (DESIGN §5). `completion zsh 1</dev/null`
// once reached this arm with its write error and answered 2.
func TestFinishBareErrorIsAPI(t *testing.T) {
	msg := "write /dev/stdout: bad file descriptor"
	_, stderr, restore := captureOut()
	t.Cleanup(restore)
	if code := finish(errors.New(msg)); code != 4 {
		t.Fatalf("finish(%q) = %d, want 4 (no trustworthy answer)", msg, code)
	}
	env := decodeErrorEnvelope(t, stderr.String())
	if env.Code != 4 || env.Message != msg {
		t.Fatalf("envelope = %+v, want code 4 carrying %q unchanged", env, msg)
	}
}

// TestCobraParseErrorsAreUsage: each kind of error cobra raises over the
// command line is 2, with cobra's message as the envelope's — classified where
// cobra raises it (newRootCmd), since finish's bare arm is 4.
func TestCobraParseErrorsAreUsage(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		msg  string
	}{
		"unknown flag":       {[]string{"lint", "--nope"}, "unknown flag: --nope"},
		"missing flag value": {[]string{"bump", "--range"}, "flag needs an argument: --range"},
		"bad flag value":     {[]string{"lint", "--stdin=maybe"}, `invalid argument "maybe" for "--stdin" flag: must be true or false`},
		"required flag":      {[]string{"preview"}, `required flag(s) "pr" not set`},
		"one-required group": {[]string{"bump"}, "at least one of the flags in the group [range pr since-tag] is required"},
		"exclusive group":    {[]string{"bump", "--range", "a..b", "--pr", "1"}, "if any flags in the group [range pr since-tag] are set none of the others can be; [pr range] were all set"},
		"completion shell":   {[]string{"completion", "zsh", "extra"}, `glyph completion zsh takes no arguments (got "extra")`},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runGlyph(t, tc.args...)
			env := decodeErrorEnvelope(t, stderr)
			if code != 2 || env.Code != 2 || env.Message != tc.msg {
				t.Fatalf("%v = exit %d, envelope %+v; want 2 carrying %q", tc.args, code, env, tc.msg)
			}
		})
	}
}

// failingStdout is a stdout whose every write fails, as `1</dev/null` does.
type failingStdout struct{}

func (failingStdout) Write([]byte) (int, error) {
	return 0, errors.New("write /dev/stdout: bad file descriptor")
}

// TestStdoutWriteFailureIsAPI: a payload that could not be written is 4, the
// envelope alone on stderr, whoever wrote it — glyph (bump, version) or cobra
// (help, the version template, a completion script). The root keeps cobra's
// own writer here, unlike runGlyph's, so help takes the path it takes in the
// real process. All five once answered 0 or 2 with nothing delivered.
func TestStdoutWriteFailureIsAPI(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)
	for _, args := range [][]string{
		{"bump", "--range", base + "..HEAD"},
		{"version"},
		{"--help"},
		{"--version"},
		{"completion", "zsh"},
	} {
		_, stderr, restore := captureOut()
		out = failingStdout{}
		root := newRootCmd()
		root.SetArgs(args)
		code := finish(run(context.Background(), root))
		restore()
		if code != 4 {
			t.Fatalf("%v with a failing stdout = exit %d, want 4", args, code)
		}
		if !strings.HasPrefix(stderr.String(), "{") {
			t.Fatalf("%v: stderr = %q, want the envelope alone", args, stderr.String())
		}
		if env := decodeErrorEnvelope(t, stderr.String()); env.Code != 4 || !strings.Contains(env.Message, "bad file descriptor") {
			t.Fatalf("%v: envelope = %+v, want code 4 naming the write error", args, env)
		}
	}
}

// TestUnknownCommandIsUsage: an unknown subcommand of the root exits 2. Root's
// Args: noArgs refuses it as usage; were that wiring dropped, cobra's own
// unknown-command error would reach finish's bare arm, which is 4.
func TestUnknownCommandIsUsage(t *testing.T) {
	if code, _, _ := runGlyph(t, "no-such-command"); code != 2 {
		t.Fatalf("unknown command should exit 2 (usage), got %d", code)
	}
}

// TestStrayPositionalNamesItsRemedy: a positional no command takes is usage
// (2) everywhere, but the remedy depends on where it landed. On a command with
// subcommands — the root, `hook`, cobra's `completion` — it is a mistyped
// subcommand of THAT command, answered with its own command list. On a leaf
// given a flag that takes a value it is almost always that value holding
// spaces, left unquoted; with no flag, or only bool flags, nothing could have
// been left unquoted, so the refusal names no such cause.
func TestStrayPositionalNamesItsRemedy(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"the root":               {[]string{"no-such-command"}, `unknown command "no-such-command" for "glyph" — run 'glyph --help' for the command list`},
		"a group":                {[]string{"hook", "bogus"}, `unknown command "bogus" for "glyph hook" — run 'glyph hook --help' for the command list`},
		"cobra's completion":     {[]string{"completion", "zshh"}, `unknown command "zshh" for "glyph completion" — run 'glyph completion --help' for the command list`},
		"a leaf, a value flag":   {[]string{"lint", "--message", ":bug:~", "fix", "it"}, `glyph lint takes no arguments (got "fix") — quote a flag value that holds spaces`},
		"a since-tag leaf":       {[]string{"bump", "--range", "a..b", "extra"}, `glyph bump takes no arguments (got "extra") — quote a flag value that holds spaces`},
		"a leaf, no flag":        {[]string{"init", "gemoji"}, `glyph init takes no arguments (got "gemoji")`},
		"a leaf, only bool flag": {[]string{"version", "--json", "foo"}, `glyph version takes no arguments (got "foo")`},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != int(core.CodeUsage) || stdout != "" {
				t.Fatalf("glyph %v = %d stdout %q, want exactly %d and nothing on stdout\nstderr: %s", tc.args, code, stdout, core.CodeUsage, stderr)
			}
			if env := decodeErrorEnvelope(t, stderr); env.Code != int(core.CodeUsage) || env.Message != tc.want {
				t.Errorf("glyph %v envelope = %+v, want code %d and message\n%s", tc.args, env, core.CodeUsage, tc.want)
			}
		})
	}
}

// TestHelpRefusesWhatItCannotResolve: cobra's `help` resolved its topic with
// Find, which returns the deepest command and the leftover words without
// error, so `glyph help nosuch` printed the root's help at 0. A leftover word
// is now the topic's own stray positional — the refusal the same words give
// without `help` — while a topic that resolves whole still prints the bytes
// its --help prints, at 0.
func TestHelpRefusesWhatItCannotResolve(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"an unknown topic":          {[]string{"help", "nosuch"}, `unknown command "nosuch" for "glyph" — run 'glyph --help' for the command list`},
		"a word past a leaf":        {[]string{"help", "lint", "extra"}, `glyph lint takes no arguments (got "extra")`},
		"an unknown sub of a group": {[]string{"help", "hook", "nosuch"}, `unknown command "nosuch" for "glyph hook" — run 'glyph hook --help' for the command list`},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != int(core.CodeUsage) || stdout != "" {
				t.Fatalf("glyph %v = %d stdout %q, want exactly %d and nothing on stdout\nstderr: %s", tc.args, code, stdout, core.CodeUsage, stderr)
			}
			if env := decodeErrorEnvelope(t, stderr); env.Code != int(core.CodeUsage) || env.Message != tc.want {
				t.Errorf("glyph %v envelope = %+v, want code %d and message\n%s", tc.args, env, core.CodeUsage, tc.want)
			}
		})
	}
	// runGlyph sends cobra's help to its stderr buffer (root.SetOut(errBuf)).
	for _, topic := range [][]string{{}, {"lint"}, {"hook", "install"}} {
		code, _, got := runGlyph(t, append([]string{"help"}, topic...)...)
		_, _, want := runGlyph(t, append(topic, "--help")...)
		if code != 0 || got != want || want == "" {
			t.Errorf("glyph help %v = %d, want 0 and the bytes --help prints (%d bytes, got %d)", topic, code, len(want), len(got))
		}
	}
}

// TestCompletionRejectsAnUnknownShell pins the exit code on the one
// invalid-argument path in this CLI that was not usage(2).
//
// cobra owns the `completion` command and sets Args: NoArgs on it, but gives it
// no Run — and Command.execute() returns flag.ErrHelp for an unrunnable command
// BEFORE validating arguments, so that NoArgs never ran. The measured result was
// exit 0 with 563 bytes of the parent's help on STDOUT, which matters because
// this command's output is redirected into a file the shell sources: `glyph
// completion zshh > _glyph` reported success and wrote English prose where a
// completion script belongs, breaking every later shell start with nothing
// having failed. Every other shape already exited 2 (`glyph bogus`, `glyph hook
// bogus`, `glyph completion zsh extra`).
//
// The assertion is the CODE, not merely non-zero: 2 is usage and 3 is the gate
// code a fleet lint job hard-fails on, and nothing downstream can tell them
// apart from truthiness.
func TestCompletionRejectsAnUnknownShell(t *testing.T) {
	code, stdout, _ := runGlyph(t, "completion", "bogus")
	if code != 2 {
		t.Fatalf("`completion bogus` exited %d, want 2 (usage) — an unknown shell must not look like success", code)
	}
	if stdout != "" {
		// The payload stream is what a caller redirects into the file it sources.
		t.Fatalf("`completion bogus` wrote %q to the payload stream, want nothing", stdout)
	}
}

// The positive control for the guard above — that `completion zsh` still emits a
// script — is scripts/smoke.sh (run by build.yml's extras job and by
// scripts/check.sh), against the real binary: the script on stdout for a known
// shell, nothing on stdout and exit 2 for an unknown one. Cobra captures the
// completion writer ONCE, when the command tree is built (`out :=
// c.OutOrStdout()` in completions.go, closed over by every shell's RunE), so
// runGlyph's later SetOut never reaches it; newRootCmd sets the root's writer to
// stdoutFunnel before that capture, which is why the script lands in out.

// TestCompletionBareIsHelpAtZero: a parent command with no subcommand named
// prints help and exits 0 — the shape bare `glyph` and `glyph hook` already
// have. Pinned so the fix above cannot be mistaken for "any `completion`
// invocation without a shell is an error".
func TestCompletionBareIsHelpAtZero(t *testing.T) {
	code, _, _ := runGlyph(t, "completion")
	if code != 0 {
		t.Fatalf("bare `completion` exited %d, want 0 (help)", code)
	}
}

// TestACancelledRunIsInterruptedUnlessItSucceeded pins run's mapping of a
// cancelled context: a read that takes no context (doctor's workflow files,
// lint --stdin) lets a Ctrl-C landing in it run on to the command's own code,
// measured 3 for lint --stdin, and that code is no answer to a run the operator
// stopped — so every non-success is 130 with nothing rendered. A run that
// finished with 0 delivered its verdict and keeps it.
func TestACancelledRunIsInterruptedUnlessItSucceeded(t *testing.T) {
	for _, c := range []struct {
		name     string
		returned error
		want     int
	}{
		{"a gate verdict", core.Lintf("1 violation").Quiet(true), 130},
		{"a usage refusal", core.Usagef("--repo owner/name is required"), 130},
		{"a success", nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			root := &cobra.Command{Use: "glyph", RunE: func(*cobra.Command, []string) error {
				cancel()
				return c.returned
			}}
			root.SetArgs(nil)
			_, stderr, restore := captureOut()
			code := finish(run(ctx, root))
			restore()
			if code != c.want || stderr.Len() != 0 {
				t.Fatalf("cancelled while returning %v: exit %d stderr %q, want %d with nothing rendered", c.returned, code, stderr.String(), c.want)
			}
		})
	}
}

// TestFinishNil: a nil error is the silent success — exit 0, nothing written.
func TestFinishNil(t *testing.T) {
	_, stderr, restore := captureOut()
	t.Cleanup(restore)
	if code := finish(nil); code != 0 {
		t.Fatalf("finish(nil) = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("finish(nil) wrote %q to stderr, want nothing", stderr.String())
	}
}
