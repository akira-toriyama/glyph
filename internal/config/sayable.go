package config

import (
	"regexp"
	"regexp/syntax"
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

// Sayable is what a message can write of the two things that place a commit
// its files do not (rules 2–3): a scope naming a declared line, and the none
// sigil. Its first half is the pattern that claimed the message; its second,
// the rest of the file — where a reworded message could go instead.
//
// Like checkScopeWord it reads each group's own sub-expression, which is
// necessary and not sufficient: context around a group can still keep a word
// it spells from being captured. It claims what the groups admit, no more.
type Sayable struct {
	// Pattern is the claiming pattern's position in the file: patterns[N].
	Pattern int

	// ScopeGroup: the pattern captures a scope. ScopeOptional: a message it
	// claims can leave that scope out — the group sits under a ?, a *, a
	// {0,n} or one branch of an alternation, or can itself capture nothing.
	// Scopes: the declared package names the group can capture, in config
	// order.
	ScopeGroup    bool
	ScopeOptional bool
	Scopes        []string

	// SigilGroup: the pattern captures the sigil; false means every match
	// takes its fixed semver_sigil. None: that capture can be =.
	SigilGroup bool
	None       bool

	// ElsewhereScopes and ElsewhereNone are the same two escapes under the
	// file's OTHER patterns, the ones a landed message may be reworded for:
	// not a skip (placed nowhere), not an unlandable (it never lands), and
	// not a warn — a form the file's author accepts but would rather not
	// see is no remedy to send a message to. A pattern fixed at = counts
	// for ElsewhereNone beside one that captures it.
	ElsewhereScopes []string
	ElsewhereNone   bool
}

// Sayable reports what a message patterns[i] claims can write, and what the
// file's other patterns could. ok is false when i names no pattern — the -1
// of a commit the fold does not read, which attribution never refuses.
func (c *Config) Sayable(i int) (s Sayable, ok bool) {
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
			s.ScopeGroup, s.ScopeOptional, s.Scopes = scope.present, scope.optional(), names
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
	return s, true
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

// optional: a message the pattern claims can leave the group empty — a match
// can pass it by, or it can capture nothing.
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
