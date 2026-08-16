// Package cli is glyph's cobra adapter: it parses flags, drives the core
// packages, and renders results — mapping everything to glyph's exit-code
// contract via Execute() int. It holds no classification, bump, or notes logic;
// that lives in internal/{parser,bump,notes,gitmoji,github}.
package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/version"
	"github.com/spf13/cobra"
)

// Execute builds the root command, runs it, and maps the result to glyph's
// exit-code contract:
//
//	0 ok / 1 no release / 2 usage|bad input / 3 lint / 4 API|IO / 130 interrupted
//
// On a non-zero exit it prints {"error":{...}} to stderr, keeping stdout pure.
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
// agree by inspection. They used to disagree on paper — core.ExitCode documents
// an unclassified error as CodeAPI and says it must never fall through to
// usage, which is exactly what this function did to it — and the reason both
// were right is a fact only this layer knows: cobra is silenced in newRootCmd,
// so a bare error arriving here is a parse/usage problem and nothing else. That
// fact belongs where it is knowable, and the mapping belongs in core.
func finish(err error) int {
	if err == nil {
		return int(core.CodeOK)
	}
	ce := core.AsError(err)
	if ce == nil {
		ce = &core.Error{Code: core.CodeUsage, Msg: err.Error()}
	}
	if !ce.Silent {
		renderError(ce)
	}
	return core.ExitCode(ce)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "glyph",
		Short: "gitmoji-driven commit-lint, semver, and release notes for squash-merge repos",
		Long: "glyph reads the gitmoji that leads each commit as the change type, and derives\n" +
			"the semantic-version bump and release notes from the individual commits inside a\n" +
			"pull request — so a squash-merge (which rewrites the squash subject to the PR\n" +
			"title) can never lose the per-commit types. One binary lints commits, computes\n" +
			"the next version, and renders notes.\n\n" +
			"A second commit grammar ships in the same binary: --profile=conventional reads\n" +
			"Conventional Commits types (`<type>[(scope)][!]: <subject>`) with its own\n" +
			"embedded type → semver table, sharing the footer semantics, the walk and the\n" +
			"exit codes. The profile is a flag, never a repo config file, so it is stated\n" +
			"where the pinned version already is (DESIGN §2.2, §6).",
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
	root.PersistentFlags().StringVar(&profileFlag, "profile", defaultProfile,
		"commit-grammar profile: gitmoji (the default, the fleet's own) or conventional (Conventional Commits types)")
	root.AddCommand(newVersionCmd(), newRulesCmd(), newLintCmd(), newFmtCmd(), newBumpCmd(), newNotesCmd(), newPreviewCmd(), newReleaseCmd(), newHookCmd(), newDoctorCmd(), newInitCmd())

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

// loadRules loads the selected profile's embedded table for a command. An
// unknown --profile is usage; a table that fails to load is a build/embedding
// fault — classified as internal (API), never usage.
func loadRules() (*gitmoji.Table, error) {
	p, err := resolveProfile()
	if err != nil {
		return nil, err
	}
	table, err := p.load()
	if err != nil {
		return nil, core.APIf("loading %s rules: %v", p.name, err)
	}
	return table, nil
}
