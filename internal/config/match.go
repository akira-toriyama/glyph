package config

import "fmt"

// Match is the outcome of running one commit message through the patterns.
// Exactly one of three shapes comes back: Matched=false (no pattern claimed
// the message — the caller decides what that refusal means: lint calls it a
// violation, bump refuses the range), Skip=true (a pattern claimed it and
// drops it from all processing), or a Sigil with the winning pattern's named
// groups.
type Match struct {
	// Matched is false when no pattern's regex matched the message. Sigil,
	// Skip, Groups and PatternIndex mean nothing in that case.
	Matched bool
	// Skip is true when the winning pattern declares skip = true: the commit
	// leaves lint, bump and notes entirely and Sigil means nothing.
	Skip bool
	// Sigil is the extracted version signal — the message's semver_sigil
	// capture when it produced one, else the pattern's fixed value.
	Sigil Sigil
	// Groups holds every named group the winning pattern defines, mapped to
	// what it captured (possibly ""). The notes line template's $xxx
	// placeholders read from here.
	Groups map[string]string
	// Warn is the winning pattern's warn message, "" when it declares none.
	// The verdict is unchanged — a warned match is still a match — but every
	// verdict computation that reads this Match must surface the message
	// (that invariant is what makes a warned pattern's hole visible for as
	// long as the pattern lives; see Pattern.Warn).
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
		sub := p.re.FindStringSubmatch(message)
		if sub == nil {
			continue
		}
		if p.Skip {
			return Match{Matched: true, Skip: true, PatternIndex: i}, nil
		}

		groups := make(map[string]string)
		captured := ""
		for gi, name := range p.re.SubexpNames() {
			if name == "" {
				continue
			}
			groups[name] = sub[gi]
			if name == SigilGroup {
				captured = sub[gi]
			}
		}

		var sigil Sigil
		switch {
		case captured != "":
			s, err := ParseSigil(captured)
			if err != nil {
				return Match{}, fmt.Errorf("patterns[%d] matched but %w", i, err)
			}
			sigil = s
		case p.Fixed != nil:
			sigil = *p.Fixed
		default:
			return Match{}, fmt.Errorf("patterns[%d] matched but its %s group captured nothing and no fixed semver_sigil is set", i, SigilGroup)
		}

		return Match{Matched: true, Sigil: sigil, Groups: groups, Warn: p.Warn, PatternIndex: i}, nil
	}
	return Match{PatternIndex: -1}, nil
}
