package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/version"
	"github.com/spf13/cobra"
)

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
