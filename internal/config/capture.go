package config

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// This file is what Load reads off one named group of one pattern, once —
// every group of that name, since Match hands on whichever of them took part
// (participant): their sub-expressions compiled whole, as one alternation; and
// whether a match can pass the name by. The loader reads it — the scope
// spellers every package name is held to (scopeSpellers) — and so does
// Sayable when a refusal is worded; nothing here words a refusal. It is read
// only for a file that declares packages (readCaptures), the one kind either
// reader is asked about.

// capture is one named group of one pattern as Match reads it: the groups of
// that name (groupsNamed), their sub-expressions anchored whole as one
// alternation, and whether a match can pass the name by.
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
		tree, err := syntax.Parse(p.pattern, syntax.Perl)
		if err != nil {
			return fmt.Errorf("patterns[%d]: parse %q: %w", i, p.pattern, err)
		}
		p.scope, p.sigil = captureOf(tree, ScopeGroup), captureOf(tree, sigilGroup)
	}
	return nil
}

// captureOf reads every group called name. The name is skippable only when
// every one of its groups is: a group no ?, *, {0,n} or alternation sits over
// takes part in every match, so the name is bound whichever alternative the
// others sit in.
func captureOf(tree *syntax.Regexp, name string) capture {
	groups := groupsNamed(tree, name)
	if len(groups) == 0 {
		return capture{}
	}
	c := capture{present: true, skippable: true}
	for _, group := range groups {
		if _, skippable := passable(tree, group, false); !skippable {
			c.skippable = false
		}
	}
	// A sub-expression that will not compile on its own spells nothing here:
	// a refusal then names no escape it cannot vouch for. wordErr keeps why,
	// for the loader's refusal of such a scope group (scopeSpellers).
	c.word, c.wordErr = wholeGroups(groups)
	return c
}

func (c capture) spells(word string) bool {
	return c.word != nil && c.word.MatchString(word)
}

// passable finds target under re and reports whether a match of re can pass
// it by: some ancestor is a ?, a *, a {0,n} or an alternation. An alternation
// counts whole — whether its other branches hold a group of the same name is
// not asked, so a name every branch captures reads as skippable too. That
// over-answers in the safe direction only: optional() is never affirmed, only
// negated (Sayable.ScopeRequired), so a skippable read licenses no sentence.
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

// groupsNamed is every capture called name, in index order — the order
// SubexpNames lists them, so a group's position here is its position among
// the pattern's groups of that name — or nil when the pattern names none.
func groupsNamed(re *syntax.Regexp, name string) []*syntax.Regexp {
	var groups []*syntax.Regexp
	var walk func(*syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		if r.Op == syntax.OpCapture && r.Name == name {
			groups = append(groups, r)
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(re)
	return groups
}

// groupByCap is the capture numbered cap under re — the submatch index
// FindStringSubmatchIndex reports it at, since regexp numbers groups as the
// parser does — or nil.
func groupByCap(re *syntax.Regexp, cap int) *syntax.Regexp {
	if re.Op == syntax.OpCapture && re.Cap == cap {
		return re
	}
	for _, sub := range re.Sub {
		if g := groupByCap(sub, cap); g != nil {
			return g
		}
	}
	return nil
}

// wholeGroups compiles the captures' own sub-expressions anchored whole, as
// one alternation: every value a group of the name can hand on, whichever of
// them takes part. String round-trips to an equivalent expression, flags
// included (regexp/syntax's TestToStringEquivalentParse); it is compiled,
// never shown — a refusal quotes the source instead.
func wholeGroups(groups []*syntax.Regexp) (*regexp.Regexp, error) {
	alternatives := make([]string, 0, len(groups))
	for _, group := range groups {
		alternatives = append(alternatives, `(?:`+group.Sub[0].String()+`)`)
	}
	return regexp.Compile(`\A(?:` + strings.Join(alternatives, `|`) + `)\z`)
}
