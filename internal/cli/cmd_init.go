package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/spf13/cobra"
)

// initPresetFlags carries one bool flag per shipped preset, discovered from
// the embedded set — a preset added to internal/config/presets/ becomes a
// flag with no edit here, so the command and the shipped artifacts cannot
// disagree about what exists.
var (
	initPresetFlags = map[string]*bool{}
	initForce       bool
)

func newInitCmd() *cobra.Command {
	names := config.PresetNames()
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a glyph.toml preset into the current directory",
		Long: "init writes glyph.toml — the v2 configuration in which YOUR regex patterns\n" +
			"decide the commit grammar and the named group semver_sigil (= none / ~ patch /\n" +
			"^ minor / ! major / % promote to 1.0.0) is the only input to version\n" +
			"calculation.\n\n" +
			"One preset flag is required (--" + strings.Join(names, ", --") + "); the file it\n" +
			"writes is a starting point to edit, not a contract to keep. An existing\n" +
			"glyph.toml is never touched without --force.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkExclusiveBool(cmd, names...); err != nil {
				return err
			}
			chosen := ""
			for _, name := range names {
				if *initPresetFlags[name] {
					chosen = name
				}
			}
			if chosen == "" {
				return core.Usagef("pick a preset: --%s", strings.Join(names, " or --"))
			}
			return runInit(chosen)
		},
	}
	for _, name := range names {
		v := new(bool)
		initPresetFlags[name] = v
		cmd.Flags().BoolVar(v, name, false, "write the "+name+" preset")
	}
	cmd.Flags().BoolVar(&initForce, "force", false, "overwrite an existing glyph.toml")
	return cmd
}

func runInit(preset string) error {
	data, ok := config.Preset(preset)
	if !ok {
		return core.Usagef("unknown preset %q", preset)
	}
	const path = "glyph.toml"
	// Lstat, not Stat: a broken symlink is still something a user put there,
	// and overwriting it silently is the same offence as overwriting a file.
	if _, err := os.Lstat(path); err == nil && !initForce {
		return core.Usagef("%s already exists — edit it in place, or pass --force to replace it with the %s preset", path, preset)
	} else if err != nil && !os.IsNotExist(err) {
		return core.APIf("checking %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return core.APIf("writing %s: %v", path, err)
	}
	fmt.Fprintf(out, "wrote %s (%s preset)\n", path, preset)
	return nil
}
