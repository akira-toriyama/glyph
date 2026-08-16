package conventional

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/akira-toriyama/glyph/internal/parser"
)

var update = flag.Bool("update", false, "rewrite the golden file with the rendered output")

func mustLoad(t *testing.T) *gitmoji.Table {
	t.Helper()
	tbl, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	return tbl
}

func TestLoadSucceeds(t *testing.T) {
	mustLoad(t)
}

// TestTypeCount is CodeCount's argument re-run (DESIGN §2.2): Load only
// catches rules.json disagreeing with TypeCount, which an edit to both would
// satisfy, so the literal 11 is pinned here as well — a type added or dropped
// cannot reach a release without a diff that says so.
func TestTypeCount(t *testing.T) {
	if TypeCount != 11 {
		t.Fatalf("TypeCount = %d, want the ratified 11 — growing the conventional set is a deliberate act "+
			"(DESIGN §2.2); change the ratification before this number", TypeCount)
	}
	if n := len(mustLoad(t).Codes); n != TypeCount {
		t.Fatalf("embedded table has %d types, TypeCount says %d", n, TypeCount)
	}
}

// TestDerivedFromCounterparts is DESIGN §3.1's table in executable form: each
// type takes the bump AND the section of its canonical gitmoji counterpart,
// so the two tables cannot quietly embody two philosophies. The map here IS
// the ratified counterpart column; a new type must name its counterpart (and
// a counterpart that disagrees is a dispute to settle in §3, not here), and a
// gitmoji-side reclassification of a counterpart code fails this test rather
// than silently splitting the profiles' philosophies.
func TestDerivedFromCounterparts(t *testing.T) {
	counterpart := map[string]string{
		"feat":     ":sparkles:",
		"fix":      ":bug:",
		"perf":     ":zap:",
		"revert":   ":rewind:",
		"docs":     ":memo:",
		"style":    ":art:",
		"refactor": ":recycle:",
		"test":     ":white_check_mark:",
		"build":    ":construction_worker:",
		"ci":       ":green_heart:",
		"chore":    ":hammer:",
	}
	conv := mustLoad(t)
	gm, err := gitmoji.Load()
	if err != nil {
		t.Fatalf("gitmoji.Load(): %v", err)
	}
	seen := map[string]bool{}
	for _, r := range conv.Codes {
		code, ok := counterpart[r.Code]
		if !ok {
			t.Errorf("type %q has no ratified gitmoji counterpart in this map — DESIGN §3.1 derives every "+
				"row; a type without a counterpart is a designed row, which the ratification forbids", r.Code)
			continue
		}
		seen[r.Code] = true
		want, ok := gm.Lookup(code)
		if !ok {
			t.Errorf("type %q names counterpart %s, which is not in the gitmoji table", r.Code, code)
			continue
		}
		if r.Bump != want.Bump {
			t.Errorf("type %q: bump %s, but counterpart %s says %s — the table is derived, not designed; "+
				"settle the dispute on the gitmoji row (DESIGN §3)", r.Code, r.Bump, code, want.Bump)
		}
		if r.Section != want.Section {
			t.Errorf("type %q: section %q, but counterpart %s says %q", r.Code, r.Section, code, want.Section)
		}
	}
	for typ := range counterpart {
		if !seen[typ] {
			t.Errorf("counterpart map names %q, which is not in the embedded table — retire the row with "+
				"the type, or the map re-grows a vocabulary the binary no longer ships", typ)
		}
	}
}

// TestSectionsDrawFromTheGitmojiList pins §3.1's "no new names": every
// conventional section exists in the gitmoji list, in the same relative
// order, and the breaking hoist target is among them — Group hoists into
// notes.BreakingSection unconditionally, so a table without that section
// would render a breaking commit into a section the render order does not
// know.
func TestSectionsDrawFromTheGitmojiList(t *testing.T) {
	conv := mustLoad(t)
	gm, err := gitmoji.Load()
	if err != nil {
		t.Fatalf("gitmoji.Load(): %v", err)
	}
	pos := map[string]int{}
	for i, s := range gm.Sections {
		pos[s] = i
	}
	last := -1
	for _, s := range conv.Sections {
		i, ok := pos[s]
		if !ok {
			t.Errorf("section %q is not in the gitmoji section list — §3.1 ratified no new names", s)
			continue
		}
		if i <= last {
			t.Errorf("section %q sits out of the gitmoji list's relative order — one render order, two views", s)
		}
		last = i
	}
	hasBreaking := false
	for _, s := range conv.Sections {
		if s == notes.BreakingSection {
			hasBreaking = true
		}
	}
	if !hasBreaking {
		t.Fatalf("sections %v lack %q — the hoist target every breaking commit renders into", conv.Sections, notes.BreakingSection)
	}
}

// TestEveryTypeParsesUnderTheGrammar pins the seam between the table and the
// grammar: every ratified type, written as a subject, comes back as that
// commit's Token. A type the grammar cannot carry is a row no commit can ever
// reach — dead data that looks like vocabulary.
func TestEveryTypeParsesUnderTheGrammar(t *testing.T) {
	for _, r := range mustLoad(t).Codes {
		c, err := parser.Parse(r.Code+": do the thing", parser.GrammarConventional)
		if err != nil {
			t.Errorf("type %q does not parse as a conventional subject: %v", r.Code, err)
			continue
		}
		if c.Token != r.Code {
			t.Errorf("type %q parsed to token %q", r.Code, c.Token)
		}
	}
}

// TestEmojiRefused proves the engine's vocabulary split is load-bearing both
// ways: the gitmoji table requires an emoji per entry, and this one refuses
// an entry that carries one — data the renderers would silently drop is how
// a hand edit hides.
func TestEmojiRefused(t *testing.T) {
	bad := `{"version":"x","sections":["Fixes"],"codes":[{"code":"fix","emoji":"🐛","meaning":"m","bump":"patch","section":"Fixes"}]}`
	if _, err := gitmoji.ParseTable([]byte(bad), spec); err == nil {
		t.Fatal("ParseTable accepted a conventional entry carrying an emoji — the vocabulary has none, and " +
			"silent extra data is a hand edit's hiding place")
	}
}

// TestRulesJSONIsCanonical mirrors the gitmoji table's own guard: the stored
// rules.json is byte-identical to CanonicalJSON(), so `glyph rules
// --profile=conventional --json` reproduces the embedded source verbatim and
// a hand edit that survives parsing still fails for its formatting.
func TestRulesJSONIsCanonical(t *testing.T) {
	got, err := mustLoad(t).CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON(): %v", err)
	}
	want, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatalf("reading rules.json: %v", err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("rules.json is not in canonical form; re-emit it with CanonicalJSON() (got %d bytes, stored %d)",
			len(got), len(want))
	}
}

// TestMarkdownGolden holds the rendered docs table to docs/conventional-table.md,
// exactly as the gitmoji table's golden does — the drift guard for
// `glyph rules --profile=conventional --md`.
func TestMarkdownGolden(t *testing.T) {
	golden := filepath.Join("..", "..", "docs", "conventional-table.md")
	got := mustLoad(t).Markdown()
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden %s (run with -update to create it): %v", golden, err)
	}
	if got != string(want) {
		t.Fatalf("Markdown() drifted from %s; run `go test ./internal/conventional -run Golden -update`", golden)
	}
}
