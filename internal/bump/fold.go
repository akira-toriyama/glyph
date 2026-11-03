package bump

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// LevelOf maps a sigil onto the bump lattice. The mapping is the
// sigil's definition (= none / ~ patch / ^ minor / ! major), fixed in the
// binary alongside the alphabet itself.
//
// Promote lands on major rather than on a rung of its own: a repository does
// not reach 1.0.0 over a change it considers compatible, and every reader of
// Level — note sections, preview's icon, pr-verdict's `breaking` — would
// answer a fifth word by quietly doing nothing. What makes '%' more than a
// major is carried in Decision.promote, beside the lattice, not in it.
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
// the fold. Warn is the winning pattern's warn message (config.Pattern.warn),
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
// A '%' anywhere in the range also sets Decision.promote, OR-folded, so that
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
// (ratified Q2; Judge): the alternative — quietly folding it as none — is a
// commit that stops existing for versioning the moment someone's regex misses
// it. The refusal is the lint class (exit 3), worded by RangeRefusal: the
// range holds a message the repository's own convention cannot read, which is
// the same verdict lint would hand that message.
func Fold(commits []Commit, cfg *config.Config) ([]Row, Decision, error) {
	rows := []Row{}
	var promote bool
	levels := make([]Level, 0, len(commits))
	var refusals []Refusal
	remedy := ""
	for _, c := range commits {
		if slices.Contains(cfg.ExcludeAuthors, c.Author) {
			continue
		}
		m, u := Judge(cfg, c.SHA, c.Message)
		if u != nil {
			if len(refusals) == 0 {
				remedy = u.Remedy
			}
			refusals = append(refusals, u.Row)
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
		return nil, Decision{}, RangeRefusal(refusals, remedy)
	}
	return rows, Decision{Level: reduce(levels), promote: promote}, nil
}

// Unclaimed is a message the pattern file refuses, as Judge reads it: the
// refusal's details row, and the clause RangeRefusal closes the sentence on
// when this row is the range's only one.
type Unclaimed struct {
	Row    Refusal
	Remedy string
}

// The two remedies a range's refusal of a message closes on. The unmatched
// list is wrong for an unlandable message: a pattern DID claim it, and adding
// one or excluding the author is how the refusal would be defeated, so its
// remedy says what must happen before the commit lands — and a --range over a
// published branch can hold one already landed, where only a range starting
// past it gets out. The release walk words its own refusal with the wedge's
// escape instead (internal/walk's walkRefusal).
const (
	unmatchedRemedy  = " (an unmatched commit folded as none would be a silent hole — fix the message, add a pattern, or exclude the author)"
	unlandableRemedy = " (rewrite it as its reason says before it lands; a commit already on a published branch cannot be rewritten, so the range must start past it)"
)

// Judge is the one step from a commit's message to its refusal row: m is
// cfg.Match's answer, and u is non-nil when the message yields no sigil, or
// no pattern claims it — an unlandable claim included, which Match reports
// the same way. A skip pattern is a claim. Every reader that refuses such a
// message with a details row asks it (Fold, the walk's unclaimed,
// walk.PlaceOf); lint's finding is Config.Lint's own. Whether a commit is
// judged at all is the caller's gate, never Judge's: an exclude_authors
// author is not handed here (DESIGN §3).
func Judge(cfg *config.Config, sha, message string) (config.Match, *Unclaimed) {
	m, err := cfg.Match(message)
	switch {
	case err != nil:
		return m, &Unclaimed{Row: Refusal{SHA: sha, Subject: FirstLine(message), Detail: err.Error()}, Remedy: unmatchedRemedy}
	case !m.Matched:
		remedy := unmatchedRemedy
		if m.Unlandable != "" {
			remedy = unlandableRemedy
		}
		return m, &Unclaimed{Row: Refusal{SHA: sha, Subject: FirstLine(message), Detail: cfg.UnclaimedDetail(m)}, Remedy: remedy}
	}
	return m, nil
}

// RangeRefusal is the one wording of a refusal to version a range whose
// commits the caller may still rewrite — Fold's, and walk.Pending's, which
// loads the grammar's rows (Judge) and attribution's into it in the order the
// commits come: the first row's sentence, every row in details, and the count
// of the rest when there is more than one. remedy closes a lone row's
// sentence — Unclaimed.Remedy for the grammar's; attribution's sentence names
// its own escapes, so it passes "" — and with several rows each one's detail
// says its own.
func RangeRefusal(rows []Refusal, remedy string) error {
	msg := fmt.Sprintf("commit %.7s: %s; refusing to version the range%s", rows[0].SHA, rows[0].Detail, remedy)
	if extra := len(rows) - 1; extra > 0 {
		msg = fmt.Sprintf("commit %.7s: %s (+%d more violation(s), all in details); refusing to version the range", rows[0].SHA, rows[0].Detail, extra)
	}
	return core.Lintf("%s", msg).WithDetails(rows)
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
	if dec.promote {
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
