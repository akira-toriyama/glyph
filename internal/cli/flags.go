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

// currentFlagUsage is the --help line for --current, shared by bump and release
// because they share currentVersion and therefore share the default.
//
// Naming the default is the whole job, and stating it in each command's own
// string got it wrong: bump's read "highest parseable v* tag, else v0.0.0",
// omitting the arm that runs FIRST. currentVersion prefers the base the input
// source named, so under --since-tag=TAG the step base is TAG — measured on a
// repo tagged v0.1.0 and v0.5.0 at the same commit, where
// `bump --since-tag=v0.1.0 --json` answers current v0.1.0, not the v0.5.0 the
// help promised. That is the version a wrong-bump investigation starts from,
// and --help is where the investigator looks, so the one place the CLI could
// mislead is the one place it did.
const currentFlagUsage = "the version to step from (default: the tag the walk named, else the highest parseable v* tag, else v0.0.0)"

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

// The same Changed-versus-VALUE split, one flag kind over: a BOOLEAN group.
//
// cobra's MarkFlagsMutuallyExclusive groups on Changed, so it reads
// `--json=false --md` as both flags being set and refuses the invocation with
// "[json md] were all set" — a false statement, in the machine-readable envelope
// other repositories parse, about an invocation that asks for one format and
// explicitly declines the other. And the mirror case goes the other way:
// `--md=false` is not in the group's way at all, so nothing looks at it, and the
// caller who said "not Markdown" is handed Markdown at exit 0. That is the
// silent ignore this file exists to forbid, wearing a bool.
//
// Both guards therefore ask what the flags ARE, not whether they were mentioned.

// turnedOn returns the members of a boolean group the caller actually set to
// true. A flag given =false is not selecting anything and is not a conflict.
func turnedOn(cmd *cobra.Command, names ...string) []string {
	var on []string
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil {
			panic("turnedOn: " + cmd.Name() + " has no --" + n + " flag")
		}
		if f.Value.String() == "true" {
			on = append(on, "--"+n)
		}
	}
	return on
}

// checkExclusiveBool rejects a boolean group with more than one member turned
// ON, and names the ones that are rather than the ones that were mentioned.
func checkExclusiveBool(cmd *cobra.Command, names ...string) error {
	if on := turnedOn(cmd, names...); len(on) > 1 {
		return core.Usagef("%s cannot be combined — they select different behaviour", strings.Join(on, " and "))
	}
	return nil
}

// checkDefaultModeOff rejects the default-bearing member of such a group being
// turned explicitly OFF while nothing else is on.
//
// A mode flag whose default is the command's default behaviour has no
// meaningful false: --md=false says "not the Markdown table", and with no other
// format selected that names no output at all. Honouring it as "the default,
// then" is the silent ignore; falling through to Markdown is what glyph did.
func checkDefaultModeOff(cmd *cobra.Command, name, hint string, alternatives ...string) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		panic("checkDefaultModeOff: " + cmd.Name() + " has no --" + name + " flag")
	}
	if !f.Changed || f.Value.String() == "true" {
		return nil
	}
	if len(turnedOn(cmd, alternatives...)) > 0 {
		// Coherent: the caller declined the default and chose something else.
		return nil
	}
	return core.Usagef("--%s=false selects nothing — %s", name, hint)
}
