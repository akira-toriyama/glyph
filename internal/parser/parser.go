// Package parser turns a raw commit message into glyph's structured Commit and
// lints a single message against the commit convention. It is pure — no I/O, no
// globals — and deliberately independent of internal/gitmoji: code membership
// is injected (LintOptions.Known), so the grammar and the rules table evolve
// separately.
package parser

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/internal/core"
)

// Commit is one parsed commit message plus the git metadata the range
// assembler attaches — Parse itself only sees the message, so it leaves SHA
// and Author zero.
type Commit struct {
	SHA      string
	Author   string
	Gitmoji  string // leading textual code, e.g. ":sparkles:" (shape-checked, membership is the caller's)
	Scope    string // without parens; from the new-format slot, else salvaged from a legacy token
	Breaking bool   // a `!` marker (new or legacy slot) or a BREAKING[- ]CHANGE: footer
	Subject  string // the human subject, with the markers and any legacy token stripped
	Body     string // everything after the subject line, leading blank lines dropped
}

// Violation is one lint finding: a stable machine-readable rule id plus a
// human sentence quoting the offending part.
type Violation struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// The lint rule identifiers. They are machine API — CI and agents branch on
// them — so keep the strings stable.
const (
	RuleMalformedSubject  = "malformed-subject"
	RuleUnknownGitmoji    = "unknown-gitmoji"
	RuleWIPMergeCandidate = "wip-merge-candidate"
	RuleUppercaseSubject  = "uppercase-subject"
	RuleTrailingPeriod    = "trailing-period"
)

// LintOptions configures Lint. Known is the gitmoji membership oracle
// (normally a gitmoji.Table lookup); nil skips the membership rule only.
// MergeCandidate marks commits that are on their way into main (a PR range):
// there a WIP :construction: commit is a violation, while it stays legal at
// authoring time (the commit-msg hook path).
type LintOptions struct {
	Known          func(code string) bool
	MergeCandidate bool
}

// wipCode is the one gitmoji that may never reach a merge candidate.
const wipCode = ":construction:"

var (
	// subjectRE is the new-format shape from docs/DESIGN.md §2:
	// `<:code:>[(scope)][!] <subject>`, everything glued until the single
	// mandatory space. The subject must open with a non-space (`\S.*`, not
	// `.+`): with `.+` a run of spaces after the code parses as a blank
	// subject and sails through every lint rule — not uppercase, no trailing
	// period — so `:bug:  ` would lint clean. Membership of the code is
	// checked separately.
	subjectRE = regexp.MustCompile(`^(:[a-z0-9][a-z0-9_+-]*:)(\([a-z0-9][a-z0-9-]*\))?(!)? (\S.*)$`)

	// legacyTokenRE accepts-and-ignores the retired Conventional token
	// (`<type>[(scope)][!]: `) that pre-glyph house history carries between the
	// gitmoji and the subject. The type vocabulary is the ratified house set
	// (CONTRIBUTING.md), deliberately closed so ordinary subjects with a colon
	// (":memo: note: …") are not eaten.
	// The remainder is `\S.*` for the same blank-subject reason as subjectRE:
	// `:bug: fix:  ` must not salvage a blank subject out of the legacy slot.
	legacyTokenRE = regexp.MustCompile(`^(feat|fix|perf|revert|docs|style|refactor|test|build|ci|chore)(\([^()]+\))?(!)?: (\S.*)$`)
)

// Parse parses one commit message into a Commit. A message whose subject line
// does not open with a well-formed `<:code:>[(scope)][!] <subject>` is a lint
// failure (*core.Error, CodeLint) — never a silently zero Commit. The legacy
// Conventional token after the gitmoji is accepted and dropped (its scope is
// salvaged when the new slot has none, its `!` still means breaking) so
// pre-glyph history keeps parsing — no flag-day.
func Parse(message string) (Commit, error) {
	lines := splitLines(message)
	subject := ""
	if len(lines) > 0 {
		subject = lines[0]
	}
	if strings.TrimSpace(subject) == "" {
		return Commit{}, core.Lintf("empty commit message")
	}
	m := subjectRE.FindStringSubmatch(subject)
	if m == nil {
		return Commit{}, core.Lintf("malformed subject %q: want `<:code:>[(scope)][!] <subject>` with a leading textual gitmoji", subject)
	}
	// The regexp's \S only rejects ASCII-space openers; a subject of Unicode
	// whitespace (a \v, an NBSP) would still sail through every lint rule, so
	// blankness is decided by unicode.IsSpace (TrimSpace), not the regexp.
	if strings.TrimSpace(m[4]) == "" {
		return Commit{}, core.Lintf("malformed subject %q: the subject is blank", subject)
	}
	c := Commit{
		Gitmoji:  m[1],
		Scope:    strings.Trim(m[2], "()"),
		Breaking: m[3] == "!",
		Subject:  m[4],
	}
	// The blank guard mirrors the one above: eating the legacy token must not
	// salvage a blank subject (`:bug: fix: \v`) — a blank remainder means the
	// colon phrase was part of the subject itself, not a token.
	if lm := legacyTokenRE.FindStringSubmatch(c.Subject); lm != nil && strings.TrimSpace(lm[4]) != "" {
		if c.Scope == "" {
			c.Scope = strings.Trim(lm[2], "()")
		}
		c.Breaking = c.Breaking || lm[3] == "!"
		c.Subject = lm[4]
	}

	rest := lines[1:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	c.Body = strings.TrimRight(strings.Join(rest, "\n"), "\n")
	for _, l := range rest {
		if strings.HasPrefix(l, "BREAKING CHANGE:") || strings.HasPrefix(l, "BREAKING-CHANGE:") {
			c.Breaking = true
			break
		}
	}
	return c, nil
}

// Lint checks one commit message and returns its violations in a stable order:
// malformed-subject short-circuits (nothing else is checkable), then
// unknown-gitmoji, wip-merge-candidate, uppercase-subject, trailing-period.
// An unknown code is a hard violation here, never a silent fallback.
func Lint(message string, opts LintOptions) []Violation {
	c, err := Parse(message)
	if err != nil {
		return []Violation{{Rule: RuleMalformedSubject, Detail: err.Error()}}
	}
	var vs []Violation
	if opts.Known != nil && !opts.Known(c.Gitmoji) {
		vs = append(vs, Violation{
			Rule:   RuleUnknownGitmoji,
			Detail: fmt.Sprintf("unknown gitmoji %s: not in the embedded rules table (see `glyph rules`)", c.Gitmoji),
		})
	}
	if opts.MergeCandidate && c.Gitmoji == wipCode {
		vs = append(vs, Violation{
			Rule:   RuleWIPMergeCandidate,
			Detail: fmt.Sprintf("%s is work-in-progress and must not reach a merge candidate — squash or reword it", wipCode),
		})
	}
	if r, _ := utf8.DecodeRuneInString(c.Subject); unicode.IsUpper(r) {
		vs = append(vs, Violation{
			Rule:   RuleUppercaseSubject,
			Detail: fmt.Sprintf("subject %q must start lowercase", c.Subject),
		})
	}
	if strings.HasSuffix(c.Subject, ".") {
		vs = append(vs, Violation{
			Rule:   RuleTrailingPeriod,
			Detail: fmt.Sprintf("subject %q must not end with a period", c.Subject),
		})
	}
	return vs
}

// splitLines splits a message on \n and drops a trailing \r per line, so CRLF
// input parses identically to LF input.
func splitLines(message string) []string {
	lines := strings.Split(message, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}
