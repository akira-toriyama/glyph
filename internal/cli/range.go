package cli

import (
	"context"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/akira-toriyama/glyph/v6/internal/notes"
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
// renders excluded authors and messages no pattern claims, and the listing it
// is handed holds skips too (TestNotesNoReleaseCountsParticipatingCommits).
//
// The fold itself is asked, one commit at a time, rather than its three
// conditions restated here: a second copy could drift from the rows bump
// publishes, and over the whole listing the fold refuses at the first message
// no pattern claims, where notes renders such a range and still owes a count
// of the rest.
func participating(cfg *config.Config, commits []notes.Commit) int {
	n := 0
	for _, c := range commits {
		rows, _, err := bump.Fold([]bump.Commit{{SHA: c.SHA, Author: c.Author, Message: c.Message}}, cfg)
		if err == nil {
			n += len(rows)
		}
	}
	return n
}

// logRange is every --range read — lint's, bump's and notes', single line and
// [[packages]] alike: the range's commits, and one question about the
// checkout. git lists only the commits a shallow clone holds, so a range that
// reaches past the shallow boundary is read in part, and a verdict over it
// reads exactly like a whole one. It WARNS and never refuses: a refusal
// would be a new semantics for lint, and a shallow walk is a warning to every
// reporting command and exit 4 to release alone (DESIGN §4.1, "Lint").
func logRange(ctx context.Context, revRange string) ([]gitsource.RawCommit, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, err
	}
	shallow, err := gitsource.IsShallow(ctx, ".")
	if err != nil {
		return nil, err
	}
	if shallow {
		warnf("this is a SHALLOW checkout: git lists only the commits this clone holds, so a range that reaches past its shallow boundary is read only as far as the clone goes — this verdict says nothing about the commits it cannot see. Fetch the full history (actions/checkout with fetch-depth: 0) for a verdict on the whole range")
	}
	return raws, nil
}

// checkRangeFlag rejects an empty or option-shaped --range before git runs —
// caller input, so usage, not an API failure.
func checkRangeFlag(revRange string) error {
	if strings.TrimSpace(revRange) == "" {
		return core.Usagef("--range needs a git revision range like BASE..HEAD")
	}
	if strings.HasPrefix(revRange, "-") {
		return core.Usagef("--range %q looks like an option, not a revision range", revRange)
	}
	return nil
}
