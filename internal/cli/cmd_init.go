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
	initV1Window    bool
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
			"glyph.toml is never touched without --force.\n\n" +
			"--v1-window (gemoji only) appends the v1-acceptance window pattern: a\n" +
			"gitmoji subject with no sigil lints clean WITH a warning and folds as\n" +
			"none, so a repository whose history predates the sigil can adopt glyph\n" +
			"without rewriting it. The block is designed to be REMOVED once every\n" +
			"commit behind the release walk's base carries a sigil — its own comment\n" +
			"says so, and the warning it emits on every sigil-less commit is what\n" +
			"keeps the window from quietly becoming permanent.",
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
	cmd.Flags().BoolVar(&initV1Window, "v1-window", false, "append the v1-acceptance window pattern (gemoji only): sigil-less gitmoji subjects lint clean with a warning and fold as none")
	return cmd
}

func runInit(preset string) error {
	data, ok := config.Preset(preset)
	if !ok {
		return core.Usagef("unknown preset %q", preset)
	}
	if initV1Window {
		var err error
		if data, err = config.PresetWithV1Window(preset); err != nil {
			return core.Usagef("%v", err)
		}
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
	suffix := ""
	if initV1Window {
		suffix = " + v1 window"
	}
	fmt.Fprintf(out, "wrote %s (%s preset%s)\n", path, preset, suffix)
	return nil
}
