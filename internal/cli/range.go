package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// This file is the shared --range plumbing: flag validation and the
// participation walk the range commands build on. Command logic stays in the
// cmd_*.go files.

// participatingCommits reads revRange and returns the participating commits,
// parsed and carrying their SHA and author, oldest first. bump and notes share
// this walk — lint keeps its own (it collects violations instead of failing on
// the first).
func participatingCommits(ctx context.Context, revRange string) ([]parser.Commit, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, err
	}
	return participating(raws)
}

// participating applies the participation rules to raw commits from ANY source —
// git locally, or a pull request over the API (github.Commit mirrors RawCommit so
// it converts straight into this shape). Excluded commits (bots, merges,
// autosquash artifacts, raw git reverts) are skipped, never failed; a
// participating commit that does not parse is a hard lint error naming its SHA.
// Keeping one implementation is what guarantees a PR classifies identically
// whether its commits are read from git or from GitHub.
func participating(raws []gitsource.RawCommit) ([]parser.Commit, error) {
	commits := make([]parser.Commit, 0, len(raws))
	var bad []rangeViolation
	firstMsg := ""
	for _, raw := range raws {
		if _, excluded := bump.ExcludedFromClassification(raw.Author, firstLine(raw.Message), raw.Parents); excluded {
			continue
		}
		c, perr := parseRaw(raw)
		if perr != nil {
			// Walk the WHOLE range before failing. Stopping at the first
			// unparsable commit answered a three-commit cleanup with three
			// separate red runs (measured: `bump --range --json` returned one
			// commit, no rule id, no details, and the caller re-ran lint in a
			// loop to find the rest).
			//
			// The findings come from parser.Lint's own Parse-failure arm — a
			// message that does not parse yields exactly the one mapped
			// violation (malformed-subject or invalid-scope, fix attached) and
			// no strict rule ever runs, so the walk stays exactly as lenient
			// as before: a commit that PARSES but violates an authoring rule
			// is still not this walk's business (§2's ratified split — the
			// walk is lenient, authoring is strict).
			if firstMsg == "" {
				firstMsg = fmt.Sprintf("commit %.7s: %v", raw.SHA, perr)
			}
			for _, v := range parser.Lint(raw.Message, parser.LintOptions{}) {
				bad = append(bad, rangeViolation{SHA: raw.SHA, Subject: firstLine(raw.Message), Rule: v.Rule, Detail: v.Detail, Fix: v.Fix})
			}
			continue
		}
		commits = append(commits, c)
	}
	if len(bad) > 0 {
		// Msg keeps the exact `commit %.7s: <error>` head the first failure
		// always had: wedgeHint prefixes this string into its own sentence and
		// the wedge needs ONE commit to point at, so the extras ride in the
		// count and in Details rather than in the sentence.
		msg := firstMsg
		if extra := len(bad) - 1; extra > 0 {
			msg = fmt.Sprintf("%s (+%d more violation(s), all in details)", firstMsg, extra)
		}
		return nil, &core.Error{Code: core.CodeLint, Msg: msg, Details: bad}
	}
	return commits, nil
}

// parseRaw parses one raw commit and attaches its identity — the single
// raw→parser.Commit conversion, shared by the strict walk (participating) and
// the lenient release fallback so the two can never attach different fields.
// The error is the parser's own, unclassified: each caller owns its policy
// (hard lint vs warn-and-skip).
func parseRaw(raw gitsource.RawCommit) (parser.Commit, error) {
	c, err := parser.Parse(raw.Message)
	if err != nil {
		return parser.Commit{}, err
	}
	c.SHA, c.Author = raw.SHA, raw.Author
	return c, nil
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

// firstLine returns the subject line of a raw message (CR trimmed) — what the
// participation rules match on.
func firstLine(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return strings.TrimSuffix(message, "\r")
}
