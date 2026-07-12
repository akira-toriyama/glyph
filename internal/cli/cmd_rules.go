package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/spf13/cobra"
)

// rulesJSON / rulesMD select the `glyph rules` output format. They are mutually
// exclusive (enforced by cobra). Only rulesJSON is read: Markdown is the default,
// so an explicit --md takes the same path as no flag at all. rulesMD exists to
// register the flag (documenting the default) and to form the exclusion group.
var (
	rulesJSON bool
	rulesMD   bool
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Print the embedded gitmoji → semver table",
		Long: "rules self-prints the pinned gitmoji rules the binary embeds — the machine\n" +
			"source of truth for classification and notes. --json reproduces the embedded\n" +
			"rules.json verbatim (pipe it to jq); --md (the default) renders the docs table\n" +
			"that CI diffs against docs/gitmoji-table.md to catch drift.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().BoolVar(&rulesJSON, "json", false, "emit the embedded rules.json verbatim")
	cmd.Flags().BoolVar(&rulesMD, "md", false, "render the Markdown docs table (default)")
	cmd.MarkFlagsMutuallyExclusive("json", "md")
	return cmd
}
