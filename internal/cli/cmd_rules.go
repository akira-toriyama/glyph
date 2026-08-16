package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

// rulesJSON / rulesMD / rulesLint select the `glyph rules` output. Only
// rulesJSON and rulesLint decide the path taken — Markdown is the default, so
// an explicit --md is the same as no flag at all — but rulesMD is READ, by the
// guards in flags.go, so that `--md=false` is answered rather than ignored.
// Registering a flag whose value nothing looks at is how a caller's explicit
// "not this" became a no-op.
var (
	rulesJSON bool
	rulesMD   bool
	rulesLint bool
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Print the embedded token → semver table",
		Long: "rules self-prints the pinned rules the binary embeds — the machine source of\n" +
			"truth for classification and notes, one table per profile (--profile selects;\n" +
			"gitmoji is the default). --json reproduces the profile's embedded rules.json\n" +
			"verbatim (pipe it to jq); --md (the default) renders the docs table that CI\n" +
			"diffs against docs/gitmoji-table.md / docs/conventional-table.md to catch\n" +
			"drift; --lint lists the profile's lint vocabulary — every `rule` id a finding\n" +
			"can carry, as JSON.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkExclusiveBool(cmd, "json", "md", "lint"); err != nil {
				return err
			}
			if err := checkDefaultModeOff(cmd, "md",
				"omit it for the Markdown table, or ask for another surface with --json or --lint", "json", "lint"); err != nil {
				return err
			}
			if rulesLint {
				// The vocabulary is the parser's, not the table's: no loadRules,
				// so a broken embed cannot take this surface down with it — but
				// the profile still selects WHICH vocabulary, so the flag is
				// validated even on this path (an unknown profile must be usage
				// here exactly as everywhere else, not a silent default).
				p, err := resolveProfile()
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(marshal(struct {
					Rules []parser.LintRule `json:"rules"`
				}{Rules: parser.LintRules(p.grammar)}, true)))
				return nil
			}
			table, err := loadRules()
			if err != nil {
				return err
			}
			if rulesJSON {
				b, mErr := table.CanonicalJSON()
				if mErr != nil {
					return core.APIf("rendering rules as JSON: %v", mErr)
				}
				fmt.Fprintln(out, string(b))
				return nil
			}
			fmt.Fprint(out, table.Markdown())
			return nil
		},
	}
	cmd.Flags().BoolVar(&rulesJSON, "json", false, "emit the embedded rules.json verbatim — the pretty-printed file itself (511 lines), not the one-line envelope every other --json prints")
	cmd.Flags().BoolVar(&rulesMD, "md", false, "render the Markdown docs table (default)")
	cmd.Flags().BoolVar(&rulesLint, "lint", false, "list the lint vocabulary as JSON: every finding `rule` id, each with merge_candidate_only; semantics live in docs/DESIGN.md §2")
	return cmd
}
