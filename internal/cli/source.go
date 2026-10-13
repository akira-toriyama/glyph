package cli

import (
	"context"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/spf13/cobra"
)

// This file is the input-source dispatch bump and notes share: which of
// --range / --pr / --since-tag a command was GIVEN, read once at RunE so
// nothing below it asks cobra, and the one read that turns the named source
// into the shape every verdict loop reads (sinceTagWalk, lines.go). A
// repository that declares no [[packages]] comes back as one bare line
// holding every commit, so a command has one per-line loop and no second
// arm, and the render alone tells the single line from the lines.

// sourceFlags are the input-source flags bump and notes bind: exactly one
// of the three sources (markInputSourceFlags) and --repo for the two that
// query the API. Each command binds its own value, never a package
// variable: a command's state lives in its cobra.Command, and the verdict
// below RunE is a function of what sourceOf read.
type sourceFlags struct {
	rng  string
	pull int
	tag  string
	repo string
}

// sourceKind is which of the three sources was given.
type sourceKind int

const (
	sourceRange sourceKind = iota
	sourcePull
	sourceWalk
)

// source is the input a verdict is computed over: which flag was given, and
// the values. Built by sourceOf at RunE, read by readSource.
type source struct {
	kind sourceKind
	sourceFlags
}

// sourceOf snapshots the source the caller named. It dispatches on whether a
// flag was GIVEN, not on its value — so an explicit --pr 0 (what a workflow
// yields from a null PR number) reaches the --pr guard and is diagnosed as a
// bad --pr, not misrouted into a --range complaint. markInputSourceFlags has
// required exactly one of the three, so the arm that is neither is --range.
func sourceOf(cmd *cobra.Command, f sourceFlags) source {
	src := source{kind: sourceRange, sourceFlags: f}
	switch {
	case cmd.Flags().Changed("pr"):
		src.kind = sourcePull
	case cmd.Flags().Changed("since-tag"):
		src.kind = sourceWalk
	}
	return src
}

// readSource reads the commits a verdict is computed over from the source
// the caller named, as one walk result: the release walk itself
// (sinceTagInput); a local range, every commit a landed identity pending on
// every line (rangeLines); or a pull request's individual commits, cited by
// the pull and accepted only where no [[packages]] are declared (pullLines).
// Each source's grammar is checked here or in the read, before git or the
// API runs, as the caller's input (usage, 2).
//
// The walk's facts come back with the commits and bump and notes discard
// them, for the same reason: both REPORT, they do not act. An incomplete
// walk is already a ::warning:: per cause on stderr — the channel the
// operator and the Actions annotation layer both read — and nothing here
// writes back, so there is no irreversible act to gate. release acts on its
// verdict irreversibly and refuses at 4 on them; preview's answer is pasted
// into a pull request and read by someone who never opens the log, so it
// carries the shortfall in the body itself.
func readSource(ctx context.Context, cfg *config.Config, src source) (sinceTagWalk, error) {
	switch src.kind {
	case sourcePull:
		return pullLines(ctx, cfg, src.pull, src.repo)
	case sourceWalk:
		return sinceTagInput(ctx, cfg, src.tag, src.repo)
	default:
		if err := checkRangeFlag(src.rng); err != nil {
			return sinceTagWalk{}, err
		}
		return rangeLines(ctx, cfg, src.rng)
	}
}
