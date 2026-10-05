package config

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// This file is the scope-word rule (DESIGN §4.1, "name is the scope's word
// for the package"): a package name must be a word some commit's scope can
// be, or no commit can ever name its line — rule 3's refusal then offers the
// name as its escape, and the scope that writes it matches no pattern
// (t-mfny (B), t-f2cb (2)). The check reads the patterns the file compiled;
// it never widens a group, suggests a word, or rewrites a name.

// scopeSpeller is the scope group one pattern's matches bind: the group's own
// sub-expression anchored whole, which is every value that pattern can hand
// attribution as a scope.
type scopeSpeller struct {
	index  int    // the pattern's position in the file
	source string // Pattern.Pattern as written, the form every refusal quotes
	word   *regexp.Regexp
}

// scopeSpellers returns the scope group of each pattern whose groups a
// commit binds (Pattern.bindsGroups): a skip pattern's commit is placed
// nowhere and an unlandable one is reported unclaimed with no groups, so
// neither scope is ever read. Of two groups named scope in one pattern only
// the last counts — Match fills Groups in index order, so its capture
// overwrites an earlier one even when its own alternative did not take part
// (measured 2026-09-29 under `(?:(?P<scope>[a-z_]+)|(?P<scope>[A-Z]+))`:
// `(my_lib)` reached attribution as no scope at all). Empty when no such
// pattern captures a scope: the file has decided commits carry none, and its
// packages are placed by their paths alone.
func scopeSpellers(patterns []Pattern) ([]scopeSpeller, error) {
	var out []scopeSpeller
	for i := range patterns {
		p := &patterns[i]
		if !p.bindsGroups() {
			continue
		}
		// The flags regexp.Compile parses with, so this is the tree
		// compilePattern already accepted.
		tree, err := syntax.Parse(p.Pattern, syntax.Perl)
		if err != nil {
			return nil, fmt.Errorf("patterns[%d]: parse %q: %w", i, p.Pattern, err)
		}
		group := lastScopeGroup(tree)
		if group == nil {
			continue
		}
		// String round-trips to an equivalent expression, flags included
		// (regexp/syntax's TestToStringEquivalentParse); it is compiled, never
		// shown — the refusal quotes the source instead.
		word, err := regexp.Compile(`\A(?:` + group.Sub[0].String() + `)\z`)
		if err != nil {
			return nil, fmt.Errorf("patterns[%d]: the %s group of %q does not compile on its own: %w", i, ScopeGroup, p.Pattern, err)
		}
		out = append(out, scopeSpeller{index: i, source: p.Pattern, word: word})
	}
	return out, nil
}

// lastScopeGroup is the capture named ScopeGroup with the highest index —
// the one whose value Match keeps — or nil when the pattern names none.
func lastScopeGroup(re *syntax.Regexp) *syntax.Regexp {
	var last *syntax.Regexp
	var walk func(*syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		if r.Op == syntax.OpCapture && r.Name == ScopeGroup && (last == nil || r.Cap > last.Cap) {
			last = r
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(re)
	return last
}

// checkScopeWord refuses a package whose name none of the spellers captures.
// One is enough: which pattern wins is a property of each commit, so a name
// any read scope group spells is a name some commit can write — the union
// validateLineNames takes for note.line's placeholders. explicit says the
// name came from the file's `name` key rather than the default.
//
// The group's alphabet is necessary, not sufficient: context around it (a
// lazy quantifier, an overlapping neighbour) can still keep a word it spells
// from being captured whole. The loader refuses what no message can spell
// and claims nothing more.
func checkScopeWord(p Package, explicit bool, spellers []scopeSpeller) error {
	if len(spellers) == 0 {
		return nil
	}
	for _, s := range spellers {
		if s.word.MatchString(p.Name) {
			return nil
		}
	}
	what := fmt.Sprintf("name %q", p.Name)
	if !explicit {
		what = fmt.Sprintf("the default name %q (%s)", p.Name, defaultNameRule(p.Path))
	}
	if len(spellers) == 1 {
		return fmt.Errorf("%s is not a word a commit's scope can be: the %s group of patterns[%d] %q never captures it, so no commit could name this line — set name to a word that group spells, or change that scope group so it spells this one",
			what, ScopeGroup, spellers[0].index, spellers[0].source)
	}
	asked := make([]string, 0, len(spellers))
	for _, s := range spellers {
		asked = append(asked, fmt.Sprintf("patterns[%d] %q", s.index, s.source))
	}
	return fmt.Errorf("%s is not a word a commit's scope can be: no %s group of %s captures it, so no commit could name this line — set name to a word one of them spells, or change a scope group so it spells this one",
		what, ScopeGroup, strings.Join(asked, ", "))
}
