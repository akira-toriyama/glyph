package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/spf13/cobra"
)

// rulesJSON / rulesMD select the `glyph rules` output format. Only rulesJSON
// decides the path taken — Markdown is the default, so an explicit --md is the
// same as no flag at all — but rulesMD is READ, by the guards in flags.go, so
// that `--md=false` is answered rather than ignored. Registering a flag whose
// value nothing looks at is how a caller's explicit "not this" became a no-op.
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
			if err := checkExclusiveBool(cmd, "json", "md"); err != nil {
				return err
			}
			if err := checkDefaultModeOff(cmd, "md",
				"omit it for the Markdown table, or ask for the other format with --json", "json"); err != nil {
				return err
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
	return cmd
}
