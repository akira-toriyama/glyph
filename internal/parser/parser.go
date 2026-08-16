// Package parser turns a raw commit message into glyph's structured Commit and
// lints a single message against the commit convention. It is pure — no I/O, no
// globals — and deliberately independent of internal/gitmoji: token membership
// is injected (LintOptions.Known), so the grammar and the rules tables evolve
// separately. It owns both profile grammars (DESIGN §2): which one a call
// applies is a Grammar value, and everything the grammars do not own — the
// footer walk, the trailer-block rules, git's cleanup — is one code path
// shared by construction, so the two profiles cannot drift apart on a body.
package parser

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/internal/core"
)

// Grammar selects which profile's subject grammar Parse and Lint apply —
// DESIGN §2's profiles. The zero value is the gitmoji grammar on purpose: it
// is the default profile, so a caller that states nothing means what every
// caller meant before profiles existed, and what the CLI's --profile flag
// defaults to. Only the subject line is grammar's to select — the footer walk,
// the trailer-block rules and the cleanup are shared code below, which is what
// keeps the two profiles' verdicts on an identical body identical.
type Grammar int

const (
	GrammarGitmoji      Grammar = iota // `<:code:>[(scope)][!] <subject>` — the fleet's own
	GrammarConventional                // `<type>[(scope)][!]: <subject>` — ratified 2026-08-16 (§2.2)
)

// Commit is one parsed commit message plus the git metadata the range
// assembler attaches — Parse itself only sees the message, so it leaves SHA
// and Author zero.
type Commit struct {
	SHA      string
	Author   string
	Token    string // the leading vocabulary token: a textual code (":sparkles:") under GrammarGitmoji, a type ("feat") under GrammarConventional; shape-checked here, membership is the caller's
	Scope    string // without parens; from the new-format slot, else salvaged from a legacy token
	Breaking bool   // a `!` marker (new or legacy slot) or a BREAKING[- ]CHANGE: footer
	// NonBreaking records an explicit `NON-BREAKING: <why>` footer — the author
	// stating that a removal takes nothing public away. It is deliberately NOT
	// the absence of Breaking: for the removal codes, silence is the thing the
	// undeclared-removal rule refuses to accept.
	NonBreaking bool
	Subject     string // the human subject, with the markers and any legacy token stripped
	Body        string // everything after the subject line, leading blank lines dropped

	// bareNonBreaking records that a `NON-BREAKING:` footer was written WITHOUT a
	// reason, at a position where a footer counts. It changes no verdict — a bare
	// footer leaves the rule unsatisfied, which is the point — and exists only so
	// the violation can tell an author who typed the word something other than
	// "type the word". Unexported because that is its whole scope: nothing outside
	// this package has a use for it, and the JSON surfaces do not carry it.
	bareNonBreaking bool

	// miscasedNonBreaking and misplacedNonBreaking are bareNonBreaking's two
	// siblings — the other ways an author who ALREADY WROTE the footer can be
	// answered with "write the footer" (PR #78 fixed the bare state and wrote
	// down why: different mistakes need different sentences; these were the
	// two still sharing one). Both are diagnosis-only: neither sets
	// NonBreaking, neither changes any verdict, and only the
	// undeclared-removal arm reads them.
	//
	// They are deliberately NOT one detector. miscased fires only on a line
	// the atBlockStart gate already admitted — a case-insensitive scan of the
	// whole body would match the prose sentence "this is non-breaking: the API
	// is untouched", which is the exact accident the footer's case-SENSITIVITY
	// exists to make unspellable. misplaced fires only on the EXACT spelling
	// inside a paragraph — the wrap hazard, the same one that made glyph's own
	// commit classify as breaking when its explanation wrapped onto
	// "BREAKING CHANGE:".
	miscasedNonBreaking  bool
	misplacedNonBreaking bool

	// legacyToken records the retired Conventional `<type>[(scope)][!]:` token
	// Parse salvaged out of the subject (e.g. "fix(core)!:"), "" when none was
	// there. Parse keeps eating the token so the immutable pre-glyph history
	// walks and bumps exactly as before; Lint reads this field to make the same
	// token a hard error at AUTHORING time (legacy-token, ratified with v1.0.0:
	// one grammar, zero migration debt). Unexported for bareNonBreaking's
	// reason: only Lint has a use for it.
	legacyToken string
}

