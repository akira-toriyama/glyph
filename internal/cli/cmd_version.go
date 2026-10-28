package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/v6/internal/version"
	"github.com/spf13/cobra"
)

// versionOptions is what one version run was asked, snapshotted at RunE.
// Nothing below RunE reads a flag — versionRun is a function of these alone
// (TestVersionRunsFromOptionsWithoutCobra).
type versionOptions struct {
	// json: the version subcommand owns its own --json flag. A root-level
	// one would be a LOCAL (not persistent) flag and would not reach a
	// subcommand, so `glyph version --json` would otherwise error.
	json bool
}

func newVersionCmd() *cobra.Command {
	var o versionOptions
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the glyph version",
		Long: "version prints the build identity: the release tag, the commit it was built\n" +
			"from, and the build date, stamped in by ldflags at release time and\n" +
			"recovered from the module's build info otherwise (so a `go install` build\n" +
			"still identifies itself). Like `emoji`, it reaches nothing — no git, no\n" +
			"API, no repository — so it answers anywhere.\n\n" +
			"The rules are NOT in the binary: the repository's glyph.toml decides the\n" +
			"grammar, so this version identifies the engine alone, never the convention\n" +
			"in force.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return versionRun(o)
		},
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the machine identity {version,commit,date} as one JSON line")
	return cmd
}

// versionRun is version below its flag. It takes no context: the command
// reaches nothing.
func versionRun(o versionOptions) error {
	info := version.Resolve()
	if o.json {
		printCompact(info)
		return nil
	}
	fmt.Fprintf(out, "glyph %s\n", info.String())
	return nil
}
