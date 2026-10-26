// Package cli is glyph's command layer: the cobra wiring and flag checks, the
// dispatch to an input source (a message, --range, --pr, --since-tag), the
// composition of the walk's ports (walk.go: the checkout, the API client and
// the diagnostic stream it reads and reports through), the rendering of every
// verdict under DESIGN §5's stream contract, and the exit funnel — Execute()
// int maps every error onto glyph's exit-code contract (internal/core).
//
// It also carries out the release's write list (cmd_release.go: the upserts
// and deletes draftplan.PlanRun ordered, against GitHub) — the API client
// lives here, so the writes and the failure policy each kind names stay
// here; which writes, in what order and of which kind is draftplan's.
//
// It holds no copy of: a message's meaning (internal/config's pattern match),
// the lattice, the fold and the commit that decides it, Version.Next and the
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
	"os"
	"os/signal"
	"strings"
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
	return finish(newRootCmd().ExecuteContext(ctx))
}

// finish maps a returned error to the exit-code contract and renders the stderr
// envelope — unless the error is Silent (the command already wrote its verdict
// to stdout and needs only a nonzero exit code).
//
// It CLASSIFIES and then delegates: the number itself always comes from
// core.ExitCode, so the contract has one implementation rather than two that
// agree by inspection. The classification rests on a fact only this layer
// knows: cobra is silenced in newRootCmd, so a bare error arriving here is a
// parse/usage problem and nothing else — core.ExitCode, which cannot know
// that, reads an unclassified error as CodeAPI. That fact belongs where it is
// knowable, and the mapping belongs in core.
func finish(err error) int {
	if err == nil {
		return int(core.CodeOK)
	}
	ce := core.AsError(err)
	if ce == nil {
		msg := err.Error()
		// cobra's own errors arrive bare (SilenceErrors drops its "Run 'glyph
		// --help'" tail), and the unknown-command reply is the one place a
		// lost user lands with no next step — restore one.
		if strings.HasPrefix(msg, "unknown command ") {
			msg += " — run 'glyph --help' for the command list"
		}
		ce = core.Usagef("%s", msg)
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
			"the individual commits inside a pull request, so a squash-merge (which\n" +
			"rewrites the squash subject to the PR title) can never lose the per-commit\n" +
			"sigils. One binary lints commits, computes the next version, and renders\n" +
			"notes. `glyph init --gemoji` (or --conventional) writes a starting\n" +
			"glyph.toml; everything else reads the one at the repository root.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Resolve().String(),
		// With no subcommand, print help and exit 0. An UNKNOWN subcommand is
		// rejected by cobra.NoArgs before RunE runs, so it maps to usage(2).
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetVersionTemplate("glyph {{.Version}}\n")
	root.AddCommand(newVersionCmd(), newLintCmd(), newBumpCmd(), newNotesCmd(), newPreviewCmd(), newReleaseCmd(), newHookCmd(), newDoctorCmd(), newInitCmd(), newEmojiCmd())

	// `completion` is cobra's, not ours, and it arrives with a hole the root's
	// own cobra.NoArgs cannot cover: cobra sets Args: NoArgs on it but gives it
	// no Run, and Command.execute() returns flag.ErrHelp for an unrunnable
	// command BEFORE it validates arguments. So the NoArgs never ran, and
	// `glyph completion bogus` printed the parent's help to STDOUT and exited 0
	// — the one invalid-argument path in this CLI that was not usage(2).
	//
	// That is not a cosmetic exit code. The output of this command is redirected
	// into a file the shell then sources, so `glyph completion zsh > _glyph` and
	// `glyph completion zshh > _glyph` both reported success and the second wrote
	// 563 bytes of English prose where a completion script belongs — a syntax
	// error at every subsequent shell start, with nothing having failed.
	//
	// Making the parent RUNNABLE is the whole fix: execute() then reaches
	// ValidateArgs and cobra's own NoArgs rejects the argument. `glyph hook`,
	// this repo's own parent command, has carried both an Args and a help RunE
	// since it was written (cmd_hook.go) — this only gives cobra's command the
	// same pair. A bare `glyph completion` still prints help at 0, exactly as
	// `glyph hook` and bare `glyph` do.
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
		}
	}
	return root
}
