package cli

import (
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/spf13/cobra"
)

// This file holds the flag-grammar guards every command runs before it acts —
// what it means for a flag to have been GIVEN.
//
// pflag answers two different questions ("was it set?" via Changed, "what is
// its value?" via the bound variable) and every silent misread glyph has had
// came from a command asking one and acting on the other: `lint --stdin=false`
// satisfied a one-required group by Changed and then matched no VALUE arm, so a
// bad invocation was answered with the lint gate's own exit code; `--current=`
// was checked for emptiness rather than for having been given, so a workflow
// that templated an unset variable got a version nobody asked for, at exit 0,
// with nothing on stderr.
//
// The rule these guards enforce: a flag GIVEN with an empty value is usage (2).
// Every string flag glyph takes NAMES something — a revision range, a tag, a
// version, a repository, a sha, a path — and none has a meaningful empty form,
// so an empty one is the caller having named nothing. Falling back to the
// default the flag was passed to OVERRIDE is the silent ignore this house rule
// forbids, and for --current it prints a version, for --target it picks the
// commit a published tag will point at.

// checkGivenEmpty rejects a flag the caller GAVE with an empty value — what a
// workflow templating an unset variable emits (`--current=`, `--repo=`). noun
// names what the flag was supposed to name and hint is its escape, so the
// diagnostic says what to do rather than only what went wrong; the phrasing
// matches the guard that has always done this for --since-tag.
//
// A name with no such flag is a programmer error and panics rather than
// silently disabling the guard — a typo'd guard is this very bug wearing the
// costume of its own fix, and nothing else would ever report it.
func checkGivenEmpty(cmd *cobra.Command, name, noun, hint string) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		panic("checkGivenEmpty: " + cmd.Name() + " has no --" + name + " flag")
	}
	if !f.Changed || strings.TrimSpace(f.Value.String()) != "" {
		return nil
	}
	return core.Usagef("--%s= names an empty %s — %s", name, noun, hint)
}

// The hints for the flags more than one command takes, so the same input is
// diagnosed the same way whichever command received it.
const (
	repoHint    = "omit --repo to use $GITHUB_REPOSITORY, or name one with --repo=owner/name"
	currentHint = "omit --current to step from the default (the tag the walk named, else the highest v* tag), or name one with --current=vX.Y.Z"
)

// checkNamingFlags rejects the empty form of each named flag, before any git or
// API work. Flags that select the INPUT SOURCE (--range, --pr, --since-tag)
// keep their own guards: their complaint has to name the source, not just the
// flag.
func checkNamingFlags(cmd *cobra.Command, flags [][3]string) error {
	for _, f := range flags {
		if err := checkGivenEmpty(cmd, f[0], f[1], f[2]); err != nil {
			return err
		}
	}
	return nil
}
