// Package cli is glyph's cobra adapter: it parses flags, drives the core
// packages, and renders results — mapping everything to glyph's exit-code
// contract via Execute() int. It holds no classification, bump, or notes logic;
// that lives in internal/{parser,bump,notes,gitmoji,github}.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/akira-toriyama/glyph/internal/core"
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
func finish(err error) int {
	if err == nil {
		return int(core.CodeOK)
	}
	ce := core.AsError(err)
	if ce == nil {
		// A bare error here is a cobra parse/usage problem, which is a usage
		// error by contract — never an unclassified (API) code.
		ce = &core.Error{Code: core.CodeUsage, Msg: err.Error()}
	}
	if !ce.Silent {
		renderError(ce)
	}
	return int(ce.Code)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "glyph",
		Short: "gitmoji-driven commit-lint, semver, and release notes for squash-merge repos",
		Long: "glyph reads the gitmoji that leads each commit as the change type, and derives\n" +
			"the semantic-version bump and release notes from the individual commits inside a\n" +
			"pull request — so a squash-merge (which rewrites the squash subject to the PR\n" +
			"title) can never lose the per-commit types. One binary lints commits, computes\n" +
			"the next version, and renders notes.",
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
	root.AddCommand(newVersionCmd())
	return root
}

// versionNDJSON: the version subcommand owns its own --ndjson flag. A root
// --ndjson would be a LOCAL (not persistent) flag and would not reach a
// subcommand, so `glyph version --ndjson` would otherwise error.
var versionNDJSON bool

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the glyph version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Resolve()
			if versionNDJSON {
				printCompact(info)
				return nil
			}
			fmt.Fprintf(out, "glyph %s\n", info.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&versionNDJSON, "ndjson", false, "emit compact single-line JSON instead of the human line")
	return cmd
}
