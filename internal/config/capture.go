package config

import (
	"fmt"
	"regexp"
	"regexp/syntax"
)

// This file is what Load reads off one named group of one pattern, once: the
// last group of that name, the one Match keeps; its sub-expression compiled
// whole; and whether a match can pass it by. The loader reads it — the scope
// spellers every package name is held to (scopeSpellers) — and so does
// Sayable when a refusal is worded; nothing here words a refusal. It is read
// only for a file that declares packages (readCaptures), the one kind either
// reader is asked about.

// capture is one named group of one pattern as Match reads it: the last
// capture of that name (lastGroup), its sub-expression anchored whole, and
// whether a match can pass it by.
type capture struct {
	present   bool
	skippable bool
	word      *regexp.Regexp
	wordErr   error
}

// readCaptures fills every pattern's scope and sigil facts. buildPackages
// calls it before it builds the spellers, so a file that declares no
// packages pays for none of it.
func readCaptures(patterns []Pattern) error {
	for i := range patterns {
		p := &patterns[i]
		// The flags regexp.Compile parses with, so this is the tree
		// compilePattern already accepted.
		tree, err := syntax.Parse(p.Pattern, syntax.Perl)
		if err != nil {
			return fmt.Errorf("patterns[%d]: parse %q: %w", i, p.Pattern, err)
		}
		p.scope, p.sigil = captureOf(tree, ScopeGroup), captureOf(tree, sigilGroup)
	}
	return nil
}

func captureOf(tree *syntax.Regexp, name string) capture {
	group := lastGroup(tree, name)
	if group == nil {
		return capture{}
	}
	c := capture{present: true}
	_, c.skippable = passable(tree, group, false)
	// A sub-expression that will not compile on its own spells nothing here:
	// a refusal then names no escape it cannot vouch for. wordErr keeps why,
	// for the loader's refusal of such a scope group (scopeSpellers).
	c.word, c.wordErr = wholeGroup(group)
	return c
}

func (c capture) spells(word string) bool {
	return c.word != nil && c.word.MatchString(word)
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

// lastGroup is the capture called name with the highest index — the one
// whose value Match keeps, for the scope and the sigil alike: it fills both
// in index order — or nil when the pattern names none. withoutScope finds
// the group it proposes to drop with it too.
func lastGroup(re *syntax.Regexp, name string) *syntax.Regexp {
	var last *syntax.Regexp
	var walk func(*syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		if r.Op == syntax.OpCapture && r.Name == name && (last == nil || r.Cap > last.Cap) {
			last = r
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(re)
	return last
}

// wholeGroup compiles a capture's own sub-expression anchored whole: every
// value that group can hand on. String round-trips to an equivalent
// expression, flags included (regexp/syntax's TestToStringEquivalentParse);
// it is compiled, never shown — a refusal quotes the source instead.
func wholeGroup(group *syntax.Regexp) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + group.Sub[0].String() + `)\z`)
}
