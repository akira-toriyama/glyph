package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

// The two fmt input modes; exactly one is required (cobra-enforced) — the same
// authoring-time pair lint has, minus the modes that read history: a range and
// a PR title are records, not drafts, and fmt only ever touches a draft.
var (
	fmtMessage string
	fmtStdin   bool
)

func newFmtCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fmt",
		Short: "Rewrite a commit message into the convention, or refuse",
		Long: "fmt prints the corrected message — the answer you paste, not advice about\n" +
			"it. Where lint's findings carry a mechanical fix (the retired Conventional\n" +
			"token, an uppercase first rune, trailing periods, a scope that lowercasing\n" +
			"legalises), fmt applies them all at once and prints the whole message,\n" +
			"first line corrected, every other byte untouched. A clean message prints\n" +
			"unchanged (fmt is idempotent). stdout is the payload — there is no --json.\n\n" +
			"What it will not do is guess: a violation with no mechanical fix (an\n" +
			"unknown code, a WIP marker, an undeclared removal, an all-caps word the\n" +
			"lowercasing would mangle) is a REFUSAL at exit 3 with the violations on\n" +
			"stderr, never a best-effort line — output that still fails lint is the\n" +
			"defect this command exists to end. The invariant is enforced in code: what\n" +
			"fmt prints, lint passes.\n\n" +
			"--stdin reads the message the way the commit-msg hook does (git's cleanup\n" +
			"applied, comments and template stripped), so a COMMIT_EDITMSG pipes in as\n" +
			"the message git would record. Subjects git writes itself (merges, fixups,\n" +
			"reverts) and bot-authored drafts pass through unchanged — they are not\n" +
			"glyph's to reword. fmt is NOT wired into any hook: the hook's founding\n" +
			"property is that it holds no opinion and asks lint, and a hook that\n" +
			"rewrote the message would be an opinion applied silently.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			table, err := loadRules()
			if err != nil {
				return err
			}
			known := func(code string) bool { _, ok := table.Lookup(code); return ok }
			// Changed, never the value — the same dispatch discipline lint's
			// arms follow, for the same incident (an explicit --stdin=false
			// matched no arm and answered a bad invocation with the gate code).
			switch {
			case cmd.Flags().Changed("stdin"):
				if !fmtStdin {
					return core.Usagef("--stdin=false selects no input mode — --stdin IS the mode, so drop it and give --message instead")
				}
				b, rerr := io.ReadAll(in)
				if rerr != nil {
					return core.APIf("reading stdin: %v", rerr)
				}
				return fmtRun(parser.Cleanup(string(b), hookCleanupMode(cmd.Context())), known)
			case cmd.Flags().Changed("message"):
				if err := checkGivenEmpty(cmd, "message", "message",
					"name the message to format (--message='<:code:> subject'), or pipe one with --stdin"); err != nil {
					return err
				}
				return fmtRun(fmtMessage, known)
			default:
				return core.Usagef("fmt needs one of --message or --stdin")
			}
		},
	}
	cmd.Flags().StringVar(&fmtMessage, "message", "", "format one message given inline")
	cmd.Flags().BoolVar(&fmtStdin, "stdin", false, "format one message read from stdin (git's cleanup applied)")
	cmd.MarkFlagsMutuallyExclusive("message", "stdin")
	cmd.MarkFlagsOneRequired("message", "stdin")
	return cmd
}

// fmtRun formats one message at authoring time. Exclusion mirrors lintOne
// exactly — same function, same UnknownParents — because a message fmt would
// reword and lint would skip is the two commands disagreeing about whose
// message it is.
func fmtRun(message string, known func(string) bool) error {
	if _, excluded := bump.ExcludedFromClassification("", firstLine(message), bump.UnknownParents); excluded {
		printMessage(message)
		return nil
	}
	formatted, vs := parser.Format(message, parser.LintOptions{Known: known})
	if vs != nil {
		return &core.Error{
			Code:    core.CodeLint,
			Msg:     fmt.Sprintf("%d violation(s) have no mechanical fix — this message needs its author", len(vs)),
			Details: vs,
		}
	}
	printMessage(formatted)
	return nil
}

// printMessage writes the payload with exactly one trailing newline — the shape
// `git commit -F -` and a shell substitution both want, whatever the input had.
func printMessage(message string) {
	fmt.Fprintln(out, strings.TrimRight(message, "\n"))
}
