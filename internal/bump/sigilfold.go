package bump

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
)

// SigilLevel maps a v2 sigil onto the bump lattice. The mapping is the
// sigil's definition (= none / ~ patch / ^ minor / ! major), fixed in the
// binary alongside the alphabet itself.
func SigilLevel(s config.Sigil) Level {
	switch s {
	case config.SigilPatch:
		return LevelPatch
	case config.SigilMinor:
		return LevelMinor
	case config.SigilMajor:
		return LevelMajor
	case config.SigilNone:
		return LevelNone
	}
	return LevelNone
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

// SigilVerdict is one folded commit's row in the machine verdict: the sigil
// the message carried (or the pattern supplied) and the level it folds to.
// Excluded authors and skip-pattern commits have no row — they are not in
// the fold.
type SigilVerdict struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Sigil   string `json:"sigil"`
	Level   string `json:"level"`
}

// FoldSigils computes the release level of a commit range under a v2 config:
// each commit's sigil via cfg.Match, folded with max over the lattice (the
// same order-independent Reduce as always, so squash order can never move
// the version). It returns the per-commit rows beside the fold — one verdict
// computation, so a listing can never disagree with the level it came from.
// An empty range folds to none — no release; verdicts is non-nil so the JSON
// surface serializes [].
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
// Refusal is one commit the fold refused, in the machine error's details.
// The WHOLE range is walked before the refusal goes out: stopping at the
// first unreadable commit answered a three-commit cleanup with three
// separate red runs (measured, the incident behind v1's accumulating walk —
// the caller re-ran lint in a loop to find the rest).
type Refusal struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

func FoldSigils(commits []SigilCommit, cfg *config.Config) ([]SigilVerdict, Level, error) {
	verdicts := []SigilVerdict{}
	levels := make([]Level, 0, len(commits))
	var refusals []Refusal
	for _, c := range commits {
		if slices.Contains(cfg.ExcludeAuthors, c.Author) {
			continue
		}
		m, err := cfg.Match(c.Message)
		if err != nil {
			refusals = append(refusals, Refusal{SHA: c.SHA, Subject: firstLine(c.Message), Detail: err.Error()})
			continue
		}
		if !m.Matched {
			refusals = append(refusals, Refusal{SHA: c.SHA, Subject: firstLine(c.Message), Detail: fmt.Sprintf("matches none of the %d configured patterns", len(cfg.Patterns))})
			continue
		}
		if m.Skip {
			continue
		}
		level := SigilLevel(m.Sigil)
		levels = append(levels, level)
		verdicts = append(verdicts, SigilVerdict{
			SHA:     c.SHA,
			Subject: firstLine(c.Message),
			Sigil:   m.Sigil.String(),
			Level:   string(level),
		})
	}
	if len(refusals) > 0 {
		msg := fmt.Sprintf("commit %.7s: %s; refusing to version the range (an unmatched commit folded as none would be a silent hole — fix the message, add a pattern, or exclude the author)", refusals[0].SHA, refusals[0].Detail)
		if extra := len(refusals) - 1; extra > 0 {
			msg = fmt.Sprintf("commit %.7s: %s (+%d more violation(s), all in details); refusing to version the range", refusals[0].SHA, refusals[0].Detail, extra)
		}
		return nil, "", &core.Error{Code: core.CodeLint, Msg: msg, Details: refusals}
	}
	return verdicts, Reduce(levels), nil
}

// firstLine returns a raw message's subject line (CR trimmed).
func firstLine(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSuffix(message, "\r")
}
