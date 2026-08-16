package bump

import (
	"slices"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// SigilLevel maps a v2 sigil onto the bump lattice. The mapping is the
// sigil's definition (= none / ~ patch / ^ minor / ! major), fixed in the
// binary alongside the alphabet itself.
func SigilLevel(s config.Sigil) gitmoji.Bump {
	switch s {
	case config.SigilPatch:
		return gitmoji.BumpPatch
	case config.SigilMinor:
		return gitmoji.BumpMinor
	case config.SigilMajor:
		return gitmoji.BumpMajor
	case config.SigilNone:
		return gitmoji.BumpNone
	}
	return gitmoji.BumpNone
}

// SigilCommit is the slice of a commit the v2 fold reads: nothing is parsed,
// so there is no grammar struct — the pattern file decides what the message
// means. Author carries whatever identity fact the walk resolved; the fold
// compares it literally against exclude_authors.
type SigilCommit struct {
	SHA     string
	Author  string
	Message string
}

// FoldSigils computes the release level of a commit range under a v2 config:
// each commit's sigil via cfg.Match, folded with max over the lattice (the
// same order-independent Reduce the v1 walk uses, so squash order can never
// move the version). An empty range folds to none — no release.
//
// Two ways a commit stays out of the fold, checked in this order: an
// exclude_authors author is dropped before its message is ever matched — the
// key exists for bots, whose messages are exactly the ones the patterns do
// not describe, so exclusion must not depend on a match — and a matching
// pattern with skip = true drops the commit the same way.
//
// A non-excluded commit that no pattern claims refuses the WHOLE range
// (ratified Q2): the alternative — quietly folding it as none — is a commit
// that stops existing for versioning the moment someone's regex misses it,
// which is the silent hole v2 exists to close. The refusal is the lint class
// (exit 3): the range holds a message the repository's own convention cannot
// read, which is the same verdict lint would hand that message.
func FoldSigils(commits []SigilCommit, cfg *config.Config) (gitmoji.Bump, error) {
	levels := make([]gitmoji.Bump, 0, len(commits))
	for _, c := range commits {
		if slices.Contains(cfg.ExcludeAuthors, c.Author) {
			continue
		}
		m, err := cfg.Match(c.Message)
		if err != nil {
			return "", core.Lintf("commit %s: %v", c.SHA, err)
		}
		if !m.Matched {
			return "", core.Lintf("commit %s matches none of the %d configured patterns; refusing to version the range (an unmatched commit folded as none would be a silent hole — fix the message, add a pattern, or exclude the author)", c.SHA, len(cfg.Patterns))
		}
		if m.Skip {
			continue
		}
		levels = append(levels, SigilLevel(m.Sigil))
	}
	return Reduce(levels), nil
}
