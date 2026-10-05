package cli

import (
	"context"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/github"
	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
	"github.com/akira-toriyama/glyph/v4/internal/notes"
)

// This file is the shared input plumbing that turns raw commits — from git or
// the API, both spelled gitsource.RawCommit — into the shapes the v2 engine
// reads. Which commits participate, and how, is no longer decided here: the
// pattern file decides, inside bump.FoldSigils / notes.GroupSigils /
// config.Lint, so every consumer applies identical rules by construction.

// sigilCommits adapts raw commits for the fold.
func sigilCommits(raws []gitsource.RawCommit) []bump.SigilCommit {
	out := make([]bump.SigilCommit, 0, len(raws))
	for _, r := range raws {
		out = append(out, bump.SigilCommit{SHA: r.SHA, Author: r.Author, Message: r.Message})
	}
	return out
}

// noteCommits adapts raw commits for the notes, all citing one pull (the
// --pr input) or none (a local range, n = 0).
func noteCommits(raws []gitsource.RawCommit, pull int) []notes.SigilCommit {
	out := make([]notes.SigilCommit, 0, len(raws))
	for _, r := range raws {
		out = append(out, notes.SigilCommit{SHA: r.SHA, Pull: pull, Author: r.Author, Login: identity(r), Message: r.Message})
	}
	return out
}

// identity is the GitHub login the notes may credit a commit's author BY:
// the login GitHub itself mapped the commit to when the API described it,
// else the login a noreply author address carries, else "" — and "" is an
// answer, never the author name: a name is free text and @<name> pages
// whoever happens to own it (t-39fy).
func identity(r gitsource.RawCommit) string {
	if r.Login != "" {
		return r.Login
	}
	return github.LoginFromNoreply(r.Email)
}

// logRange is every --range read — lint's, bump's and notes', single line and
// [[packages]] alike: the range's commits, and one question about the
// checkout. git lists only the commits a shallow clone holds, so a range that
// reaches past the shallow boundary is read in part, and a verdict over it
// reads exactly like a whole one — measured on a --depth 2 clone, lint judged 2
// of 6 commits and bump printed a version, both at 0 with nothing said, where
// the full clone refuses at 3 (t-esm5). It WARNS and never refuses: a refusal
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
