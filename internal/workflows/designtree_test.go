package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// treeRow matches one package row of DESIGN §5's tree fence as the tree writes
// it: the path at column 0, a space, then the one-line summary.
var treeRow = regexp.MustCompile(`(?m)^internal/([a-z]+)\s`)

// TestDesignTreeNamesEveryInternalPackage reconciles DESIGN §5's package tree
// against internal/ itself, in both directions. The section's own prose admits
// the tree had rotted once already (four packages behind before t-0cqs) and
// then it rotted the same way again — internal/testutil arrived in #105 and no
// gate read this file. "Read them against `ls internal/`" is an instruction a
// test can follow, so a reader no longer has to.
func TestDesignTreeNamesEveryInternalPackage(t *testing.T) {
	doc := repoFile(t, filepath.Join("docs", "DESIGN.md"))

	// Scope to §5 — heading through the next `## ` — then take the section's
	// first code fence, which is the tree. Scoping first keeps the match away
	// from §6 and any later fence that happens to spell an internal/ path.
	start := strings.Index(doc, "\n## 5. ")
	if start < 0 {
		t.Fatal("docs/DESIGN.md has no `## 5. ` heading; a renumbered section needs the anchor here updated too")
	}
	sec := doc[start+1:]
	if end := strings.Index(sec[1:], "\n## "); end >= 0 {
		sec = sec[:end+1]
	}
	fences := strings.Split(sec, "```")
	if len(fences) < 3 {
		t.Fatal("DESIGN §5 carries no closed code fence; the package tree moved and this test cannot find it")
	}
	tree := fences[1]

	documented := map[string]bool{}
	for _, m := range treeRow.FindAllStringSubmatch(tree, -1) {
		documented[m[1]] = true
	}
	// POSITIVE CONTROL: the fence must still yield internal/core, or the row
	// matcher (or the fence-finding) broke — and the loops below would then
	// reconcile two empty sets and pass over anything.
	if !documented["core"] {
		t.Fatal("canary: the §5 tree fence does not yield internal/core — the matcher or the fence moved, and this test is reconciling nothing")
	}

	// The filesystem side: every directory under internal/ holding at least
	// one .go file. A testdata/ directory or an emptied package is not a
	// package and earns no row.
	real := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join("..", "..", "internal"))
	if err != nil {
		t.Fatalf("reading internal/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join("..", "..", "internal", e.Name()))
		if err != nil {
			t.Fatalf("reading internal/%s: %v", e.Name(), err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".go") {
				real[e.Name()] = true
				break
			}
		}
	}

	for _, name := range sortedTreeNames(real) {
		if !documented[name] {
			t.Errorf("internal/%s exists and DESIGN §5's tree does not name it — the exact rot "+
				"this test ends; add the row (one line, what the package holds, §5)", name)
		}
	}
	for _, name := range sortedTreeNames(documented) {
		if !real[name] {
			t.Errorf("DESIGN §5's tree names internal/%s, which does not exist or holds no .go "+
				"file; delete the row — a tree that names ghosts teaches the reader to distrust "+
				"every other row", name)
		}
	}
}

func sortedTreeNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
