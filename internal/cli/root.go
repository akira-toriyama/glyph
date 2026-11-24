// Package cli is glyph's command layer: the cobra wiring and flag checks, the
// dispatch to an input source (a message, --range, --pr, --since-tag), the
// composition of the walk's ports (walk.go: the checkout, the API client and
// the diagnostic stream it reads and reports through), the rendering of every
// verdict under DESIGN §5's stream contract, the pre-push verdict (prepush.go),
// doctor's inputs (cmd_doctor.go: every subprocess and request doctor needs,
// DESIGN §7), and the exit funnel — Execute() int maps every error onto
// glyph's exit-code contract (internal/core).
//
// It also carries out the release's write list (cmd_release.go: the upserts
// and deletes draftplan.PlanRun ordered, against GitHub) — the API client
// lives here, so the writes and the failure policy each kind names stay
// here; which writes, in what order and of which kind is draftplan's.
//
// It holds no copy of: a message's meaning (internal/config's pattern match),
// the lattice, the fold and the commit that decides it, Version.next and the
// step on a line (internal/bump), the release walk — the base past a tag, the
// expansion of each merge point, which lines a commit joins and what the walk
// could not read (internal/walk) — grouping and rendering the notes
// (internal/notes), the package a commit's diff moves (internal/attribution),
// which draft survives, the published floor its tag must clear and the hand
// region its body keeps (internal/draftplan), the preview comment's
// arithmetic and prose (internal/preview), or reading git and calling GitHub
// (internal/gitsource, internal/github).
package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/version"
	"github.com/spf13/cobra"
)

// Execute builds the root command, runs it, and returns the exit code finish
// maps its result to (the contract is core.Code).
//
// A root context is derived from SIGINT/SIGTERM so a Ctrl-C cancels in-flight
// work (subprocesses and HTTP requests) rather than orphaning it; once cancelled
// we restore the default signal disposition so a second Ctrl-C hard-kills.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return finish(run(ctx, newRootCmd()))
}

// run executes root with every stdout byte behind one payload latch (output.go)
// and answers CodeAPI when a write failed, in place of the success or the Silent
// verdict the command returned: the payload a caller reads — a version piped
// into a tag step, a verdict.json — is exactly what went missing. An error that
// renders its own envelope, and the user's own interrupt, keep their code.
//
// A run whose context was cancelled and that ended in any code but success is
// the interrupt, 130: a read that takes no context (a plain file, stdin)
// carries no signal, so the code it lets the command reach — a usage refusal,
// a gate verdict — is not an answer to the run the operator stopped. A run
// that finished with 0 delivered its verdict and keeps it
// (TestACancelledRunIsInterruptedUnlessItSucceeded).
func run(ctx context.Context, root *cobra.Command) error {
	p := &payload{w: out}
	saved := out
	out = p
	defer func() { out = saved }()
	err := root.ExecuteContext(ctx)
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		err = core.Interrupted()
	}
	if p.err == nil || core.IsInterrupted(err) {
		return err
	}
	if ce := core.AsError(err); err != nil && (ce == nil || !ce.Silent) {
		return err
	}
	return core.APIf("writing the payload to stdout: %v", p.err)
}

