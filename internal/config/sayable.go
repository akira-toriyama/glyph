package config

import (
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
)

// This file answers what attribution's refusals ask of the pattern file
// (DESIGN §4.1, rule 3 and the contradiction check): of the escapes a refusal
// could name, which can this commit's message actually write? checkScopeWord
// holds the FILE to "some read scope group spells every name"; which pattern
// wins is a property of each commit, so a refusal asks again of the one that
// claimed it. The shipped raw-revert pattern fixes its sigil and captures no
// scope, and rule 3 told its author to name the package in the scope or write
// = — neither of which that message can do (t-mfny (A)).
//
// Wording only. Nothing here places a commit, moves a verdict or loads a
// file: it is computed when a refusal is worded, from the patterns Load
// already accepted, and a reader that needs a verdict must not ask it.

// Sayable is what one message can write of the two things that place a commit
// its files do not (rules 2–3): a scope naming a declared line, and the none
// sigil. Its first half is the pattern that claimed the message; its second,
// the rest of the file — where a reworded message could go instead.
//
// It answers in two ways, and which way a field was answered is what a
// sentence may build on:
//
//   - What a group can CAPTURE (Scopes, None, the Elsewhere pair) is read off
//     the group's own sub-expression, as checkScopeWord reads it: necessary
//     and not sufficient — context around a group can still keep a word it
//     spells from being captured. It claims what the groups admit, no more.
//   - What the message can go WITHOUT (ScopeDroppable, SigilDroppable) is
//     read off nothing: the message is rewritten and run through Match, and
//     the field is what Match said (removals). Taking something out changes
//     the message outside the group, which no sub-expression describes. Read
//     off the tree — "the scope sits under a ?, a *, a {0,n} or one branch of
//     an alternation" — `(?:type\((?P<scope>…)\)|release)` was told "or drop
//     the scope" of a message that, scope dropped, matches no pattern
//     (measured 2026-10-05: exit 3).
type Sayable struct {
	// Pattern is the claiming pattern's position in the file: patterns[N].
	Pattern int

	// ScopeGroup: the pattern captures a scope. Scopes: the declared package
	// names the group can capture, in config order. ScopeRequired: no message
	// the pattern claims goes without a scope — no ?, *, {0,n} or alternation
	// lets a match pass the group by, and the group cannot capture nothing.
	// That one is read off the tree and is exact in this direction only:
	// false does NOT say the scope can be dropped (ScopeDroppable does).
	ScopeGroup    bool
	ScopeRequired bool
	Scopes        []string

	// SigilGroup: the pattern captures the sigil; false means every match
	// takes its fixed semver_sigil. None: that capture can be =.
	SigilGroup bool
	None       bool

	// ScopeDroppable: THIS message with its scope dropped is still the
	// pattern's — claimed by it, with no scope and the sigil it had.
	// SigilDroppable: this message with its sigil left out is still the
	// pattern's and reads as =, the fixed semver_sigil a match takes when its
	// sigil group captures nothing. Both are Match's answer for the rewritten
	// message (removals). They are the only licence for "drop the scope" and
	// "leave the sigil out".
	ScopeDroppable bool
	SigilDroppable bool

	// ElsewhereScopes and ElsewhereNone are the same two escapes under the
	// file's OTHER patterns, the ones a landed message may be reworded for:
	// not a skip (placed nowhere), not an unlandable (it never lands), and
	// not a warn — a form the file's author accepts but would rather not
	// see is no remedy to send a message to. A pattern fixed at = counts
	// for ElsewhereNone beside one that captures it.
	ElsewhereScopes []string
	ElsewhereNone   bool

	// RootNeedsName: declaring the root package takes a name — the loader
	// would refuse `path = "."` alone (rootNeedsName). It is the loader's
	// answer and is derived from none of the fields above: they leave a warn
	// pattern out as no form to send a message to, while the loader reads its
	// scope group like any other. Decided from them, a file whose only scope
	// group sits in a warn pattern was told `path = "."` declares the root
	// package, and the file so written exits 2 (measured 2026-10-05).
	RootNeedsName bool
}

