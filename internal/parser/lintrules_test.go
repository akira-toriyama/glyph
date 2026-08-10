package parser

import (
	"os"
	"testing"
)

// TestLintRulesMatchTheConstants holds LintRules() to the Rule* constants the
// same way TestDesignDocNamesEveryRuleID holds DESIGN §2 to them: by scraping
// parser.go rather than keeping a third hand-written list that would age in
// step with the second. It asserts the full sequence, not the set — the
// vocabulary's order is part of the surface (agents diff it), and sequence
// equality also catches a duplicated or dropped entry in one comparison.
func TestLintRulesMatchTheConstants(t *testing.T) {
	src, err := os.ReadFile("parser.go")
	if err != nil {
		t.Fatalf("reading parser.go: %v", err)
	}
	var want []string
	for _, m := range ruleConstRE.FindAllStringSubmatch(string(src), -1) {
		want = append(want, m[1])
	}
	if len(want) == 0 {
		t.Fatal("found no Rule* constants in parser.go — the const block moved or was reformatted, " +
			"and this test is now checking nothing")
	}

	got := LintRules()
	if len(got) != len(want) {
		var ids []string
		for _, r := range got {
			ids = append(ids, r.Rule)
		}
		t.Fatalf("LintRules() has %d entries, parser.go declares %d Rule* constants.\ngot:  %v\nwant: %v",
			len(got), len(want), ids, want)
	}
	for i, r := range got {
		if r.Rule != want[i] {
			t.Errorf("LintRules()[%d] = %q, want %q (declaration order of the Rule* constants)",
				i, r.Rule, want[i])
		}
	}
}

// TestLintRulesModeGating holds the vocabulary's merge_candidate_only claim to
// what Lint actually does, per rule: every entry has a message here that trips
// it, every entry must trip in merge-candidate mode, and it trips at authoring
// time exactly when the vocabulary says it is not merge-candidate-only. The
// fixture map is required to cover the vocabulary in both directions, so a new
// rule cannot ship with its mode claim unmeasured and a retired rule cannot
// leave a fixture behind.
func TestLintRulesModeGating(t *testing.T) {
	known := func(code string) bool { return code != ":not-a-real-code:" }
	emoji := func(glyph string) string {
		if glyph == "✨" {
			return ":sparkles:"
		}
		return ""
	}
	opts := func(mergeCandidate bool) LintOptions {
		return LintOptions{Known: known, MergeCandidate: mergeCandidate, CodeForEmoji: emoji}
	}

	// One message per rule that trips it and, where possible, nothing else —
	// the assertion below only asks for membership, so co-trips are tolerated.
	trip := map[string]string{
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
	}

	live := map[string]bool{}
	for _, r := range LintRules() {
		live[r.Rule] = true
		msg, ok := trip[r.Rule]
		if !ok {
			t.Errorf("no tripping fixture for %q — every vocabulary entry needs one, or its "+
				"merge_candidate_only claim ships unmeasured", r.Rule)
			continue
		}
		t.Run(r.Rule, func(t *testing.T) {
			fires := func(mergeCandidate bool) bool {
				for _, v := range Lint(msg, opts(mergeCandidate)) {
					if v.Rule == r.Rule {
						return true
					}
				}
				return false
			}
			if !fires(true) {
				t.Fatalf("Lint(%q) in merge-candidate mode does not report %q — every rule in the "+
					"vocabulary fires there; either the fixture rotted or the rule left Lint", msg, r.Rule)
			}
			if got, want := fires(false), !r.MergeCandidateOnly; got != want {
				t.Fatalf("Lint(%q) at authoring time reports %q = %v, but the vocabulary says "+
					"merge_candidate_only = %v — the printed claim and the enforced behaviour disagree, "+
					"and consumers pre-checking at the hook will enforce the printed one",
					msg, r.Rule, got, r.MergeCandidateOnly)
			}
		})
	}
	for id := range trip {
		if !live[id] {
			t.Errorf("fixture %q names a rule that is not in LintRules() — retire the fixture with "+
				"the rule, or the map re-grows a vocabulary the binary no longer prints", id)
		}
	}
}