// finish maps a returned error to the exit-code contract and renders the stderr
// envelope — unless the error is Silent (the command already wrote its verdict
// to stdout and needs only a nonzero exit code).
//
// It CLASSIFIES and then delegates: the number itself always comes from
// core.ExitCode, so the contract has one implementation rather than two that
// agree by inspection. An unclassified error is CodeAPI here as in core
// (DESIGN §5): cobra's parse errors are made usage where cobra raises them —
// the flag-error func, the Args validators, and the root's PersistentPreRunE
// for required flags and flag groups (newRootCmd) — so a bare error reaching
// finish is one nobody classified, never usage by elimination.
func finish(err error) int {
	if err == nil {
		return int(core.CodeOK)
	}
	ce := core.AsError(err)
	if ce == nil {
		ce = core.APIf("%s", err.Error())
	}
	if !ce.Silent {
		renderError(ce)
	}
	return core.ExitCode(ce)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "glyph",
		Short: "sigil-driven commit-lint, semver, and release notes for squash-merge repos",
		Long: "glyph reads each commit's version sigil (= none / ~ patch / ^ minor / ! major /\n" +
			"% promote to 1.0.0)\n" +
			"under the repository's own glyph.toml — your regex patterns decide the\n" +
			"grammar; the named group semver_sigil is the only input to version\n" +
			"calculation — and derives the semantic-version bump and release notes from\n" +
			"the individual commits inside a pull request, so a multi-commit squash-merge\n" +
			"(which rewrites the squash subject to the PR title) can never lose the\n" +
			"per-commit sigils. One binary lints commits, computes the next version, and\n" +
			"renders notes. `glyph init --gemoji` (or --conventional) writes a starting\n" +
			"glyph.toml; lint, bump, notes, preview, release and doctor read the one at\n" +
			"the repository root.",
		Args:          noArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Resolve().String(),
		// With no subcommand, print help and exit 0. An UNKNOWN subcommand is
		// rejected by noArgs before RunE runs, so it maps to usage(2).
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		// cobra checks required flags and flag groups after every pre-run and
		// returns plain errors, which finish reads as 4; the same two checks
		// run here first, so their refusal is usage. No subcommand may define
		// its own PersistentPreRunE: cobra runs only the nearest one.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.ValidateRequiredFlags(); err != nil {
				return core.Usagef("%s", err.Error())
			}
			if err := cmd.ValidateFlagGroups(); err != nil {
				return core.Usagef("%s", err.Error())
			}
			return nil
		},
	}
	root.SetVersionTemplate("glyph {{.Version}}\n")
	// Before InitDefaultCompletionCmd: the shell subcommands capture the
	// root's writer once, there, so it must already be the funnel to out.
	root.SetOut(stdoutFunnel{})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return core.Usagef("%s", err.Error())
	})
	root.AddCommand(newVersionCmd(), newLintCmd(), newBumpCmd(), newNotesCmd(), newPreviewCmd(), newReleaseCmd(), newHookCmd(), newDoctorCmd(), newInitCmd(), newEmojiCmd())

	// `completion` is cobra's, and the root's noArgs cannot reach it: cobra
	// gives it Args: NoArgs but no Run, and Command.execute returns
	// flag.ErrHelp for an unrunnable command BEFORE ValidateArgs — so
	// `glyph completion zshh > _glyph` would exit 0 and write help into a file
	// the shell sources. A help RunE makes it runnable; noArgs, swapped in for
	// cobra's NoArgs so the refusal points at `glyph completion --help` like
	// every other parent's, then rejects the argument at 2, and bare
	// `glyph completion` still prints help at 0, as `glyph hook` does. The
	// shells get noArgs too: cobra's NoArgs refuses with a plain error.
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.Args = noArgs
			c.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
			for _, shell := range c.Commands() {
				shell.Args = noArgs
			}
		}
	}

	// `help` is cobra's too, and it gives it no Args: its Run resolves the
	// topic with Find, which returns the deepest command plus the words left
	// over without error whenever that command has Args set — every glyph
	// command does — so `glyph help nosuch` printed the root's help at 0 and
	// `glyph help lint extra` lint's. This guard hands the leftover words to
	// the topic's own noArgs, so the refusal is the one `glyph nosuch` or
	// `glyph lint extra` gives, at 2. InitDefaultHelpCmd is idempotent: the
	// call cobra makes again at Execute keeps this same command.
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Args = func(cmd *cobra.Command, args []string) error {
				topic, rest, err := cmd.Root().Find(args)
				if err != nil {
					return err
				}
				return noArgs(topic, rest)
			}
		}
	}
	return root
}