// Violation is one lint finding: a stable machine-readable rule id plus a
// human sentence quoting the offending part.
//
// Fix, when present, is the corrected SUBJECT LINE — replace the message's
// first line with it, verbatim, and the mechanical violations are gone. It is
// a field and not a phrase inside Detail because agents were regexing the
// prose to recover the suggestion, and the prose has been reworded before
// (PR #78) — a rewording must never break a machine consumer, which is what
// the stable rule ids already promise. Every fixable violation on one message
// carries the SAME fully-corrected line, so applying any one of them applies
// them all — fixes that each corrected only their own rule un-did each other
// when applied in sequence. Rules whose repair needs a human decision (an
// unknown code, a WIP marker, an undeclared removal) carry no Fix at all:
// a guessed fix that lint would bless anyway is how a wrong answer gets
// pasted with confidence.
type Violation struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// The lint rule identifiers. They are machine API — CI and agents branch on
// them — so keep the strings stable.
const (
	RuleMalformedSubject  = "malformed-subject"
	RuleInvalidScope      = "invalid-scope"
	RuleLegacyToken       = "legacy-token"
	RuleGitmojiToken      = "gitmoji-token"
	RuleUnknownGitmoji    = "unknown-gitmoji"
	RuleUnknownType       = "unknown-type"
	RuleWIPMergeCandidate = "wip-merge-candidate"
	RuleUppercaseSubject  = "uppercase-subject"
	RuleTrailingPeriod    = "trailing-period"
	RuleCJKSubject        = "cjk-subject"
	RuleRenderedGitmoji   = "rendered-gitmoji"
	RuleUndeclaredRemoval = "undeclared-removal"
)

// LintRule is one entry of the lint vocabulary: the value a finding's `rule`
// key can carry, plus the one property that changes a rule's verdict by mode.
// MergeCandidateOnly marks a rule that fires only under
// LintOptions.MergeCandidate — a consumer pre-checking at authoring time must
// not enforce it there.
type LintRule struct {
	Rule               string `json:"rule"`
	MergeCandidateOnly bool   `json:"merge_candidate_only"`
}

// LintRules enumerates one grammar's lint vocabulary, in the order the Rule*
// constants above are declared. Ids and the mode gate only, deliberately: the
// per-rule semantics live in DESIGN §2's list (gated by
// TestDesignDocNamesEveryRuleID), and a prose field here would be that
// section's second home. The slice is fresh per call so no caller can edit the
// vocabulary out from under the next. TestLintRulesMatchTheConstants holds
// both lists to the constants — every constant appears in at least one, each
// list follows declaration order — and the merge-candidate claim is held to
// Lint's real behaviour by TestLintRulesModeGating.
func LintRules(g Grammar) []LintRule {
	if g == GrammarConventional {
		return []LintRule{
			{Rule: RuleMalformedSubject},
			{Rule: RuleInvalidScope},
			{Rule: RuleGitmojiToken},
			{Rule: RuleUnknownType},
			{Rule: RuleUppercaseSubject},
			{Rule: RuleTrailingPeriod},
			{Rule: RuleCJKSubject},
		}
	}
	return []LintRule{
		{Rule: RuleMalformedSubject},
		{Rule: RuleInvalidScope},
		{Rule: RuleLegacyToken},
		{Rule: RuleUnknownGitmoji},
		{Rule: RuleWIPMergeCandidate, MergeCandidateOnly: true},
		{Rule: RuleUppercaseSubject},
		{Rule: RuleTrailingPeriod},
		{Rule: RuleCJKSubject},
		{Rule: RuleRenderedGitmoji},
		{Rule: RuleUndeclaredRemoval},
	}
}

