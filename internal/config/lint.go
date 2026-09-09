package config

import (
	"fmt"
	"slices"
	"strings"
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
	// Warn carries the winning pattern's warn message on an OK verdict — a
	// legal-but-undesirable message the caller must surface without failing
	// (see Pattern.Warn). Empty otherwise.
	Warn string
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
		return LintVerdict{Reason: c.unmatchedReason()}
	}
	return LintVerdict{OK: true, Warn: m.Warn}
}

// unmatchedReason is the no-pattern-matches violation. It quotes the
// subject form from commit.template (t-s1q0): the author of a refused
// message otherwise has to open glyph.toml and read the winning regex back
// into a shape, every time. The form is the file's own words, so its
// sigil-less window warning and this refusal spell the same line, and a
// config with no template gets the bare pointer instead.
func (c *Config) unmatchedReason() string {
	form := c.SubjectForm()
	if form == "" {
		return fmt.Sprintf("message matches none of the %d configured patterns — write it to match one (see glyph.toml), or exclude the author", len(c.Patterns))
	}
	return fmt.Sprintf("message matches none of the %d configured patterns — write it as %s with a semver_sigil of = ~ ^ ! or %% (commit.template in glyph.toml), or exclude the author", len(c.Patterns), form)
}

// SubjectForm is the first non-blank line of commit.template — the shape of
// a subject line as the file's author wrote it for a reader. It is quoted,
// never parsed: no placeholder is interpreted, and an absent [commit] block
// yields "".
func (c *Config) SubjectForm() string {
	for _, line := range strings.Split(c.Commit.Template, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
