package cli

import (
	"strings"

	"github.com/akira-toriyama/glyph/v3/internal/bump"
	"github.com/akira-toriyama/glyph/v3/internal/core"
	"github.com/akira-toriyama/glyph/v3/internal/github"
	"github.com/akira-toriyama/glyph/v3/internal/gitsource"
	"github.com/akira-toriyama/glyph/v3/internal/notes"
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
