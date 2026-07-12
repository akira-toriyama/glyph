package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

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
			switch {
			case cmd.Flags().Changed("range"):
				return lintRangeRun(cmd.Context(), lintRange, known)
			case lintStdin:
				b, rerr := io.ReadAll(in)
				if rerr != nil {
					return core.APIf("reading stdin: %v", rerr)
				}
				return lintOne(string(b), known)
			default:
				return lintOne(lintMessage, known)
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
func lintOne(message string, known func(string) bool) error {
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
		if _, excluded := bump.Excluded(raw.Author, subject, raw.Parents); excluded {
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

// checkRangeFlag rejects an empty or option-shaped --range before git runs —
// caller input, so usage, not an API failure.
func checkRangeFlag(revRange string) error {
	if strings.TrimSpace(revRange) == "" {
		return core.Usagef("--range needs a git revision range like BASE..HEAD")
	}
	if strings.HasPrefix(revRange, "-") {
		return core.Usagef("--range %q looks like an option, not a revision range", revRange)
	}
	return nil
}

// firstLine returns the subject line of a raw message (CR trimmed) — what the
// participation rules match on.
func firstLine(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSuffix(message, "\r")
}
