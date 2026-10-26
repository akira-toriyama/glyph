package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/spf13/cobra"
)

// initOptions is what one init run was asked, snapshotted at RunE: the one
// preset the flags chose, and --force. Nothing below RunE reads a flag —
// runInit is a function of these alone (TestInitRunsFromOptionsWithoutCobra).
type initOptions struct {
	preset string
	force  bool
}

func newInitCmd() *cobra.Command {
	names := config.PresetNames()
	// One bool flag per shipped preset, discovered from the embedded set — a
	// preset added to internal/config/presets/ becomes a flag with no edit
	// here, so the command and the shipped artifacts cannot disagree about
	// what exists.
	presetFlags := map[string]*bool{}
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a glyph.toml preset at the checkout's top level",
		Long: "init writes glyph.toml — the configuration in which YOUR regex patterns\n" +
			"decide the commit grammar and the named group semver_sigil (= none / ~ patch /\n" +
			"^ minor / ! major / % promote to 1.0.0) is the only input to version\n" +
			"calculation.\n\n" +
			"The file lands at the top level of the git checkout init runs in, from any\n" +
			"subdirectory — the one glyph.toml every other command reads — and in the\n" +
			"current directory only where git says there is no repository at all. Inside\n" +
			"a git directory (.git, a bare repository), which has no work tree, init\n" +
			"refuses (exit 2); where git cannot read the repository (dubious ownership,\n" +
			"a broken config, no git on PATH) it writes nothing and exits 4 with the\n" +
			"error it hit.\n\n" +
			"One preset flag is required (--" + strings.Join(names, ", --") + "); the file it\n" +
			"writes is a starting point to edit, not a contract to keep. An existing\n" +
			"glyph.toml is never touched without --force, and --force replaces only a\n" +
			"regular file: a directory, a named pipe or a broken symbolic link at the\n" +
			"path is refused either way (exit 2) — remove it, then re-run init.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkExclusiveBool(cmd, names...); err != nil {
				return err
			}
			chosen := ""
			for _, name := range names {
				if *presetFlags[name] {
					chosen = name
				}
			}
			if chosen == "" {
				return core.Usagef("pick a preset: --%s", strings.Join(names, " or --"))
			}
			return runInit(cmd.Context(), initOptions{preset: chosen, force: force})
		},
	}
	for _, name := range names {
		v := new(bool)
		presetFlags[name] = v
		cmd.Flags().BoolVar(v, name, false, "write the "+name+" preset")
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing glyph.toml (a regular file only)")
	return cmd
}

func runInit(ctx context.Context, o initOptions) error {
	data, ok := config.Preset(o.preset)
	if !ok {
		return core.Usagef("unknown preset %q", o.preset)
	}
	path, err := initPath(ctx)
	if err != nil {
		return err
	}
	// What is at the path is config.StatFile's answer, the one every verdict
	// command's loader gets, and only a regular file is ever replaced: the
	// write is os.WriteFile, which blocks on a named pipe until a reader
	// appears, fails on a directory and follows a dangling link to create its
	// target, so anything else is refused, --force or not (DESIGN §5).
	serr := config.StatFile(path)
	_, isNotAFile := errors.AsType[*config.NotAFileError](serr)
	switch {
	case serr == nil:
		if !o.force {
			return core.Usagef("%s already exists — edit it in place, or pass --force to replace it with the %s preset", path, o.preset)
		}
	case isNotAFile:
		return core.Usagef("%v — init replaces only a regular file, even with --force; remove it, then re-run glyph init --%s", serr, o.preset)
	case !errors.Is(serr, fs.ErrNotExist):
		return core.APIf("%v — init could not tell what is there, so it wrote nothing", serr)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return core.APIf("%v — glyph init could not write the preset", err)
	}
	fmt.Fprintf(out, "wrote %s (%s preset)\n", path, o.preset)
	return nil
}

// initPath is where init writes: the glyph.toml at the checkout's top level,
// the one file loadConfig resolves for every other command from any
// subdirectory. Only where git itself says "not a git repository" is the
// current directory the answer. Inside a git directory git names no top level
// either, and that case is refused at 2. Any other git failure — a repository
// git will not read (dubious ownership, a broken config), or no git at all —
// is no trustworthy answer: exit 4 with git's message and nothing written. An
// interrupt in either read is the user's abort, never a reason to write
// somewhere else. Why each arm: DESIGN §2, "Config resolution is one file per
// checkout".
func initPath(ctx context.Context) (string, error) {
	top, err := gitsource.TopLevel(ctx, ".")
	switch {
	case core.IsInterrupted(err):
		return "", err
	case err == nil:
		return configPathAt(top), nil
	}
	inGitDir, gerr := gitsource.InsideGitDir(ctx, ".")
	switch {
	case errors.Is(gerr, gitsource.ErrNoRepository):
		return configPathAt("."), nil
	case gerr != nil:
		return "", gerr
	case inGitDir:
		return "", core.Usagef("this directory is inside a git directory, which has no work tree — run glyph init from the checkout's work tree, where every command reads glyph.toml")
	}
	// git reads a repository here and calls this its work tree, yet named no
	// top level: its own failure is the answer.
	return "", err
}
