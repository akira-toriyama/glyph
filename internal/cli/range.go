package cli

import (
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/notes"
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
		out = append(out, notes.SigilCommit{SHA: r.SHA, Pull: pull, Author: r.Author, Message: r.Message})
	}
	return out
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

// firstLine returns the subject line of a raw message (CR trimmed).
func firstLine(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSuffix(message, "\r")
}
