package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/spf13/cobra"
)

var (
	hookForce  bool
	hookPrint  bool
	hookNDJSON bool
)

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage the commit-msg hook that lints through glyph",
		Long: "hook manages the commit-msg hook, which moves the convention check from CI\n" +
			"to the moment the message is written.\n\n" +
			"The hook holds NO copy of the convention — it calls `glyph lint --stdin`, so\n" +
			"it cannot fall out of lockstep when the rules move, which is exactly what the\n" +
			"hand-written per-repo regexes it replaces did. Without glyph on PATH it warns\n" +
			"and lets the commit through, and it blocks for ONE reason only: lint's\n" +
			"convention-violation exit. The commit-lint CI job stays the authority; this is\n" +
			"early warning, and a tool that is unwell must never stop anyone committing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHookInstallCmd())
	return cmd
}

func newHookInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the commit-msg hook into this repository",
		Long: "install writes a commit-msg hook that pipes the message being written through\n" +
			"`glyph lint --stdin`, so a convention violation surfaces at authoring time\n" +
			"instead of in CI. The hook carries no copy of the rules — that is what makes\n" +
			"it drift-proof, and it is why the hand-written per-repo regexes it replaces\n" +
			"kept enforcing a retired form.\n\n" +
			"The destination honours core.hooksPath, so a repo that tracks its hooks under\n" +
			"scripts/hooks is written there (and the change shows up in git status, to be\n" +
			"committed like any other file).\n\n" +
			"An existing hook glyph did not write is never clobbered: install refuses and\n" +
			"exits 2 until --force. Where glyph is absent from PATH the installed hook\n" +
			"warns and lets the commit through — CI stays the authority.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if hookPrint {
				fmt.Fprint(out, hook.Script)
				return nil
			}
			hooksDir, err := gitsource.HooksDir(cmd.Context(), ".")
			if err != nil {
				return err
			}
			res, err := hook.Install(hooksDir, hookForce)
			if err != nil {
				return err
			}
			if hookNDJSON {
				printCompact(res)
				return nil
			}
			fmt.Fprintln(out, res.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVar(&hookForce, "force", false, "replace an existing commit-msg hook glyph did not write")
	cmd.Flags().BoolVar(&hookPrint, "print", false, "write the hook script to stdout instead of installing it")
	cmd.Flags().BoolVar(&hookNDJSON, "ndjson", false, "emit compact single-line JSON instead of the human line")
	// Value-aware, not Changed-aware: `--print=false --force` asks to install
	// rather than print, and force the install — a coherent invocation cobra's
	// grouping refused while reporting that both flags "were all set".
	//
	// The group has no default-bearing member (installing is what the command
	// does when nothing is selected, and no flag names that), so there is no
	// checkDefaultModeOff here: --print=false and --ndjson=false both leave the
	// command with something to do.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := checkExclusiveBool(cmd, "print", "force"); err != nil {
			return err
		}
		return checkExclusiveBool(cmd, "print", "ndjson")
	}
	return cmd
}