// LintOptions configures Lint. Known is the token membership oracle (normally
// a rules-table lookup for the same profile as Grammar); nil skips the
// membership rule only. MergeCandidate marks commits that are on their way
// into main (a PR range): there a WIP :construction: commit is a violation,
// while it stays legal at authoring time (the commit-msg hook path).
type LintOptions struct {
	// Grammar selects the profile grammar the message is judged under. The
	// zero value is GrammarGitmoji — the default profile — so options built
	// before profiles existed still mean what they meant.
	Grammar        Grammar
	Known          func(code string) bool
	MergeCandidate bool
	// CodeForEmoji resolves a rendered emoji glyph to its textual :code:, ""
	// when it is no gitmoji. Injected exactly as Known is — the table lives in
	// internal/gitmoji and this package must not import it — and read only on
	// the Parse-failure path: a subject that OPENS with the glyph form of a
	// known code gets the sharper rendered-gitmoji finding (with the corrected
	// spelling as its fix) instead of malformed-subject quoting the whole
	// line. The resolver owns normalization (U+FE0F, ZWJ) on both sides; the
	// caller here hands over the raw leading token.
	CodeForEmoji func(glyph string) string
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

	// legacyTokenRE recognises the retired Conventional token
	// (`<type>[(scope)][!]: `) that pre-glyph house history carries between the
	// gitmoji and the subject. Parse still eats it — the walk over immutable
	// history must keep classifying those commits as it always has — but it now
	// also RECORDS it, and Lint turns the record into a hard error at authoring
	// time (legacy-token). The type vocabulary is the ratified house set
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

	// conventionalSubjectRE is the conventional profile's shape from DESIGN
	// §2.2: `<type>[(scope)][!]: <subject>` — the type-colon where the gitmoji
	// grammar has its mandatory space, the `!` before it as the Conventional
	// spec places it, and the same scope slot. The type shape is open (any
	// lowercase word), NOT the closed eleven-type alternation legacyTokenRE
	// uses: membership is the injected oracle's question here exactly as code
	// membership is under the gitmoji grammar, so the parser stays table-blind
	// in both profiles and `readme: fix typo` is diagnosed as an UNKNOWN TYPE
	// rather than a shapeless line. The subject is `\S.*` for subjectRE's
	// blank-subject reason.
	conventionalSubjectRE = regexp.MustCompile(`^([a-z][a-z0-9-]*)(\([a-z0-9][a-z0-9-]*\))?(!)?: (\S.*)$`)

	// conventionalLaxRE is conventionalSubjectRE with the scope slot opened to
	// anything but parens — laxSubjectRE's diagnosis role, ported: a subject
	// that fails the shape but matches this one is well-formed except for its
	// scope, and the author gets invalid-scope naming the offending text
	// instead of malformed-subject quoting the whole line.
	conventionalLaxRE = regexp.MustCompile(`^([a-z][a-z0-9-]*)\(([^()]*)\)(!)?: (\S.*)$`)

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

	// closingRefRE is the OTHER line a footer block legitimately contains: an
	// issue reference in GitHub's closing-keyword form, which has NO COLON —
	// `Closes #12`, `Fixes owner/repo#12`, `Resolves https://github.com/...`.
	//
	// It exists for exactly the reason the paragraph above exists, one step
	// further out. `git interpret-trailers` does not consider this a trailer, and
	// it is not one — but docs/DESIGN.md §2 lists `Closes #N` among the footers a
	// commit may carry, so it is a line the house writes INSIDE footer blocks. A
	// rule that recognised only `token: value` read it as prose, closed the block,
	// and discarded a `BREAKING CHANGE:` footer stacked beneath it. Measured
	// before this rule existed:
	//
	//	Closes: #12          + BREAKING CHANGE: ...  ->  breaking = true
	//	Closes #12           + BREAKING CHANGE: ...  ->  breaking = FALSE
	//	Fixes #12            + BREAKING CHANGE: ...  ->  breaking = FALSE
	//
	// A major shipped as a minor, out of a footer the design document itself
	// blesses, decided by one character. That is the same failure the trailer
	// rule above was written to end and the same one Q10 calls non-suppressible;
	// the earlier fix simply did not reach the colon-less half of the vocabulary.
	//
	// The keyword set is GitHub's own (close/closes/closed, fix/fixes/fixed,
	// resolve/resolves/resolved) and the reference must be the WHOLE rest of the
	// line, so ordinary prose that opens with one of these words ("fixes the
	// crash reported in #12 by rewriting the parser") is still prose and still
	// closes the block. Case-blind, like the trailer rule, and for the same
	// reason: `closes #12` is what people type.
	closingRefRE = regexp.MustCompile(`(?i)^(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?) +(?:#\d+|[\w.-]+/[\w.-]+#\d+|https?://\S+)$`)
)

// continuesTrailerBlock reports whether l is a line a footer block may contain
// without ending — a git trailer, or an issue reference in the colon-less
// closing-keyword form. Anything else is prose, and prose closes the block.
func continuesTrailerBlock(l string) bool {
	return trailerRE.MatchString(l) || closingRefRE.MatchString(l)
}

