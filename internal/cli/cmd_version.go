package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/version"
	"github.com/spf13/cobra"
)

// versionJSON: the version subcommand owns its own --json flag. A root-level
// one would be a LOCAL (not persistent) flag and would not reach a subcommand,
// so `glyph version --json` would otherwise error.
//
// It was spelled --ndjson until v1.0.0, which named a format glyph has never
// emitted — printCompact writes one object, not a stream — and split the
// machine flag into two spellings a caller had to memorise per subcommand.
var versionJSON bool

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the glyph version",
		Long: "version prints the build identity: the release tag, the commit it was built\n" +
			"from, and the build date, stamped in by ldflags at release time and\n" +
			"recovered from the module's build info otherwise (so a `go install` build\n" +
			"still identifies itself). It is the one command that reaches nothing —\n" +
			"no git, no API, no repository — so it answers anywhere.\n\n" +
			"The binary IS the pinned rules (the gitmoji table is embedded), so this\n" +
			"version identifies the convention in force as well as the code.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Resolve()
			if versionJSON {
				printCompact(info)
				return nil
			}
			fmt.Fprintf(out, "glyph %s\n", info.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&versionJSON, "json", false, "emit the machine identity {version,commit,date} as one JSON line")
	return cmd
}
