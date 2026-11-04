package config

import (
	"fmt"
	"regexp"
)

// Match is the outcome of running one commit message through the patterns.
// Exactly one of three shapes comes back: Matched=false (no pattern claimed
// the message — the caller decides what that refusal means: lint calls it a
// violation, bump refuses the range), Skip=true (a pattern claimed it and
// drops it from all processing), or a Sigil with the winning pattern's named
// groups.
//
// A message an unlandable pattern claims comes back in the FIRST shape, with
// Unlandable carrying why. That is deliberate: every consumer already refuses
// an unclaimed message wherever a commit exists (the fold, lint, the release
// walk's wedge, packages placement) and already puts exclude_authors ahead
// of it, so a consumer that never reads the field still fails closed, and the
// notes refuse it on every source as they refuse any message no pattern
// claims (bump.RangeRefusal over --range and --pr, the walk's wedge over
// --since-tag); only an exclude_authors author's message, never judged,
// renders through the raw-line fallback. Only LintAuthoring reads the field
// to let the message through.
type Match struct {
	// Matched is false when no pattern's regex matched the message, or when
	// the first one that did is unlandable. Sigil, Skip, Groups and
	// PatternIndex mean nothing in that case.
	Matched bool
	// Unlandable is set only on an unmatched shape: the sentence naming the
	// unlandable pattern that claimed the message and the file author's
	// reason. "" when no pattern claimed it at all. UnclaimedDetail is the
	// one reader history gates use.
	Unlandable string
	// Skip is true when the winning pattern declares skip = true: the commit
	// leaves lint, bump and notes entirely and Sigil means nothing.
	Skip bool
	// Sigil is the extracted version signal — the message's semver_sigil
	// capture when it produced one, else the pattern's fixed value.
	Sigil Sigil
	// Groups holds every named group the winning pattern defines, mapped to
	// what it captured (possibly ""). A name the pattern gives to more than
	// one group — one per alternative, `(?P<scope>…)|…(?P<scope>…)` — maps to
	// the capture of the group that took part in the match (participant): a
	// group whose alternative was not taken never overwrites one that was.
	// The notes line template's $xxx placeholders read from here.
	Groups map[string]string
	// Warn is the winning pattern's warn message, "" when it declares none.
	// The verdict is unchanged — a warned match is still a match — but every
	// verdict computation that reads this Match must surface the message
	// (that invariant is what makes a warned pattern's hole visible for as
	// long as the pattern lives; see Pattern.warn).
	Warn string
	// PatternIndex is the winning pattern's position in Config.Patterns, -1
	// when Matched is false.
	PatternIndex int
}

// Match runs the message through the patterns in file order and stops at the
// first regex that matches — first match wins, deliberately unlike
// semantic-release's highest-level-wins, because "the first rule that claims
// a commit decides it" is predictable from reading the file top to bottom.
// The regex is applied to the whole message: glyph does not decide where a
// sigil may sit, the pattern does.
//
// The only error is a matched message that yields no usable sigil: the
// capture is not one of = ~ ^ ! %, or the group captured nothing and the
// pattern declares no fixed fallback. Both are config bugs surfaced at the
// first commit that hits them.
func (c *Config) Match(message string) (Match, error) {
	for i := range c.Patterns {
		p := &c.Patterns[i]
		loc := p.re.FindStringSubmatchIndex(message)
		if loc == nil {
			continue
		}
		if p.skip {
			return Match{Matched: true, Skip: true, PatternIndex: i}, nil
		}
		if p.unlandable != "" {
			return Match{PatternIndex: -1, Unlandable: fmt.Sprintf("patterns[%d] marks this message unlandable: %s", i, p.unlandable)}, nil
		}

		groups := make(map[string]string)
		for gi, name := range p.re.SubexpNames() {
			if name == "" {
				continue
			}
			if _, seen := groups[name]; seen && loc[2*gi] < 0 {
				continue
			}
			groups[name] = submatch(message, loc, gi)
		}

		var sigil Sigil
		switch captured := groups[sigilGroup]; {
		case captured != "":
			s, err := parseSigil(captured)
			if err != nil {
				return Match{}, fmt.Errorf("patterns[%d] matched but %w", i, err)
			}
			sigil = s
		case p.fixed != nil:
			sigil = *p.fixed
		default:
			return Match{}, fmt.Errorf("patterns[%d] matched but its %s group captured nothing and no fixed semver_sigil is set", i, sigilGroup)
		}

		return Match{Matched: true, Sigil: sigil, Groups: groups, Warn: p.warn, PatternIndex: i}, nil
	}
	return Match{PatternIndex: -1}, nil
}

// submatch is what group gi captured in loc, FindStringSubmatchIndex's
// answer: "" for a group that took no part (-1, -1) as for one that captured
// nothing. participant is what tells the two apart.
func submatch(message string, loc []int, gi int) string {
	if loc[2*gi] < 0 {
		return ""
	}
	return message[loc[2*gi]:loc[2*gi+1]]
}

// participant is the group called name that took part in the match loc
// describes — the last such when more than one did, which is also the one
// whose capture a repeat leaves standing — as a submatch index, or -1 when
// the pattern names no such group or every one of them sat in an alternative
// the match did not take. It is the one reading of a repeated name: Match
// fills Groups by it, and span reads a capture's place in the message by it.
//
// SubexpIndex is not this reading. It answers the leftmost group of the name,
// and FindStringSubmatch fills a repeated name in index order, so the later
// group's empty capture stood in for the earlier one's value even when its
// alternative was not taken: under
// `^(?P<semver_sigil>[=~^!%]) :[a-z]+: .+|^:[a-z]+:(?P<semver_sigil>[=~^!%]) .+`
// the first alternative's `!` read as a sigil group that captured nothing
// (exit 3), and beside `semver_sigil = '='` it folded to none (measured
// 2026-11-04 at 2f9428a; TestMatchReadsTheGroupThatTookPart).
func participant(re *regexp.Regexp, loc []int, name string) int {
	at := -1
	for gi, n := range re.SubexpNames() {
		if n == name && loc[2*gi] >= 0 {
			at = gi
		}
	}
	return at
}

// UnclaimedDetail is the one sentence a history gate gives a message Match
// left unclaimed: the unlandable pattern's reason when one claimed it, the
// bare count otherwise. It is only meaningful when m.Matched is false.
func (c *Config) UnclaimedDetail(m Match) string {
	if m.Unlandable != "" {
		return m.Unlandable
	}
	return fmt.Sprintf("matches none of the %d configured patterns", len(c.Patterns))
}