// Sayable reports what message can write under patterns[i], the pattern that
// claimed it, and what the file's other patterns could. ok is false when i
// names no pattern — the -1 of a commit the fold does not read, which
// attribution never refuses. A message patterns[i] does not claim — none
// handed in, or another pattern's — proves no removal, so neither Droppable
// field is set and the sentence offers neither.
func (c *Config) Sayable(i int, message string) (s Sayable, ok bool) {
	if i < 0 || i >= len(c.Patterns) {
		return Sayable{}, false
	}
	s.Pattern = i
	elsewhere := make(map[string]bool)
	for j := range c.Patterns {
		p := &c.Patterns[j]
		if j != i && (!p.bindsGroups() || p.Warn != "") {
			continue
		}
		// The flags regexp.Compile parses with: the tree compilePattern
		// already accepted, so the error arm is unreachable for a loaded file.
		tree, err := syntax.Parse(p.Pattern, syntax.Perl)
		if err != nil {
			continue
		}
		scope, sigil := captureOf(tree, ScopeGroup), captureOf(tree, SigilGroup)
		var names []string
		for _, pkg := range c.Packages {
			if scope.spells(pkg.Name) {
				names = append(names, pkg.Name)
			}
		}
		if j == i {
			s.ScopeGroup, s.ScopeRequired, s.Scopes = scope.present, scope.present && !scope.optional(), names
			s.SigilGroup, s.None = sigil.present, sigil.spells(SigilNone.String())
			continue
		}
		for _, n := range names {
			elsewhere[n] = true
		}
		if sigil.spells(SigilNone.String()) || (p.Fixed != nil && *p.Fixed == SigilNone) {
			s.ElsewhereNone = true
		}
	}
	for _, pkg := range c.Packages {
		if elsewhere[pkg.Name] {
			s.ElsewhereScopes = append(s.ElsewhereScopes, pkg.Name)
		}
	}
	s.ScopeDroppable, s.SigilDroppable = c.removals(i, message)
	s.RootNeedsName = c.rootNeedsName()
	return s, true
}

// rootNeedsName asks the loader whether `[[packages]] path = "."` loads with
// no name key: the root's default name put to checkScopeWord over the file's
// own spellers. Asked, never restated — "which patterns count" kept in a
// second place is what drifted from the loader.
func (c *Config) rootNeedsName() bool {
	spellers, err := scopeSpellers(c.Patterns)
	if err != nil {
		// Unreachable for a file Load accepted with packages declared, the
		// only kind a refusal is worded for.
		return true
	}
	return checkScopeWord(Package{Path: ".", Name: defaultName(".")}, false, spellers) != nil
}

// removals proves the two escapes that take something OUT of the message:
// "drop the scope" and "leave the sigil out". Each rewrites the message and
// asks Match — the whole of what lint judges a message by (Lint) — whether
// patterns[i] still claims it: with no scope and the sigil it had, or as =.
//
// The pattern's tree only proposes what to take out (withoutScope); Match
// disposes, on the patterns as compiled at load. So a wrong proposal costs an
// escape left unnamed, never one that fails when taken. What only Match can
// see: which pattern takes the rewritten message (first match wins, so an
// unlandable `^wip…` above the grammar claims `wip~: …` once `(haiku)` is
// gone), and whether what is left still fits the branch of an alternation the
// removed text sat in.
func (c *Config) removals(i int, message string) (scope, sigil bool) {
	before, ok := c.claimedBy(i, message)
	if !ok {
		return false, false
	}
	p := &c.Patterns[i]
	if dropped, ok := withoutScope(p.Pattern, message); ok {
		after, ok := c.claimedBy(i, dropped)
		scope = ok && after.Groups[ScopeGroup] == "" && after.Sigil == before.Sigil
	}
	if left, ok := withoutSigil(p.re, message); ok {
		after, ok := c.claimedBy(i, left)
		sigil = ok && after.Sigil == SigilNone
	}
	return scope, sigil
}

// claimedBy runs message through Match and reports whether patterns[i] is the
// pattern that claims it for the fold.
func (c *Config) claimedBy(i int, message string) (Match, bool) {
	m, err := c.Match(message)
	return m, err == nil && m.Matched && !m.Skip && m.PatternIndex == i
}

// withoutScope proposes message with its scope dropped: the text the scope
// group's innermost optional ancestor — a ?, a * or a {0,n} — matched, taken
// out whole, which is the scope with what the pattern writes around it
// (`(haiku)` under the presets). ok is false when there is nothing to
// propose:
//
//   - An alternation is no such ancestor. Its other branch is another
//     message, not this one less its scope.
//   - What goes with the scope must be punctuation or space alone. Under
//     `(?:\((?P<scope>…)#(?P<ticket>…)\))?` the group holds a ticket too, and
//     "drop the scope" does not tell anyone to drop that.
//   - The scope must lie inside the span. A capture keeps its value from an
//     earlier turn of a repeat, so under `(?:\((?P<scope>…)\)|!)*` the last
//     turn can be a `!` beside a scope captured two turns back.
//
// regexp reports no span for a non-capturing group, so the pattern is
// compiled again with one more capture around that ancestor's operand. The
// copy is asked where a span lies and nothing else; whether the rewritten
// message is the pattern's is Match's to say (removals).
func withoutScope(pattern, message string) (string, bool) {
	tree, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", false
	}
	group := lastGroup(tree, ScopeGroup)
	if group == nil {
		return "", false
	}
	around := optionalAround(tree, group)
	if around == nil {
		return "", false
	}
	name := "glyph_dropped"
	for slices.Contains(tree.CapNames(), name) {
		name += "_"
	}
	around.Sub[0] = &syntax.Regexp{Op: syntax.OpCapture, Name: name, Cap: tree.MaxCap() + 1, Sub: []*syntax.Regexp{around.Sub[0]}}
	re, err := regexp.Compile(tree.String())
	if err != nil {
		return "", false
	}
	loc := re.FindStringSubmatchIndex(message)
	if loc == nil {
		return "", false
	}
	whole, scope := span(re, loc, name), span(re, loc, ScopeGroup)
	if scope[0] < 0 || scope[0] == scope[1] || whole[0] < 0 || whole[0] > scope[0] || scope[1] > whole[1] {
		return "", false
	}
	beside := message[whole[0]:scope[0]] + message[scope[1]:whole[1]]
	if strings.ContainsFunc(beside, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return "", false
	}
	return message[:whole[0]] + message[whole[1]:], true
}

