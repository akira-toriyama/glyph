package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
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
		Short: "Write a glyph.toml preset at the checkout's top level",
		Long: "init writes glyph.toml — the v2 configuration in which YOUR regex patterns\n" +
			"decide the commit grammar and the named group semver_sigil (= none / ~ patch /\n" +
			"^ minor / ! major / % promote to 1.0.0) is the only input to version\n" +
			"calculation.\n\n" +
			"The file lands at the top level of the git checkout init runs in, from any\n" +
			"subdirectory — the one glyph.toml every other command reads — and in the\n" +
			"current directory only outside a checkout.\n\n" +
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
			return runInit(cmd.Context(), chosen)
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

func runInit(ctx context.Context, preset string) error {
	data, ok := config.Preset(preset)
	if !ok {
		return core.Usagef("unknown preset %q", preset)
	}
	path, err := initPath(ctx)
	if err != nil {
		return err
	}
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

// initPath is where init writes: the glyph.toml at the checkout's top level,
// the one file loadConfig resolves for every other command from any
// subdirectory. Writing the current directory instead left, from a
// subdirectory, a file nothing reads at exit 0 while loadConfig's remedy sent
// the author back to the same command (t-f2cb, measured 2026-09-29). Only
// where git names no top level — outside a checkout, or with no git at all —
// is the current directory the answer; an interrupt is the user's abort,
// never a reason to write somewhere else.
func initPath(ctx context.Context) (string, error) {
	top, err := gitsource.TopLevel(ctx, ".")
	switch {
	case core.IsInterrupted(err):
		return "", err
	case err != nil:
		return "glyph.toml", nil
	}
	return filepath.Join(top, "glyph.toml"), nil
}
