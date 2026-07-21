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
	// NonBreaking records an explicit `NON-BREAKING: <why>` footer — the author
	// stating that a removal takes nothing public away. It is deliberately NOT
	// the absence of Breaking: for the removal codes, silence is the thing the
	// undeclared-removal rule refuses to accept.
	NonBreaking bool
	Subject     string // the human subject, with the markers and any legacy token stripped
	Body        string // everything after the subject line, leading blank lines dropped
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
	RuleInvalidScope      = "invalid-scope"
	RuleUnknownGitmoji    = "unknown-gitmoji"
	RuleWIPMergeCandidate = "wip-merge-candidate"
	RuleUppercaseSubject  = "uppercase-subject"
	RuleTrailingPeriod    = "trailing-period"
	RuleUndeclaredRemoval = "undeclared-removal"
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

	// laxSubjectRE is subjectRE with the scope slot opened up to anything but
	// parens. It never parses — it only DIAGNOSES. A subject that fails
	// subjectRE but matches this one is well-formed except for its scope, so
	// the author gets invalid-scope naming the offending text instead of
	// malformed-subject pointing at the whole line (t-edan). The asymmetry that
	// makes this bite: legacyTokenRE's scope slot is already `[^()]+`, so the
	// RETIRED form accepts `(Palette)` while the canonical one rejects it —
	// which is exactly the shape Swift repos write (sill, wand, facet, halo,
	// perch all scope by PascalCase module name).
	laxSubjectRE = regexp.MustCompile(`^(:[a-z0-9][a-z0-9_+-]*:)\(([^()]*)\)(!)? (\S.*)$`)

	// scopeRE is the scope slot of subjectRE standing alone, used to decide
	// whether merely lowercasing a rejected scope would make it legal.
	scopeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

	// trailerRE is the `token: value` shape of a git trailer. It decides whether
	// a trailer block CONTINUES — several trailers may stack with no blank line
	// between them, prose may not — so a footer (BREAKING CHANGE / NON-BREAKING)
	// stacked under another trailer is still seen.
	//
	// A git trailer key is a single run with NO whitespace, and case is not part
	// of its definition: `closes:`, `refs:` and `fixes:` are trailers exactly as
	// `Closes:` is, and `git interpret-trailers` treats them so. Requiring each
	// word capitalized (the first cut of this rule did) read those everyday
	// lowercase tokens as prose, closed the block, and DISCARDED a
	// `BREAKING CHANGE:` footer sitting under them — a major shipped as a minor,
	// which is the one thing this engine exists to prevent. So the token half is
	// case-blind. The one Conventional footer whose key contains a space,
	// `BREAKING CHANGE`, is spelled out as the sole multi-word alternative.
	//
	// Prose is still excluded structurally, not by case: an ordinary sentence
	// with a colon ("see the docs: here") has a SPACE before the colon, so it
	// matches neither a single no-whitespace token nor the literal
	// `BREAKING CHANGE`.
	trailerRE = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9-]*|BREAKING CHANGE): `)
)

// invalidScope reports whether subject's ONLY defect is a scope outside
// lowercase kebab-case, returning that scope. It is the shared oracle behind
// Parse's error text and Lint's rule id, so the two can never disagree.
func invalidScope(subject string) (string, bool) {
	if subjectRE.MatchString(subject) {
		return "", false
	}
	m := laxSubjectRE.FindStringSubmatch(subject)
	if m == nil || strings.TrimSpace(m[4]) == "" {
		return "", false
	}
	return m[2], true
}

// kebabSuggestion returns the lowercased form of scope when that alone makes it
// legal, else "" — a suggestion that would itself be rejected (a scope with
// commas or spaces) is worse than none.
func kebabSuggestion(scope string) string {
	if lower := strings.ToLower(scope); scopeRE.MatchString(lower) {
		return lower
	}
	return ""
}

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
		// Name the real defect when it is only the scope: "malformed subject"
		// points at the whole line and sends the author hunting the gitmoji or
		// the space, when all they wrote was (Palette) instead of (palette).
		if scope, ok := invalidScope(subject); ok {
			if hint := kebabSuggestion(scope); hint != "" {
				return Commit{}, core.Lintf("invalid scope %q in %q: scopes are lowercase kebab-case — write (%s)", scope, subject, hint)
			}
			return Commit{}, core.Lintf("invalid scope %q in %q: scopes are lowercase kebab-case (a-z, 0-9 and -; one scope, no separators)", scope, subject)
		}
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
	// A footer is a trailer, not prose. It counts only where a trailer can
	// legally sit: opening a block (after a blank line, or as the first body
	// line) or stacked under another trailer. Matching any line that merely
	// STARTS with the phrase makes a major release out of a paragraph that
	// happened to wrap that way — glyph's own history is the proof, since the
	// commit introducing this very rule wrapped onto "BREAKING CHANGE:" while
	// explaining it and classified itself as breaking.
	atBlockStart := true
	for _, l := range rest {
		if strings.TrimSpace(l) == "" {
			atBlockStart = true
			continue
		}
		if !atBlockStart {
			continue
		}
		if strings.HasPrefix(l, "BREAKING CHANGE:") || strings.HasPrefix(l, "BREAKING-CHANGE:") {
			c.Breaking = true
		}
		// The counterpart footer: an author asserting a removal is safe. Only a
		// removal code asks for it, and only the undeclared-removal rule reads
		// it — it never lowers a bump, so it cannot be used to hide a break.
		//
		// Uppercase and case-SENSITIVE, mirroring BREAKING CHANGE:, for the same
		// reason that one is: a body sentence may well read "this is
		// non-breaking: the API is untouched", and a footer that silences a rule
		// must not be spellable by accident in prose.
		//
		// The reason is mandatory. A bare footer would let the rule be satisfied
		// by reflex — the author types the magic word without answering the
		// question the rule exists to ask — which buys nothing over not having
		// the rule at all.
		if v, found := strings.CutPrefix(l, "NON-BREAKING:"); found && strings.TrimSpace(v) != "" {
			c.NonBreaking = true
		}
		// Trailers may stack without a blank line between them; prose may not,
		// so anything else closes the block.
		atBlockStart = trailerRE.MatchString(l)
	}
	return c, nil
}

// Lint checks one commit message and returns its violations in a stable order:
// malformed-subject (or the sharper invalid-scope) short-circuits — nothing
// else is checkable — then unknown-gitmoji, wip-merge-candidate,
// uppercase-subject, trailing-period, undeclared-removal. An unknown code is a
// hard violation here, never a silent fallback.
func Lint(message string, opts LintOptions) []Violation {
	c, err := Parse(message)
	if err != nil {
		rule := RuleMalformedSubject
		if lines := splitLines(message); len(lines) > 0 {
			if _, ok := invalidScope(lines[0]); ok {
				rule = RuleInvalidScope
			}
		}
		return []Violation{{Rule: rule, Detail: err.Error()}}
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
	// Deliberately NOT gated on MergeCandidate, unlike wip-merge-candidate.
	// :construction: is legal mid-branch and illegal only at the merge, so its
	// verdict genuinely changes with time. Whether a removal breaks anyone is
	// settled the moment the commit is written, so there is nothing to wait
	// for — and waiting is what hurts: caught at authoring time the fix is one
	// line in an open editor, caught in CI it is a rewrite of pushed history.
	if removalCodes[c.Gitmoji] && !c.Breaking && !c.NonBreaking {
		vs = append(vs, Violation{
			Rule: RuleUndeclaredRemoval,
			Detail: fmt.Sprintf("%s removes or renames something but does not say whether that breaks anyone — "+
				"add `!` (or a BREAKING CHANGE: footer) if it removes public API, else add a "+
				"`NON-BREAKING: <why>` footer to record that it does not", c.Gitmoji),
		})
	}
	return vs
}

// removalCodes are the gitmoji that take something AWAY. They all classify as
// bump=none, which is right for the overwhelmingly common case (dead code,
// docs, fixtures) and silently wrong for the rare one: deleting or renaming a
// library's public API is a major change that none of them can express.
//
// sill shipped exactly that — a public theme preset pruned with :fire: inside a
// :sparkles: PR, released as MINOR, breaking downstream wand (t-n158). :truck:
// is worse than :fire: there, because a rename resolves at runtime: sill's
// paletteFor("catppuccin-latte") fell back to another theme silently rather
// than failing.
//
// glyph cannot know whether the removed symbol was public — that is the
// consuming repo's knowledge, and an API-diff tool's job. What it CAN do is
// refuse to let the question go unanswered, which is all this rule does.
var removalCodes = map[string]bool{
	":fire:":   true, // Remove code or files.
	":coffin:": true, // Remove dead code.
	":truck:":  true, // Move or rename resources.
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