// grammarREs returns g's subject shape and its lax diagnostic variant. The
// two grammars' REs share their group layout — token, scope, bang, subject —
// which is what lets every function below stay one implementation.
func grammarREs(g Grammar) (shape, lax *regexp.Regexp) {
	if g == GrammarConventional {
		return conventionalSubjectRE, conventionalLaxRE
	}
	return subjectRE, laxSubjectRE
}

// invalidScope reports whether subject's ONLY defect is a scope outside
// lowercase kebab-case, returning that scope. It is the shared oracle behind
// Parse's error text and Lint's rule id, so the two can never disagree. One
// implementation for both grammars on purpose — the scope rule is the same
// house rule in each (DESIGN §2.2), so a scope diagnosis must never depend on
// which profile noticed it.
func invalidScope(subject string, g Grammar) (string, bool) {
	shape, lax := grammarREs(g)
	if shape.MatchString(subject) {
		return "", false
	}
	m := lax.FindStringSubmatch(subject)
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

// canonicalLine spells c as g's canonical subject line, or "" when no clean
// spelling exists — kebabSuggestion's rule, one level up: a suggestion the
// linter would itself reject is worse than none. The one lossy case is a
// salvaged scope outside kebab-case that even lowercasing cannot fix (the
// legacy slot is `[^()]+`, the canonical ones are not); dropping the scope
// from the suggestion would misrepresent the commit, so that case gets the
// plain grammar reminder instead.
func canonicalLine(c Commit, g Grammar) string {
	scope := ""
	if c.Scope != "" {
		s := c.Scope
		if !scopeRE.MatchString(s) {
			if s = kebabSuggestion(s); s == "" {
				return ""
			}
		}
		scope = "(" + s + ")"
	}
	bang := ""
	if c.Breaking {
		bang = "!"
	}
	sep := " "
	if g == GrammarConventional {
		sep = ": "
	}
	line := c.Token + scope + bang + sep + c.Subject
	if shape, _ := grammarREs(g); !shape.MatchString(line) {
		return ""
	}
	return line
}

// mechanicalFix spells the one corrected subject line that clears every
// mechanical rule at once: the retired token gone (canonicalLine under the
// gitmoji grammar), trailing periods trimmed, the first rune lowercased. ""
// when no clean line exists — canonicalLine's own rule, inherited: a
// suggestion the linter would itself reject is worse than none, and so is one
// for a subject that is nothing BUT periods.
func mechanicalFix(c Commit, g Grammar) string {
	s := strings.TrimRight(c.Subject, ".")
	if r, size := utf8.DecodeRuneInString(s); r != utf8.RuneError && unicode.IsUpper(r) {
		// Acronym refusal, measured before it was written: of the fleet
		// corpus's uppercase-subject rows, roughly a quarter open with an
		// all-caps word — TOML, README, CLI — and lowercasing exactly one rune
		// mints "tOML": a wrong answer lint itself would bless, pasted with
		// confidence. Two uppercase runes in a row is the signature; the human
		// rewrite ("support TOML …") is a rewording, not a mechanical fix.
		if next, _ := utf8.DecodeRuneInString(s[size:]); unicode.IsUpper(next) {
			return ""
		}
		s = string(unicode.ToLower(r)) + s[size:]
	}
	c.Subject = s
	return canonicalLine(c, g)
}

// Format returns the corrected MESSAGE — the first line replaced by the one
// mechanical fix, every other byte untouched — or the violations that stop it.
// The contract is the epic's invariant, enforced in code rather than promised:
// Lint(Format(m)) is empty, or Format refuses. Three outcomes:
//
//   - no violations: the message returns unchanged (fmt is idempotent);
//   - every violation carries the fix: the rewritten message returns, RE-LINTED
//     first — a composer bug must become a refusal here, never green-looking
//     red output;
//   - anything else — an unfixable violation, or a re-lint that still finds
//     fault: (nil, violations), and the caller refuses. Emitting a best-effort
//     line that still fails lint is the three-round-trip loop this exists to
//     end (measured: one message, three lint calls, because invalid-scope
//     short-circuits and each paste surfaced the next rule).
func Format(message string, opts LintOptions) (string, []Violation) {
	vs := Lint(message, opts)
	if len(vs) == 0 {
		return message, nil
	}
	for _, v := range vs {
		if v.Fix == "" {
			return "", vs
		}
	}
	lines := splitLines(message)
	lines[0] = vs[0].Fix // every fixable violation carries the same line
	formatted := strings.Join(lines, "\n")
	if left := Lint(formatted, opts); len(left) != 0 {
		return "", vs
	}
	return formatted, nil
}

// renderedGitmoji recognises a first line that opens with the GLYPH form of a
// known gitmoji — `✨ feat(tree): x` — and returns the textual code plus the
// corrected subject line. Detection sits beside laxSubjectRE, never inside the
// parse path: Parse must keep refusing the glyph form, or the walk would start
// accepting subjects the convention defines as textual.
//
// The fix is composed through Format on the code-substituted message rather
// than by string surgery, because the measured population co-trips other
// rules — five of the eight carried a retired Conventional token too — and two
// findings each proposing half the repair would fight over the same line. If
// even the substituted message cannot be made green mechanically, there is no
// fix, only the sharper name.
func renderedGitmoji(message, first string, opts LintOptions) (code, fix string, ok bool) {
	// Gitmoji-grammar only: under the conventional grammar there is no textual
	// spelling to correct toward, and the resolver should not even be
	// injected there — the grammar gate keeps the rule scoped even if one is.
	if opts.Grammar != GrammarGitmoji || opts.CodeForEmoji == nil {
		return "", "", false
	}
	glyph, rest, found := strings.Cut(first, " ")
	if !found || glyph == "" {
		return "", "", false
	}
	code = opts.CodeForEmoji(glyph)
	if code == "" {
		return "", "", false
	}
	lines := splitLines(message)
	lines[0] = code + " " + rest
	if formatted, vs := Format(strings.Join(lines, "\n"), opts); vs == nil {
		fix = splitLines(formatted)[0]
	}
	return code, fix, true
}

// gitmojiToken returns the gitmoji token a conventional-grammar subject opens
// with — the OTHER profile's vocabulary, written whole — or "". It matches the
// full gitmoji subject shape rather than a bare leading `:code:`, so only a
// line that genuinely IS the other grammar gets the sharper name; a stray
// colon-word stays malformed-subject. Grammar-gated here rather than at the
// call site so the detector can never leak into the gitmoji profile, where
// the same opening is simply correct.
func gitmojiToken(first string, g Grammar) string {
	if g != GrammarConventional {
		return ""
	}
	if m := subjectRE.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// scopeFix spells the corrected subject line for an invalid-scope finding, or
// "" — the lax match re-derives the pieces Parse refused, and kebabSuggestion
// decides whether lowercasing alone legalises the scope (anything more is a
// guess, and a guessed fix pastes a wrong answer with confidence).
func scopeFix(first string, g Grammar) string {
	_, lax := grammarREs(g)
	m := lax.FindStringSubmatch(first)
	if m == nil {
		return ""
	}
	k := kebabSuggestion(m[2])
	if k == "" {
		return ""
	}
	// Through mechanicalFix, not straight to a string: a line that fixed only
	// the scope and left an uppercase subject or a trailing period would be a
	// paste that still fails lint — the one property Fix exists to guarantee.
	return mechanicalFix(Commit{Token: m[1], Scope: k, Breaking: m[3] == "!", Subject: m[4]}, g)
}

// parseSubjectLine parses one already-trimmed, non-blank subject line under
// g's grammar. It owns everything grammar-specific about Parse: the shape, the
// sharpened invalid-scope diagnosis, and the gitmoji grammar's legacy-token
// leniency.
func parseSubjectLine(subject string, g Grammar) (Commit, error) {
	shape, _ := grammarREs(g)
	m := shape.FindStringSubmatch(subject)
	if m == nil {
		// Name the real defect when it is only the scope: "malformed subject"
		// points at the whole line and sends the author hunting the token or
		// the separator, when all they wrote was (Palette) instead of
		// (palette).
		if scope, ok := invalidScope(subject, g); ok {
			if hint := kebabSuggestion(scope); hint != "" {
				return Commit{}, core.Lintf("invalid scope %q in %q: scopes are lowercase kebab-case — write (%s)", scope, subject, hint)
			}
			return Commit{}, core.Lintf("invalid scope %q in %q: scopes are lowercase kebab-case (a-z, 0-9 and -; one scope, no separators)", scope, subject)
		}
		if g == GrammarConventional {
			return Commit{}, core.Lintf("malformed subject %q: want `<type>[(scope)][!]: <subject>` with a leading Conventional type", subject)
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
		Token:    m[1],
		Scope:    strings.Trim(m[2], "()"),
		Breaking: m[3] == "!",
		Subject:  m[4],
	}
	// The blank guard mirrors the one above: eating the legacy token must not
	// salvage a blank subject (`:bug: fix: \v`) — a blank remainder means the
	// colon phrase was part of the subject itself, not a token.
	if g == GrammarGitmoji {
		if lm := legacyTokenRE.FindStringSubmatch(c.Subject); lm != nil && strings.TrimSpace(lm[4]) != "" {
			if c.Scope == "" {
				c.Scope = strings.Trim(lm[2], "()")
			}
			c.Breaking = c.Breaking || lm[3] == "!"
			c.Subject = lm[4]
			c.legacyToken = lm[1] + lm[2] + lm[3] + ":"
		}
	}
	return c, nil
}

// Parse parses one commit message into a Commit under g's subject grammar. A
// message whose subject line does not open with the grammar's well-formed
// shape is a lint failure (*core.Error, CodeLint) — never a silently zero
// Commit. Under GrammarGitmoji the legacy Conventional token after the
// gitmoji is still eaten (its scope salvaged when the new slot has none, its
// `!` still meaning breaking) so pre-glyph history keeps parsing and bumping
// exactly as before — but it is recorded on the Commit, and Lint makes it a
// hard error at authoring time (legacy-token). Under GrammarConventional
// there is no such leniency to carry: no pre-profile history exists, so the
// grammar is exactly its shape. Everything below the subject line — the body,
// the footer walk, the trailer-block rules — is one shared path, which is
// what keeps the two profiles' verdicts on an identical body identical.
func Parse(message string, g Grammar) (Commit, error) {
	lines := splitLines(message)
	subject := ""
	if len(lines) > 0 {
		// Trailing whitespace is stripped because GIT strips it: measured against
		// git 2.54, `git commit -m ':bug:(cli) fix it.   '` and `-F` alike record
		// `:bug:(cli) fix it.`, exactly as `git stripspace` does (space, tab and
		// CR; a \v or \f is content and stays). Reading the untrimmed line let a
		// trailing space hide the period behind it, so `trailing-period` — which
		// DESIGN §2 states as a rule — did not fire on the very message git was
		// about to record with the period at the end. That hole was in EVERY mode,
		// `--range` and CI included, not only at the hook.
		subject = strings.TrimRight(lines[0], " \t\r")
	}
	if strings.TrimSpace(subject) == "" {
		return Commit{}, core.Lintf("empty commit message")
	}
	c, err := parseSubjectLine(subject, g)
	if err != nil {
		return Commit{}, err
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
			// The exact footer spelling inside a paragraph is the wrap hazard:
			// the author wrote the footer, the editor's fill wrapped it into
			// prose, and a trailer cannot sit there. Exact prefix only — this
			// arm must not become a case-insensitive body scan.
			if strings.HasPrefix(l, "NON-BREAKING:") {
				c.misplacedNonBreaking = true
			}
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
		if v, found := strings.CutPrefix(l, "NON-BREAKING:"); found {
			if strings.TrimSpace(v) != "" {
				c.NonBreaking = true
			} else {
				c.bareNonBreaking = true
			}
		} else if n := len("NON-BREAKING:"); len(l) >= n && strings.EqualFold(l[:n], "NON-BREAKING:") {
			// The right footer in the wrong case, at a position where the
			// footer would have counted. Gated on atBlockStart by construction
			// (this loop already is), so prose that merely contains the phrase
			// mid-sentence can never reach here — the case-sensitivity's whole
			// argument stays intact.
			c.miscasedNonBreaking = true
		}
		// Trailers and issue references may stack without a blank line between
		// them; prose may not, so anything else closes the block.
		atBlockStart = continuesTrailerBlock(l)
	}
	return c, nil
}

// Lint checks one commit message against opts.Grammar's vocabulary and
// returns its violations in a stable order: malformed-subject (or the sharper
// invalid-scope, rendered-gitmoji or gitmoji-token) short-circuits — nothing
// else is checkable — then the surviving rules in `LintRules(opts.Grammar)`
// order. An unknown token is a hard violation here, never a silent fallback.
func Lint(message string, opts LintOptions) []Violation {
	g := opts.Grammar
	c, err := Parse(message, g)
	if err != nil {
		rule, detail, fix := RuleMalformedSubject, err.Error(), ""
		if lines := splitLines(message); len(lines) > 0 {
			// Same trim as Parse: the two must judge the same string, or the rule
			// id and the message would disagree about which line was read.
			first := strings.TrimRight(lines[0], " \t\r")
			if _, ok := invalidScope(first, g); ok {
				rule = RuleInvalidScope
				fix = scopeFix(first, g)
			} else if gm := gitmojiToken(first, g); gm != "" {
				// The mirror image of legacy-token (DESIGN §2.2): the author
				// wrote the OTHER profile's vocabulary. Shape-checked only —
				// deciding whether the code is a known gitmoji would mean
				// loading the other profile's table to lint this one's
				// commits — and no fix: which type a gitmoji maps to is a
				// cross-vocabulary guess, exactly what Fix refuses to bless.
				rule = RuleGitmojiToken
				detail = fmt.Sprintf("subject opens with the gitmoji token %s where a Conventional type belongs — this profile's grammar is `<type>[(scope)][!]: <subject>` (if this repository lints gitmoji commits, it is missing --profile=gitmoji)", gm)
			} else if code, f, ok := renderedGitmoji(message, first, opts); ok {
				// The same argument that made invalid-scope: malformed-subject
				// quotes the whole line and sends the author hunting, when the
				// one wrong thing is the emoji being the GLYPH instead of the
				// textual code. Measured 8 subjects across 4 PRs — all PR
				// titles, where an emoji picker is one keystroke away.
				rule = RuleRenderedGitmoji
				fix = f
				detail = fmt.Sprintf("subject opens with the rendered emoji instead of its textual code %s — the convention is textual (pure ASCII, grep-friendly; GitHub renders the glyph anyway)", code)
				if f != "" {
					detail += fmt.Sprintf("; write %q", f)
				}
			}
		}
		return []Violation{{Rule: rule, Detail: detail, Fix: fix}}
	}
	var vs []Violation
	// One corrected line for every mechanical rule this message trips — each
	// fixable violation carries the same string (see Violation.Fix for why).
	fix := mechanicalFix(c, g)
	// First among the appended rules: it is a grammar defect, and the rewrite
	// it suggests is what the remaining rules should be judging. It fires in
	// every mode — unlike wip-merge-candidate there is no time at which the
	// retired token becomes legal, and the walk over history never runs Lint,
	// so no old commit can trip it. Gitmoji-grammar only by construction:
	// parseSubjectLine records the token under no other grammar.
	if c.legacyToken != "" {
		detail := fmt.Sprintf("retired Conventional token %q after the gitmoji — the convention is one grammar, `<:code:>[(scope)][!] <subject>`", c.legacyToken)
		if s := canonicalLine(c, g); s != "" {
			detail = fmt.Sprintf("retired Conventional token %q after the gitmoji — the convention is one grammar, write %q", c.legacyToken, s)
		}
		vs = append(vs, Violation{Rule: RuleLegacyToken, Detail: detail, Fix: fix})
	}
	if opts.Known != nil && !opts.Known(c.Token) {
		if g == GrammarConventional {
			vs = append(vs, Violation{
				Rule:   RuleUnknownType,
				Detail: fmt.Sprintf("unknown type %q: not in the embedded conventional table (see `glyph rules --profile=conventional`)", c.Token),
			})
		} else {
			vs = append(vs, Violation{
				Rule:   RuleUnknownGitmoji,
				Detail: fmt.Sprintf("unknown gitmoji %s: not in the embedded rules table (see `glyph rules`)", c.Token),
			})
		}
	}
	if g == GrammarGitmoji && opts.MergeCandidate && c.Token == wipCode {
		vs = append(vs, Violation{
			Rule:   RuleWIPMergeCandidate,
			Detail: fmt.Sprintf("%s is work-in-progress and must not reach a merge candidate — squash or reword it", wipCode),
		})
	}
	if r, _ := utf8.DecodeRuneInString(c.Subject); unicode.IsUpper(r) {
		vs = append(vs, Violation{
			Rule:   RuleUppercaseSubject,
			Detail: fmt.Sprintf("subject %q must start lowercase", c.Subject),
			Fix:    fix,
		})
	}
	if strings.HasSuffix(c.Subject, ".") {
		vs = append(vs, Violation{
			Rule:   RuleTrailingPeriod,
			Detail: fmt.Sprintf("subject %q must not end with a period", c.Subject),
			Fix:    fix,
		})
	}
	// The convention's subjects are English (CONTRIBUTING; DESIGN §2 has said
	// so since the scaffold) and this is the first rule to hold any of it —
	// measured before it existed, 592 fleet subjects carried CJK text and 585
	// of them linted clean. The id names what is CHECKED, not the policy: a
	// CJK scan is not an English detector (a French subject sails through),
	// and a rule called non-english-subject would promise exactly the
	// judgement it cannot make. Subject only, deliberately — bodies in the
	// fleet's history legitimately carry `---（和訳）` sections from before
	// translations were retired, and history is never re-judged, but the rule
	// must not fire on a body either way. No fix: the mechanical repair is a
	// translation, which is precisely the kind of guess Fix refuses to bless.
	if r := firstCJKRune(c.Subject); r != 0 {
		vs = append(vs, Violation{
			Rule:   RuleCJKSubject,
			Detail: fmt.Sprintf("subject %q carries CJK text (first: %q) — commit subjects are English (see the fleet CONTRIBUTING); reword the subject, the body is not judged here", c.Subject, r),
		})
	}
	// Deliberately NOT gated on MergeCandidate, unlike wip-merge-candidate.
	// :construction: is legal mid-branch and illegal only at the merge, so its
	// verdict genuinely changes with time. Whether a removal breaks anyone is
	// settled the moment the commit is written, so there is nothing to wait
	// for — and waiting is what hurts: caught at authoring time the fix is one
	// line in an open editor, caught in CI it is a rewrite of pushed history.
	if g == GrammarGitmoji && removalCodes[c.Token] && !c.Breaking && !c.NonBreaking {
		// An author who typed the footer and left it empty has already read the
		// instruction below, so repeating it verbatim answers nothing: the reply to
		// "NON-BREAKING:" used to be "add a `NON-BREAKING: <why>` footer", byte for
		// byte the same sentence as for a commit carrying no footer at all. The two
		// states need different sentences because they are different mistakes —
		// one has not answered the question, the other has not been asked it yet.
		// Four states, four sentences — an author who already wrote the footer
		// must never be told to write the footer (PR #78 fixed the bare state
		// and named the principle; miscased and misplaced are its siblings).
		var detail string
		switch {
		case c.bareNonBreaking:
			detail = fmt.Sprintf("%s carries a `NON-BREAKING:` footer with no reason after it, which leaves "+
				"the question unanswered — write WHY the removal takes nothing public away (e.g. "+
				"`NON-BREAKING: the preset was never exported`), or use `!` if it does", c.Token)
		case c.miscasedNonBreaking:
			detail = fmt.Sprintf("%s carries the footer in the wrong case — it is case-sensitive, exactly "+
				"`NON-BREAKING: <why>`, so that prose can never spell it by accident; recase the one you "+
				"already wrote", c.Token)
		case c.misplacedNonBreaking:
			detail = fmt.Sprintf("%s has a `NON-BREAKING:` line inside a body paragraph, where a trailer "+
				"cannot sit — it counts only after a blank line, as the first body line, or stacked under "+
				"another trailer; move the one you already wrote onto its own block (an editor's line-fill "+
				"wraps it into prose, which is how a footer disappears mid-sentence)", c.Token)
		default:
			detail = fmt.Sprintf("%s removes or renames something but does not say whether that breaks anyone — "+
				"add `!` (or a BREAKING CHANGE: footer) if it removes public API, else add a "+
				"`NON-BREAKING: <why>` footer to record that it does not", c.Token)
		}
		vs = append(vs, Violation{Rule: RuleUndeclaredRemoval, Detail: detail})
	}
	return vs
}

// firstCJKRune returns the first rune of s in a CJK script — Han, Hiragana,
// Katakana, Hangul, or the CJK punctuation/fullwidth blocks those scripts are
// typed with — or 0 when there is none. The block list is the check's whole
// definition, so it stays enumerable here rather than approximated by "not
// ASCII": café and naïve are not this rule's business.
func firstCJKRune(s string) rune {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
			(r >= 0x3000 && r <= 0x303F) || // CJK symbols and punctuation (、。「」…)
			(r >= 0xFF00 && r <= 0xFFEF) { // halfwidth and fullwidth forms (！？ＡＢ…)
			return r
		}
	}
	return 0
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
