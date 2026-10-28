package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/core"

	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/akira-toriyama/glyph/v6/internal/hook"
	"github.com/spf13/cobra"
)

// hookInstallOptions is what one hook install run was asked, snapshotted at
// RunE: --force, --print, --json and the kinds resolved from the positionals —
// every kind when none is named. Nothing below RunE reads a flag —
// hookInstallRun is a function of these alone
// (TestHookInstallRunsFromOptionsWithoutCobra).
type hookInstallOptions struct {
	force, print, json bool
	kinds              []hook.Kind
}

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage the git hooks that lint through glyph",
		Long: "hook manages glyph's two git hooks — commit-msg lints the message being\n" +
			"written, pre-push lints the commits a push would add — moving the convention\n" +
			"check from CI to the moment the work happens.\n\n" +
			"Neither hook holds a copy of the convention — both call glyph, so they cannot\n" +
			"fall out of lockstep when the rules move. Without glyph on PATH they warn\n" +
			"and let you through, and they block for ONE reason only: lint's\n" +
			"convention-violation exit. The commit-lint CI job stays the authority; this is\n" +
			"early warning, and a tool that is unwell must never stop anyone committing.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHookInstallCmd(), newHookPrePushCmd())
	return cmd
}

// newHookPrePushCmd is what a pre-push hook calls. It takes git's argv
// verbatim and reads git's protocol on stdin.
//
// ArbitraryArgs and no flags: every installed script runs
// `glyph hook pre-push -- "$@"` and nothing refreshes it (DESIGN §2.1), so an
// arity check would make its run a usage error the day git's argv changes, and
// a flag is one no installed copy could ever set.
func newHookPrePushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-push [remote] [url]",
		Short: "Lint the commits a push would add, reading git's pre-push protocol on stdin",
		Long: "pre-push is called BY the installed pre-push hook, not by hand. git writes one\n" +
			"line per ref to stdin (<local ref> <local sha> <remote ref> <remote sha>) and\n" +
			"passes the remote on argv; this lints the commits that push would add which\n" +
			"the remote does not already have.\n\n" +
			"Both hooks run the same patterns; what pre-push adds is the branch behind\n" +
			"the message. On a repository declaring [[packages]] it is where the\n" +
			"attribution rules fire: --message and --stdin see no files, so a version\n" +
			"sigil on a commit no line can carry (shared files, no package scope) is\n" +
			"caught here and nowhere earlier on this machine. A violation blocks ONLY\n" +
			"when the ref being written is the remote's default branch. Everywhere else\n" +
			"the finding is real but the consequence is not — a repo's patterns may bless\n" +
			"a WIP shape mid-branch that its gate rejects at the merge — so it warns and\n" +
			"exits 0: refusing a legal mid-branch commit makes the branch unpushable and\n" +
			"the only escape is --no-verify, which turns the gate off entirely.\n\n" +
			"Deletions, tags and a push with nothing to do are skipped in silence. The\n" +
			"default branch is read from the local refs/remotes/<remote>/HEAD and never\n" +
			"over the network; where nothing records it, nothing blocks and the run says so.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return prePushRun(cmd.Context(), args)
		},
	}
}

func newHookInstallCmd() *cobra.Command {
	var o hookInstallOptions
	cmd := &cobra.Command{
		Use:       "install [kind...]",
		Short:     "Install glyph's git hooks into this repository",
		ValidArgs: hook.KindNames(),
		Long: "install writes glyph's git hooks: a commit-msg hook that hands git's message\n" +
			"file to `glyph lint --stdin`, and a pre-push hook that lints the commits a push\n" +
			"would add — so a convention violation surfaces at authoring time instead of in\n" +
			"CI. The hooks carry no copy of the rules: each calls the installed glyph,\n" +
			"which reads the repository's glyph.toml at run time, so the rules a hook\n" +
			"enforces cannot fall out of lockstep when they move.\n\n" +
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
		Args: hookKindArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			kinds, err := resolveHookKinds(args)
			if err != nil {
				return err
			}
			// One script or none: two concatenated shell scripts are not a
			// script, and writing them out as if they were is worse than
			// refusing.
			if o.print && len(args) != 1 {
				return core.Usagef("--print writes ONE hook script; name the kind (one of %s)", strings.Join(hook.KindNames(), ", "))
			}
			o.kinds = kinds
			return hookInstallRun(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "replace an existing hook glyph did not write")
	cmd.Flags().BoolVar(&o.print, "print", false, "write one named hook's script to stdout instead of installing it")
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the machine result {hooks:[{kind,path,action,existed}]} as one JSON line")
	// Value-aware, not Changed-aware: `--print=false --force` asks to install
	// rather than print, and force the install — a coherent invocation cobra's
	// grouping refused while reporting that both flags "were all set".
	//
	// The group has no default-bearing member (installing is what the command
	// does when nothing is selected, and no flag names that), so nothing here
	// rejects an all-false invocation: --print=false and --json=false both
	// leave the command with something to do.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := checkExclusiveBool(cmd, "print", "force"); err != nil {
			return err
		}
		return checkExclusiveBool(cmd, "print", "json")
	}
	return cmd
}

// hookInstallRun is hook install below its flags: under print the one named
// kind's script on stdout, otherwise every resolved kind installed into the
// hooks directory git runs and the report — compact JSON, or one human line
// per hook.
func hookInstallRun(ctx context.Context, o hookInstallOptions) error {
	if o.print {
		fmt.Fprint(out, o.kinds[0].Script)
		return nil
	}
	hooksDir, herr := gitsource.HooksDir(ctx, ".")
	if herr != nil {
		return herr
	}
	rep, ierr := hook.Install(hooksDir, o.force, o.kinds...)
	if ierr != nil {
		return ierr
	}
	if o.json {
		printCompact(rep)
		return nil
	}
	fmt.Fprintln(out, rep.Summary())
	return nil
}

// hookKindArgs is install's Args guard: every positional must name a hook kind
// glyph writes, refused with resolveHookKinds' own sentence — the one refusal
// for that mistake — in place of cobra.OnlyValidArgs, whose `invalid argument
// "bogus" for "glyph hook install"` names neither the kinds nor where to find
// them.
func hookKindArgs(_ *cobra.Command, args []string) error {
	_, err := resolveHookKinds(args)
	return err
}

// resolveHookKinds maps the command's positional arguments to hook kinds. No
// argument means every kind glyph writes — bare `glyph hook install` is the
// invocation the fleet's CONTRIBUTING points at, so it must keep working AND
// must not quietly omit a gate that was added after it was written.
func resolveHookKinds(args []string) ([]hook.Kind, error) {
	if len(args) == 0 {
		return hook.Kinds(), nil
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
