package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"

	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/spf13/cobra"
)

var (
	hookForce bool
	hookPrint bool
	hookJSON  bool
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
	cmd.AddCommand(newHookInstallCmd(), newHookPrePushCmd())
	return cmd
}

// newHookPrePushCmd is what a pre-push hook calls. It takes git's argv
// verbatim and reads git's protocol on stdin.
//
// ArbitraryArgs, and no flags at all, is the rollout decision. The script that
// calls this is installed once into ~34 clones and nothing refreshes one, so a
// named flag would freeze today's argv shape into every copy: the day git passes
// a third argument, every frozen script turns a push into a usage error. Passing
// argv through unread is the only shape that survives its own installed base —
// the same reasoning DESIGN §2.1 used to put the cleanup mode in the binary.
func newHookPrePushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-push [remote] [url]",
		Short: "Lint the commits a push would add, reading git's pre-push protocol on stdin",
		Long: "pre-push is called BY the installed pre-push hook, not by hand. git writes one\n" +
			"line per ref to stdin (<local ref> <local sha> <remote ref> <remote sha>) and\n" +
			"passes the remote on argv; this lints the commits that push would add which\n" +
			"the remote does not already have.\n\n" +
			"It is the only place the merge-candidate rules can fire before CI: the\n" +
			"commit-msg hook judges one message with no branch behind it, so\n" +
			":construction: is legal there and stays legal here — a violation blocks ONLY\n" +
			"when the ref being written is the remote's default branch. Everywhere else it\n" +
			"warns and exits 0, because refusing a legal mid-branch commit makes the branch\n" +
			"unpushable and the only escape is --no-verify, which turns the gate off\n" +
			"entirely.\n\n" +
			"Deletions, tags and a push with nothing to do are skipped in silence. The\n" +
			"default branch is read from the local refs/remotes/<remote>/HEAD and never\n" +
			"over the network; where nothing records it, nothing blocks and the run says so.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			table, err := loadRules()
			if err != nil {
				return err
			}
			return prePushRun(cmd.Context(), args, authoringLintOptions(table))
		},
	}
}

func newHookInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "install [kind...]",
		Short:     "Install glyph's git hooks into this repository",
		ValidArgs: hook.KindNames(),
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
			"warns and lets the commit through — CI stays the authority.\n\n" +
			"Naming no kind installs every hook glyph writes (commit-msg, pre-push).\n" +
			"Name kinds to install a subset — the escape when one of them is somebody\n" +
			"else's file. Nothing is written unless every named kind can be: a run that\n" +
			"refused halfway would report failure over a tree that had already changed.",
		Args: cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			kinds, err := resolveHookKinds(args)
			if err != nil {
				return err
			}
			if hookPrint {
				// One script or none: two concatenated shell scripts are not a
				// script, and writing them out as if they were is worse than
				// refusing.
				if len(args) != 1 {
					return core.Usagef("--print writes ONE hook script; name the kind (one of %s)", strings.Join(hook.KindNames(), ", "))
				}
				fmt.Fprint(out, kinds[0].Script)
				return nil
			}
			hooksDir, herr := gitsource.HooksDir(cmd.Context(), ".")
			if herr != nil {
				return herr
			}
			rep, ierr := hook.Install(hooksDir, hookForce, kinds...)
			if ierr != nil {
				return ierr
			}
			if hookJSON {
				printCompact(rep)
				return nil
			}
			fmt.Fprintln(out, rep.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVar(&hookForce, "force", false, "replace an existing hook glyph did not write")
	cmd.Flags().BoolVar(&hookPrint, "print", false, "write one named hook's script to stdout instead of installing it")
	cmd.Flags().BoolVar(&hookJSON, "json", false, "emit the machine result {hooks:[{kind,path,action,existed}]} as one JSON line")
	// Value-aware, not Changed-aware: `--print=false --force` asks to install
	// rather than print, and force the install — a coherent invocation cobra's
	// grouping refused while reporting that both flags "were all set".
	//
	// The group has no default-bearing member (installing is what the command
	// does when nothing is selected, and no flag names that), so there is no
	// checkDefaultModeOff here: --print=false and --json=false both leave the
	// command with something to do.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := checkExclusiveBool(cmd, "print", "force"); err != nil {
			return err
		}
		return checkExclusiveBool(cmd, "print", "json")
	}
	return cmd
}

// resolveHookKinds maps the command's positional arguments to hook kinds. No
// argument means every kind glyph writes — bare `glyph hook install` is the
// invocation the fleet's CONTRIBUTING points at, so it must keep working AND
// must not quietly omit a gate that was added after it was written.
func resolveHookKinds(args []string) ([]hook.Kind, error) {
	if len(args) == 0 {
		return hook.Kinds, nil
	}
	kinds := make([]hook.Kind, 0, len(args))
	for _, name := range args {
		k, ok := hook.KindByName(name)
		if !ok {
			return nil, core.Usagef("glyph writes no %q hook; the kinds are %s", name, strings.Join(hook.KindNames(), ", "))
		}
		kinds = append(kinds, k)
	}
	return kinds, nil
}