// withoutSigil proposes message with its sigil left out: the span the sigil
// group captured, removed. Nothing goes with a sigil the way parentheses go
// with a scope, so the capture's own span is all of it. ok is false when the
// message's sigil is not one it captured.
func withoutSigil(re *regexp.Regexp, message string) (string, bool) {
	loc := re.FindStringSubmatchIndex(message)
	if loc == nil {
		return "", false
	}
	sigil := span(re, loc, SigilGroup)
	if sigil[0] < 0 || sigil[0] == sigil[1] {
		return "", false
	}
	return message[:sigil[0]] + message[sigil[1]:], true
}

// span is where the last group called name matched in loc — the group Match
// reads (lastGroup) — or -1, -1 when the pattern names none or it took no
// part.
func span(re *regexp.Regexp, loc []int, name string) [2]int {
	last := -1
	for i, n := range re.SubexpNames() {
		if n == name {
			last = i
		}
	}
	if last < 0 {
		return [2]int{-1, -1}
	}
	return [2]int{loc[2*last], loc[2*last+1]}
}

// optionalAround returns target's innermost ancestor under re that a match
// can leave out by itself — a ?, a * or a {0,n} — or nil.
func optionalAround(re, target *syntax.Regexp) *syntax.Regexp {
	var find func(re, around *syntax.Regexp) (*syntax.Regexp, bool)
	find = func(re, around *syntax.Regexp) (*syntax.Regexp, bool) {
		if re == target {
			return around, true
		}
		if re.Op == syntax.OpQuest || re.Op == syntax.OpStar || (re.Op == syntax.OpRepeat && re.Min == 0) {
			around = re
		}
		for _, sub := range re.Sub {
			if a, found := find(sub, around); found {
				return a, true
			}
		}
		return nil, false
	}
	around, _ := find(re, nil)
	return around
}

// capture is one named group of one pattern as Match reads it: the last
// capture of that name (lastGroup), its sub-expression anchored whole, and
// whether a match can pass it by.
type capture struct {
	present   bool
	skippable bool
	word      *regexp.Regexp
}

func captureOf(tree *syntax.Regexp, name string) capture {
	group := lastGroup(tree, name)
	if group == nil {
		return capture{}
	}
	c := capture{present: true}
	_, c.skippable = passable(tree, group, false)
	// A sub-expression that will not compile on its own spells nothing here:
	// the refusal then names no escape it cannot vouch for.
	if word, err := wholeGroup(group); err == nil {
		c.word = word
	}
	return c
}

func (c capture) spells(word string) bool {
	return c.word != nil && c.word.MatchString(word)
}

// optional: some message the pattern claims has the group empty — a match can
// pass it by, or it can capture nothing. It is the tree's reading and it
// over-answers what THIS message can do without (an alternation's other
// branch is another message), so it is only ever negated: its false is
// Sayable.ScopeRequired, and its true licenses no sentence.
func (c capture) optional() bool {
	return c.present && (c.skippable || c.spells(""))
}

// passable finds target under re and reports whether a match of re can pass
// it by: some ancestor is a ?, a *, a {0,n} or an alternation. An alternation
// counts whole — whether the other branches hold the group too is not asked,
// because Match reads only the last group of a name, and a branch that
// captured under an earlier one hands on nothing.
func passable(re, target *syntax.Regexp, under bool) (found, skippable bool) {
	if re == target {
		return true, under
	}
	if re.Op == syntax.OpQuest || re.Op == syntax.OpStar || re.Op == syntax.OpAlternate || (re.Op == syntax.OpRepeat && re.Min == 0) {
		under = true
	}
	for _, sub := range re.Sub {
		if found, skippable = passable(sub, target, under); found {
			return true, skippable
		}
	}
	return false, false
}
