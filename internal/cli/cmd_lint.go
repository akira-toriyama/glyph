package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

// The three lint input modes; exactly one is required (cobra-enforced).
var (
	lintRange   string
	lintMessage string
	lintStdin   bool
)

func newLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint commit messages against the gitmoji convention",
		Long: "lint checks commit messages against the gitmoji convention\n" +
			"(`<:code:>[(scope)][!] <subject>`; legacy Conventional tokens are accepted).\n" +
			"--range lints every commit on its way into main (bots, merges, autosquash\n" +
			"artifacts and raw git reverts are skipped; :construction: is a violation\n" +
			"there). --message and --stdin lint one message at authoring time — the\n" +
			"commit-msg hook path, where :construction: stays legal. Violations exit 3\n" +
			"with a structured stderr envelope; a clean run is silent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			table, err := loadRules()
			if err != nil {
				return err
			}
			known := func(code string) bool { _, ok := table.Lookup(code); return ok }
			// EVERY arm asks whether the flag was GIVEN, never what its value
			// is — the same question MarkFlagsOneRequired answers, so the group
			// check and the dispatch cannot disagree about which mode was
			// selected. Dispatching --stdin on its VALUE meant an explicit
			// --stdin=false satisfied the group (cobra asks pflag's Changed) and
			// then matched no arm, so the run fell through to an empty --message
			// and answered a bad INVOCATION with 3 — the gate code the fleet's
			// commit-lint job hard-fails on, for a commit nobody submitted. It
			// swallowed a good message too: a valid subject on stdin was reported
			// as "empty commit message".
			switch {
			case cmd.Flags().Changed("range"):
				return lintRangeRun(cmd.Context(), lintRange, known)
			case cmd.Flags().Changed("stdin"):
				if !lintStdin {
					return core.Usagef("--stdin=false selects no input mode — --stdin IS the mode, so drop it and give --range or --message instead")
				}
				b, rerr := io.ReadAll(in)
				if rerr != nil {
					return core.APIf("reading stdin: %v", rerr)
				}
				// --stdin is the commit-msg hook, which git invokes BEFORE its
				// own cleanup: the file still carries the editor template, the
				// status block and (under commit.verbose) the diff. Reduce it to
				// the message git will record before judging it.
				return lintOne(parser.Cleanup(string(b)), known)
			case cmd.Flags().Changed("message"):
				// An empty --message is the caller naming no message, which is
				// usage — not a message that violates the convention. The old
				// fall-through could not tell the two apart and called both 3.
				if err := checkGivenEmpty(cmd, "message", "message",
					"name the message to lint (--message='<:code:> subject'), or read one from the commit-msg hook with --stdin"); err != nil {
					return err
				}
				return lintOne(lintMessage, known)
			default:
				// Unreachable while MarkFlagsOneRequired holds. Kept as usage
				// rather than a panic so a fourth mode added without its arm is
				// diagnosed as a bad invocation instead of crashing a CI gate.
				return core.Usagef("lint needs one of --range, --message or --stdin")
			}
		},
	}
	cmd.Flags().StringVar(&lintRange, "range", "", "lint every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().StringVar(&lintMessage, "message", "", "lint one message given inline")
	cmd.Flags().BoolVar(&lintStdin, "stdin", false, "lint one message read from stdin (commit-msg hook)")
	cmd.MarkFlagsMutuallyExclusive("range", "message", "stdin")
	cmd.MarkFlagsOneRequired("range", "message", "stdin")
	return cmd
}

// lintOne lints a single message at authoring time (no merge-candidate rules).
//
// Subjects git writes itself are skipped, exactly as the --range walk skips
// them (bump.ExcludedFromClassification — the message question, the only one a
// hook can ask: there is no commit to resolve yet). Authoring time is where
// that tolerance matters MOST: the range walk only ever sees these after the
// fact, whereas the commit-msg hook stands between the developer and
// `git merge` / `git commit --fixup`. Judging them here rejected messages CI
// accepts — an author cannot rewrite a subject git generated, so the only
// escape was --no-verify, which turns the whole gate off. The retired shell
// hook exempted the same four prefixes.
func lintOne(message string, known func(string) bool) error {
	if _, excluded := bump.ExcludedFromClassification("", firstLine(message), 0); excluded {
		return nil
	}
	vs := parser.Lint(message, parser.LintOptions{Known: known})
	if len(vs) == 0 {
		return nil
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     fmt.Sprintf("%d commit-convention violation(s)", len(vs)),
		Details: vs,
	}
}

// rangeViolation is one finding over a range, anchored to its commit.
type rangeViolation struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Rule    string `json:"rule"`
	Detail  string `json:"detail"`
}

// lintRangeRun lints every participating commit in revRange as a merge
// candidate. Excluded commits (bots, merges, autosquash artifacts, raw git
// reverts) are skipped, never failed — the same tolerance the retired shell
// lint gave history.
func lintRangeRun(ctx context.Context, revRange string, known func(string) bool) error {
	if err := checkRangeFlag(revRange); err != nil {
		return err
	}
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return err
	}
	var all []rangeViolation
	checked := 0
	for _, raw := range raws {
		subject := firstLine(raw.Message)
		if _, excluded := bump.ExcludedFromClassification(raw.Author, subject, raw.Parents); excluded {
			continue
		}
		checked++
		for _, v := range parser.Lint(raw.Message, parser.LintOptions{Known: known, MergeCandidate: true}) {
			all = append(all, rangeViolation{SHA: raw.SHA, Subject: subject, Rule: v.Rule, Detail: v.Detail})
		}
	}
	if len(all) == 0 {
		return nil
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     fmt.Sprintf("%d commit-convention violation(s) across %d linted commit(s) in %s", len(all), checked, revRange),
		Details: all,
	}
}
