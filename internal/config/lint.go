package config

import (
	"fmt"
	"slices"
)

// LintVerdict is one message's standing under the config. Exactly one of
// three shapes: Excluded (the author is in exclude_authors — the message was
// never judged), OK (a pattern claimed it: a sigil obtained, or skip = true),
// or a violation (OK false, Reason says why).
type LintVerdict struct {
	Excluded bool
	OK       bool
	// Reason is the violation, empty when OK or Excluded. It is the complete
	// user-facing sentence: what failed and what would fix it.
	Reason string
}

// Lint judges a single commit message: does any pattern claim it, and does
// the claim yield a verdict? That is the whole check — v2 lint deliberately
// has no opinion on combinations (a :memo: subject carrying ! is the
// author's call; glyph parses and computes, it does not taste). The three
// violations are exactly the three ways a message can fail to mean anything
// under the file: no pattern matches, the semver_sigil capture is outside
// the alphabet, or the capture is empty with no fixed fallback.
//
// An exclude_authors author is excluded before matching, same order as the
// fold (its mutation row) and for the same reason: the key exists for bots,
// whose messages are exactly the ones the patterns do not describe.
func (c *Config) Lint(message, author string) LintVerdict {
	if slices.Contains(c.ExcludeAuthors, author) {
		return LintVerdict{Excluded: true}
	}
	m, err := c.Match(message)
	if err != nil {
		return LintVerdict{Reason: err.Error()}
	}
	if !m.Matched {
		return LintVerdict{Reason: fmt.Sprintf("message matches none of the %d configured patterns — write it to match one (see glyph.toml), or exclude the author", len(c.Patterns))}
	}
	return LintVerdict{OK: true}
}
