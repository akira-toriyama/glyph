package bump

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// LevelOf maps a sigil onto the bump lattice. The mapping is the
// sigil's definition (= none / ~ patch / ^ minor / ! major), fixed in the
// binary alongside the alphabet itself.
//
// Promote lands on major rather than on a rung of its own: a repository does
// not reach 1.0.0 over a change it considers compatible, and every reader of
// Level — note sections, preview's icon, pr-verdict's `breaking` — would
// answer a fifth word by quietly doing nothing. What makes '%' more than a
// major is carried in Decision.Promote, beside the lattice, not in it.
func LevelOf(s config.Sigil) Level {
	switch s {
	case config.SigilPatch:
		return LevelPatch
	case config.SigilMinor:
		return LevelMinor
	case config.SigilMajor, config.SigilPromote:
		return LevelMajor
	case config.SigilNone:
		return LevelNone
	}
	return LevelNone
}

// Commit is the slice of a commit the fold reads: nothing is parsed,
// so there is no grammar struct — the pattern file decides what the message
// means. Author carries whatever identity fact the walk resolved; the fold
// compares it literally against exclude_authors.
type Commit struct {
	SHA     string
	Author  string
	Message string
}

// Row is one folded commit in the machine verdict: the sigil
// the message carried (or the pattern supplied) and the level it folds to.
// Excluded authors and skip-pattern commits have no row — they are not in
// the fold. Warn is the winning pattern's warn message (config.Pattern.Warn),
// on the row so the machine surface names the warned commits and the CLI can
// surface each one — a warned pattern must be loud wherever it wins a
// verdict.
type Row struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Sigil   string `json:"sigil"`
	Level   string `json:"level"`
	Warn    string `json:"warn,omitempty"`
}

// Refusal is one finding about a commit or a message: every row of a
// machine error's details that names one (doctor's details are check ids),
// and lint's warned rows, share this shape; SHA is empty for a message that
// is no commit yet (lint --message, a pull request's title). The fold walks
// the WHOLE range before its refusal goes out: stopping at the first
// unreadable commit answered a three-commit cleanup with three separate red
// runs (measured — the caller re-ran lint in a loop to find the rest).
type Refusal struct {
	SHA     string `json:"sha,omitempty"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Fold computes the release level of a commit range: each commit's sigil via
// cfg.Match, folded with max over the lattice (the same order-independent
// reduce as always, so squash order can never move the version). It returns
// the per-commit rows beside the fold — one verdict computation, so a listing
// can never disagree with the level it came from.
// An empty range folds to none — no release; rows is non-nil so the JSON
// surface serializes [].
//
// A '%' anywhere in the range also sets Decision.Promote, OR-folded, so that
// half is order-independent too: one commit asking for 1.0.0 is enough, and
// asking twice is the same as asking once.
//
// Two ways a commit stays out of the fold, checked in this order: an
// exclude_authors author is dropped before its message is ever matched — the
// key exists for bots, whose messages are exactly the ones the patterns do
// not describe, so exclusion must not depend on a match — and a matching
// pattern with skip = true drops the commit the same way.
//
// A non-excluded commit that no pattern claims — or that an unlandable
// pattern claims, which Match reports the same way — refuses the WHOLE range
// (ratified Q2): the alternative — quietly folding it as none — is a commit
// that stops existing for versioning the moment someone's regex misses it.
// The refusal is the lint class (exit 3): the range holds a message the
// repository's own convention cannot read, which is the same verdict lint
// would hand that message.
func Fold(commits []Commit, cfg *config.Config) ([]Row, Decision, error) {
	rows := []Row{}
	var promote bool
	levels := make([]Level, 0, len(commits))
	var refusals []Refusal
	firstUnlandable := false
	for _, c := range commits {
		if slices.Contains(cfg.ExcludeAuthors, c.Author) {
			continue
		}
		m, err := cfg.Match(c.Message)
		if err != nil {
			refusals = append(refusals, Refusal{SHA: c.SHA, Subject: FirstLine(c.Message), Detail: err.Error()})
			continue
		}
		if !m.Matched {
			if len(refusals) == 0 {
				firstUnlandable = m.Unlandable != ""
			}
			refusals = append(refusals, Refusal{SHA: c.SHA, Subject: FirstLine(c.Message), Detail: cfg.UnclaimedDetail(m)})
			continue
		}
		if m.Skip {
			continue
		}
		level := LevelOf(m.Sigil)
		promote = promote || m.Sigil == config.SigilPromote
		levels = append(levels, level)
		rows = append(rows, Row{
			SHA:     c.SHA,
			Subject: FirstLine(c.Message),
			Sigil:   m.Sigil.String(),
			Level:   string(level),
			Warn:    m.Warn,
		})
	}
	if len(refusals) > 0 {
		// The unmatched remedy list is wrong for an unlandable message: a
		// pattern DID claim it, and adding one or excluding the author is how
		// the refusal would be defeated. Its reason says what must happen
		// before the commit lands, and --range over a published branch can
		// hold one already landed, where only a range starting past it gets
		// out. (The release walk refuses its own commits before the fold, with
		// the wedge's escape — cli.unclaimed, cli.walkRefusal.)
		remedy := " (an unmatched commit folded as none would be a silent hole — fix the message, add a pattern, or exclude the author)"
		if firstUnlandable {
			remedy = " (rewrite it as its reason says before it lands; a commit already on a published branch cannot be rewritten, so the range must start past it)"
		}
		msg := fmt.Sprintf("commit %.7s: %s; refusing to version the range%s", refusals[0].SHA, refusals[0].Detail, remedy)
		if extra := len(refusals) - 1; extra > 0 {
			msg = fmt.Sprintf("commit %.7s: %s (+%d more violation(s), all in details); refusing to version the range", refusals[0].SHA, refusals[0].Detail, extra)
		}
		return nil, Decision{}, core.Lintf("%s", msg).WithDetails(refusals)
	}
	return rows, Decision{Level: reduce(levels), Promote: promote}, nil
}

// FirstLine returns a raw message's subject line (CR trimmed) — the one
// subject definition every consumer (fold refusals, lint findings, the notes
// fallback) shares, so a CRLF message cannot mean different things per caller.
func FirstLine(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSuffix(message, "\r")
}

// DecidingReason names the oldest commit that reaches the folded level — the
// one-line answer to "why this bump".
//
// A promoted range names its oldest '%' commit instead, even though promote
// and major share a level. Otherwise the reason for landing on 1.0.0 would
// point at whichever breaking commit came first, and the one commit that
// actually chose the version would not appear in the answer at all.
func DecidingReason(commits []Row, dec Decision) string {
	if dec.Promote {
		for _, c := range commits {
			if c.Sigil == config.SigilPromote.String() {
				return fmt.Sprintf("%.7s %s %q → promote", c.SHA, c.Sigil, c.Subject)
			}
		}
	}
	for _, c := range commits {
		if c.Level == string(dec.Level) {
			return fmt.Sprintf("%.7s %s %q → %s", c.SHA, c.Sigil, c.Subject, dec.Level)
		}
	}
	return fmt.Sprintf("level %s", dec.Level)
}
