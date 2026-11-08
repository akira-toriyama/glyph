package cli

import (
	"context"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// This file is the --range read lint, bump and notes share, and the count
// notes owes for its no-release reason. Which commits each of them reads, and
// how, is not decided here: the pattern file decides, inside bump.Fold /
// notes.Group / config.Lint, so every consumer applies identical rules by
// construction.

// participating counts the commits the fold reads — not an exclude_authors
// author, matched, not claimed by a skip pattern (DESIGN §4.1) — for notes'
// no-release reason, which says "N commit(s) participate" in bump's words and
// so must mean bump's set. notes has no fold of its own to count: notes.Group
// renders excluded authors, and the listing it is handed holds skips too
// (TestNotesNoReleaseCountsParticipatingCommits).
//
// The count is the fold's rows over the listing — one computation, bump's set
// by construction, its three conditions never restated here, where a second
// copy could drift from the rows bump publishes. The error return is the
// fold's contract, not a path a source takes: every source refused a message
// no pattern claims before notes was handed the listing (readSource).
func participating(cfg *config.Config, all []bump.Commit) (int, error) {
	rows, _, err := bump.Fold(all, cfg)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// logRange is every --range read — lint's, bump's and notes', single line and
// [[packages]] alike: the range's commits, and one question about the
// checkout. git lists only the commits a shallow clone holds, so a range that
// reaches past the shallow boundary is read in part, and a verdict over it
// reads exactly like a whole one. It WARNS and never refuses: a refusal
// would be a new semantics for lint, and a shallow walk is a warning to every
// reporting command and exit 4 to release alone (DESIGN §4.1, "Lint").
func logRange(ctx context.Context, revRange string) ([]gitsource.RawCommit, error) {
	// Trimmed once, here, where every --range reaches git: checkRangeFlag
	// judged the trimmed value, and ` v0.1.0..HEAD` is the range the caller
	// typed, not a name git should be asked about.
	revRange = strings.TrimSpace(revRange)
	if err := localWalker().CheckRange(ctx, revRange); err != nil {
		return nil, err
	}
	raws, err := checkout().Log(ctx, revRange)
	if err != nil {
		return nil, err
	}
	shallow, err := checkout().IsShallow(ctx)
	if err != nil {
		return nil, err
	}
	if shallow {
		warnf("this is a SHALLOW checkout: git lists only the commits this clone holds, so a range that reaches past its shallow boundary is read only as far as the clone goes — this verdict says nothing about the commits it cannot see. Fetch the full history (actions/checkout with fetch-depth: 0) for a verdict on the whole range")
	}
	return raws, nil
}

// checkRangeFlag rejects an empty or option-shaped --range before git runs —
// caller input, so usage, not an API failure. Judged TRIMMED, as logRange
// reads it: ` -x` reached git as a revision and answered "bad revision" at 4
// while `-x` was refused here (the --range twin of t-3dfg). Whether its
// endpoints name a commit is git's question, asked at the read
// (walk.Walker.CheckRange).
func checkRangeFlag(revRange string) error {
	revRange = strings.TrimSpace(revRange)
	if revRange == "" {
		return core.Usagef("--range needs a git revision range like BASE..HEAD")
	}
	if strings.HasPrefix(revRange, "-") {
		return core.Usagef("--range %q looks like an option, not a revision range", revRange)
	}
	return nil
}
