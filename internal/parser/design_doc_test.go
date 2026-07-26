package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ruleConstRE scrapes the VALUE of every Rule* constant out of this package's own
// source. The values are read rather than listed here on purpose: a hand-kept set
// inside the test is the same drift one level down, and it would keep passing
// while both copies aged together.
var ruleConstRE = regexp.MustCompile(`(?m)^\s*Rule[A-Za-z0-9]*\s+=\s+"([a-z][a-z0-9-]*)"`)

// designRuleBulletRE finds the id docs/DESIGN.md §2 declares for a rule: a list
// item whose first token is a backticked lowercase-kebab word followed by an em
// dash. §2's other bullets all lead with a **bold** field name, so the shape is
// unambiguous — and keeping that shape is part of what this test asks of the doc.
var designRuleBulletRE = regexp.MustCompile("(?m)^- `([a-z][a-z0-9-]*)` — ")

// TestDesignDocNamesEveryRuleID turns a claim docs/DESIGN.md §2 makes about
// itself into a gate. The list there says every rule id is machine API and has to
// stay in step with the Rule* constants in parser.go; for the life of the repo it
// did not. The sentence that stood in its place was written in the scaffold
// commit, before this package existed, and named four of the seven rules in prose
// — it left malformed-subject implicit in the shape block and had no word at all
// for invalid-scope (t-edan) or undeclared-removal (t-n158), both added later.
// Replacing that sentence with a longer hand-kept list only moves the drift one
// paragraph over unless something checks the list, which is this.
//
// It guards both directions, and the second is the one prose cannot: a rule added
// here and never documented fails, and so does a rule-id-shaped token in §2's
// list that is no longer a live constant value. A RENAMED id would otherwise
// leave its old line standing, still reading as machine API to whoever branches
// on it.
func TestDesignDocNamesEveryRuleID(t *testing.T) {
	live := scrapeRuleConstants(t)
	documented := scrapeDesignRuleIDs(t)

	for id := range live {
		if !documented[id] {
			t.Errorf("rule id %q has no bullet in docs/DESIGN.md §2. Every id is machine API; "+
				"an undocumented one is an id callers cannot look up. Add a bullet to §2's list, "+
				"in the shape the others use: the backticked id, an em dash, then what it catches "+
				"and the defect it prevents.", id)
		}
	}
	for id := range documented {
		if !live[id] {
			t.Errorf("docs/DESIGN.md §2 documents rule id %q, which is not the value of any Rule* "+
				"constant in parser.go. Either the id was renamed here and the doc kept the old "+
				"line (a stale line still reads as machine API), or the rule was removed and its "+
				"bullet outlived it. Live ids: %s.", id, strings.Join(sortedKeys(live), ", "))
		}
	}
}

// scrapeRuleConstants reads parser.go for the live rule ids.
func scrapeRuleConstants(t *testing.T) map[string]bool {
	t.Helper()
	// Positive control: a pattern that matched nothing would let every assertion
	// above pass vacuously for ever. Prove it still bites on a real constant line.
	const canary = "\tRuleExampleOnly       = \"example-only\""
	if m := ruleConstRE.FindStringSubmatch(canary); m == nil || m[1] != "example-only" {
		t.Fatalf("ruleConstRE no longer reads a Rule* constant line (%q -> %v); the rest of this "+
			"test would compare two empty sets — re-derive the pattern from parser.go's const block",
			canary, m)
	}

	src, err := os.ReadFile("parser.go")
	if err != nil {
		t.Fatalf("reading parser.go: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range ruleConstRE.FindAllStringSubmatch(string(src), -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("found no Rule* constants in parser.go — the const block moved or was reformatted, " +
			"and this test is now checking nothing")
	}
	return ids
}

// scrapeDesignRuleIDs reads the ids §2 of the design doc declares.
func scrapeDesignRuleIDs(t *testing.T) map[string]bool {
	t.Helper()
	// The second positive control, for the second pattern.
	const canary = "- `example-only` — the subject line does not match the shape above."
	if m := designRuleBulletRE.FindStringSubmatch(canary); m == nil || m[1] != "example-only" {
		t.Fatalf("designRuleBulletRE no longer reads a §2 rule bullet (%q -> %v); the rest of this "+
			"test would pass vacuously — re-derive the pattern from the list in §2", canary, m)
	}

	// The doc lives two levels up, as internal/workflows' repoFile does it.
	path := filepath.Join("..", "..", "docs", "DESIGN.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	section := designSection(t, string(doc), "## 2. ")
	ids := map[string]bool{}
	for _, m := range designRuleBulletRE.FindAllStringSubmatch(section, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatalf("%s §2 declares no rule ids in the shape this test reads (a bullet opening with a "+
			"backticked id and an em dash). Either the list was reformatted — keep the shape, it is "+
			"what makes the list checkable — or the section was renumbered.", path)
	}
	return ids
}

// designSection returns the doc text from heading through to the next `## `, so a
// pattern meant for one section cannot match text in another.
func designSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n"+heading)
	if start < 0 {
		t.Fatalf("docs/DESIGN.md has no %q heading; this test cannot locate the rule list "+
			"(renumbered sections need the heading here updated too)", heading)
	}
	sec := doc[start+1:]
	if end := strings.Index(sec[1:], "\n## "); end >= 0 {
		sec = sec[:end+1]
	}
	return sec
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
