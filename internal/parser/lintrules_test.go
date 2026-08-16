package parser

import (
	"os"
	"testing"
)

// grammars is the fixed set every vocabulary test walks. A third grammar added
// to parser.go without a row here fails TestLintRulesMatchTheConstants (its
// rules would be missing from the union) rather than shipping unmeasured.
var grammars = []struct {
	name string
	g    Grammar
}{
	{"gitmoji", GrammarGitmoji},
	{"conventional", GrammarConventional},
}

// TestLintRulesMatchTheConstants holds LintRules to the Rule* constants the
// same way TestDesignDocNamesEveryRuleID holds DESIGN §2 to them: by scraping
// parser.go rather than keeping a third hand-written list that would age in
// step with the second. With one vocabulary per grammar the sequence claim
// splits in two: every constant must appear in at least one grammar's list
// (a rule no grammar prints is dead machine API), and each list must follow
// the constants' declaration order without duplicates — order is part of the
// surface (agents diff it), and the subsequence check catches a duplicated,
// dropped or reshuffled entry per grammar in one comparison.
func TestLintRulesMatchTheConstants(t *testing.T) {
	src, err := os.ReadFile("parser.go")
	if err != nil {
		t.Fatalf("reading parser.go: %v", err)
	}
	var declared []string
	for _, m := range ruleConstRE.FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	if len(declared) == 0 {
		t.Fatal("found no Rule* constants in parser.go — the const block moved or was reformatted, " +
			"and this test is now checking nothing")
	}
	index := map[string]int{}
	for i, id := range declared {
		index[id] = i
	}

	printed := map[string]bool{}
	for _, gr := range grammars {
		list := LintRules(gr.g)
		last := -1
		for _, r := range list {
			i, ok := index[r.Rule]
			if !ok {
				t.Errorf("LintRules(%s) prints %q, which is not a Rule* constant in parser.go", gr.name, r.Rule)
				continue
			}
			if i <= last {
				t.Errorf("LintRules(%s) lists %q out of declaration order (or twice) — the vocabulary's "+
					"order is part of the surface, and it is the constants' order", gr.name, r.Rule)
			}
			last = i
			printed[r.Rule] = true
		}
	}
	for _, id := range declared {
		if !printed[id] {
			t.Errorf("Rule* constant %q appears in no grammar's LintRules — a rule no vocabulary prints "+
				"is dead machine API; add it to its grammar's list or retire the constant", id)
		}
	}
}

// TestLintRulesModeGating holds each vocabulary's merge_candidate_only claim
// to what Lint actually does, per grammar and per rule: every entry has a
// message here that trips it under its grammar, every entry must trip in
// merge-candidate mode, and it trips at authoring time exactly when the
// vocabulary says it is not merge-candidate-only. The fixture maps are
// required to cover each vocabulary in both directions, so a new rule cannot
// ship with its mode claim unmeasured and a retired rule cannot leave a
// fixture behind.
func TestLintRulesModeGating(t *testing.T) {
	emoji := func(glyph string) string {
		if glyph == "✨" {
			return ":sparkles:"
		}
		return ""
	}
	opts := func(g Grammar, mergeCandidate bool) LintOptions {
		o := LintOptions{Grammar: g, MergeCandidate: mergeCandidate}
		if g == GrammarGitmoji {
			o.Known = func(code string) bool { return code != ":not-a-real-code:" }
			o.CodeForEmoji = emoji
		} else {
			o.Known = func(typ string) bool { return typ == "fix" || typ == "feat" }
		}
		return o
	}

	// One message per rule that trips it and, where possible, nothing else —
	// the assertion below only asks for membership, so co-trips are tolerated.
	trip := map[Grammar]map[string]string{
		GrammarGitmoji: {
			RuleMalformedSubject:  "no gitmoji at all",
			RuleInvalidScope:      ":bug:(Palette) fix a crash",
			RuleLegacyToken:       ":bug: fix: fix a crash",
			RuleUnknownGitmoji:    ":not-a-real-code: fix a crash",
			RuleWIPMergeCandidate: ":construction: try things",
			RuleUppercaseSubject:  ":bug: Fix a crash",
			RuleTrailingPeriod:    ":bug: fix a crash.",
			RuleCJKSubject:        ":bug: クラッシュを直す",
			RuleRenderedGitmoji:   "✨ add a thing",
			RuleUndeclaredRemoval: ":fire: remove the preset",
		},
		GrammarConventional: {
			RuleMalformedSubject: "no type at all",
			RuleInvalidScope:     "fix(Palette): fix a crash",
			RuleGitmojiToken:     ":bug: fix a crash",
			RuleUnknownType:      "readme: fix a typo",
			RuleUppercaseSubject: "fix: Fix a crash",
			RuleTrailingPeriod:   "fix: fix a crash.",
			RuleCJKSubject:       "fix: クラッシュを直す",
		},
	}

	for _, gr := range grammars {
		fixtures := trip[gr.g]
		live := map[string]bool{}
		for _, r := range LintRules(gr.g) {
			live[r.Rule] = true
			msg, ok := fixtures[r.Rule]
			if !ok {
				t.Errorf("no tripping fixture for %s/%q — every vocabulary entry needs one, or its "+
					"merge_candidate_only claim ships unmeasured", gr.name, r.Rule)
				continue
			}
			t.Run(gr.name+"/"+r.Rule, func(t *testing.T) {
				fires := func(mergeCandidate bool) bool {
					for _, v := range Lint(msg, opts(gr.g, mergeCandidate)) {
						if v.Rule == r.Rule {
							return true
						}
					}
					return false
				}
				if !fires(true) {
					t.Fatalf("Lint(%q) under %s in merge-candidate mode does not report %q — every rule in "+
						"the vocabulary fires there; either the fixture rotted or the rule left Lint", msg, gr.name, r.Rule)
				}
				if got, want := fires(false), !r.MergeCandidateOnly; got != want {
					t.Fatalf("Lint(%q) under %s at authoring time reports %q = %v, but the vocabulary says "+
						"merge_candidate_only = %v — the printed claim and the enforced behaviour disagree, "+
						"and consumers pre-checking at the hook will enforce the printed one",
						msg, gr.name, r.Rule, got, r.MergeCandidateOnly)
				}
			})
		}
		for id := range fixtures {
			if !live[id] {
				t.Errorf("fixture %s/%q names a rule that is not in LintRules(%s) — retire the fixture with "+
					"the rule, or the map re-grows a vocabulary the binary no longer prints", gr.name, id, gr.name)
			}
		}
	}
}
