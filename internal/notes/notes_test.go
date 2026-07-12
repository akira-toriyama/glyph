package notes

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/parser"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current render")

// loadTable loads the embedded rules table or fails the test.
func loadTable(t *testing.T) *gitmoji.Table {
	t.Helper()
	table, err := gitmoji.Load()
	if err != nil {
		t.Fatalf("gitmoji.Load: %v", err)
	}
	return table
}

// sha returns a fake 40-char SHA made of one repeated hex digit, so goldens
// stay readable (the short form is seven of the same character).
func sha(digit string) string { return strings.Repeat(digit, 40) }

// TestGroupSectionsFollowTableOrder: sections come out in the table's render
// order regardless of commit order, and each version-moving commit lands in
// its rule's section.
func TestGroupSectionsFollowTableOrder(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":arrow_up:", Subject: "bump cobra to 1.10.2", SHA: sha("0")},
		{Gitmoji: ":bug:", Subject: "fix a crash when the config is empty", SHA: sha("b")},
		{Gitmoji: ":sparkles:", Scope: "ui", Subject: "add a command palette", SHA: sha("a")},
	}
	sections, err := Group(commits, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	var titles []string
	for _, s := range sections {
		titles = append(titles, s.Title)
	}
	want := []string{"Features", "Fixes", "Dependencies"}
	if !slices.Equal(titles, want) {
		t.Fatalf("section order = %v, want %v (the table's render order)", titles, want)
	}
}

// TestGroupHoistsBreaking: a breaking commit is hoisted into Breaking Changes
// whatever its gitmoji — even a none-bump code — and leaves its home section.
func TestGroupHoistsBreaking(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":recycle:", Subject: "rework the store layout", SHA: sha("d"), Breaking: true},
		{Gitmoji: ":bug:", Subject: "drop the broken fallback", SHA: sha("c"), Breaking: true},
	}
	sections, err := Group(commits, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(sections) != 1 || sections[0].Title != BreakingSection {
		t.Fatalf("breaking commits must all hoist into %q, got %+v", BreakingSection, sections)
	}
	if n := len(sections[0].Entries); n != 2 {
		t.Fatalf("Breaking Changes carries %d entries, want 2", n)
	}
	for _, e := range sections[0].Entries {
		if !e.Breaking {
			t.Errorf("hoisted entry %+v must record breaking=true", e)
		}
	}
}

// TestGroupSkipsNone: none-bump commits never reach the notes; an all-none
// input is an empty (nil) section list, not an error.
func TestGroupSkipsNone(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":memo:", Subject: "document the bump model", SHA: sha("9")},
		{Gitmoji: ":recycle:", Subject: "rework the store layout", SHA: sha("d")},
	}
	sections, err := Group(commits, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(sections) != 0 {
		t.Fatalf("all-none input must produce no sections, got %+v", sections)
	}
}

// TestGroupUnknownCodeIsLint: an unknown gitmoji is a hard lint error naming
// the code and the commit — never a silent skip.
func TestGroupUnknownCodeIsLint(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":not-a-real-code:", Subject: "mystery change", SHA: sha("e")},
	}
	_, err := Group(commits, table)
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeLint {
		t.Fatalf("Group with an unknown code = %v, want a CodeLint *core.Error", err)
	}
	if !strings.Contains(ce.Msg, ":not-a-real-code:") || !strings.Contains(ce.Msg, sha("e")) {
		t.Fatalf("lint error should name the code and the commit: %q", ce.Msg)
	}
}

// TestGroupKeepsEntryOrder: entries inside one section keep commit order
// (oldest first), and carry the commit's scope and the rule's emoji.
func TestGroupKeepsEntryOrder(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":bug:", Subject: "fix the first crash", SHA: sha("1")},
		{Gitmoji: ":pencil2:", Scope: "help", Subject: "fix a typo in the usage text", SHA: sha("2")},
	}
	sections, err := Group(commits, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(sections) != 1 || sections[0].Title != "Fixes" {
		t.Fatalf("both fixes must group under Fixes, got %+v", sections)
	}
	es := sections[0].Entries
	if es[0].Subject != "fix the first crash" || es[1].Subject != "fix a typo in the usage text" {
		t.Fatalf("entries must keep commit order, got %+v", es)
	}
	if es[0].Emoji != "🐛" || es[0].Code != ":bug:" {
		t.Errorf("entry must carry the rule's emoji and code, got %+v", es[0])
	}
	if es[1].Scope != "help" {
		t.Errorf("entry must carry the commit's scope, got %+v", es[1])
	}
}

// TestGroupEveryVersionMovingRuleHasAHome: exhaustively over the embedded
// table, every version-moving code groups into its declared section and every
// none code stays out — so a rules.json edit can never orphan a code from the
// notes. Also pins that the table knows the breaking section by name.
func TestGroupEveryVersionMovingRuleHasAHome(t *testing.T) {
	table := loadTable(t)
	if !slices.Contains(table.Sections, BreakingSection) {
		t.Fatalf("the embedded table's sections %v must include %q", table.Sections, BreakingSection)
	}
	for _, r := range table.Codes {
		sections, err := Group([]parser.Commit{
			{Gitmoji: r.Code, Subject: "exercise " + r.Code, SHA: sha("f")},
		}, table)
		if err != nil {
			t.Fatalf("Group(%s): %v", r.Code, err)
		}
		if r.Bump == gitmoji.BumpNone {
			if len(sections) != 0 {
				t.Errorf("none code %s must stay out of the notes, got %+v", r.Code, sections)
			}
			continue
		}
		if len(sections) != 1 || sections[0].Title != r.Section {
			t.Errorf("code %s must group under %q, got %+v", r.Code, r.Section, sections)
		}
	}
}

// TestRenderGolden: the Markdown render is pinned byte-for-byte against
// hand-written goldens (the format spec), and is deterministic. Regenerate
// with `go test ./internal/notes -update` after a deliberate format change.
func TestRenderGolden(t *testing.T) {
	table := loadTable(t)
	cases := []struct {
		name    string
		commits []parser.Commit
	}{
		{
			// Section order, the breaking hoist (a none code included), a
			// scoped and an unscoped entry, and a none commit dropped.
			name: "kitchen_sink",
			commits: []parser.Commit{
				{Gitmoji: ":recycle:", Subject: "rework the store layout", SHA: sha("d"), Breaking: true},
				{Gitmoji: ":sparkles:", Scope: "ui", Subject: "add a command palette", SHA: sha("a")},
				{Gitmoji: ":memo:", Subject: "document the palette", SHA: sha("9")},
				{Gitmoji: ":boom:", Subject: "drop the v1 config format", SHA: sha("e")},
				{Gitmoji: ":bug:", Subject: "fix a crash when the config is empty", SHA: sha("b")},
				{Gitmoji: ":bug:", Scope: "parser", Subject: "keep CRLF messages parsing", SHA: sha("c")},
				{Gitmoji: ":zap:", Subject: "speed up rule lookup", SHA: sha("f")},
				{Gitmoji: ":arrow_up:", Subject: "bump cobra to 1.10.2", SHA: sha("0")},
			},
		},
		{
			// Subjects are rendered verbatim: backticks, a pipe, emphasis
			// markers, and CJK must survive untouched.
			name: "adversarial_subjects",
			commits: []parser.Commit{
				{Gitmoji: ":bug:", Subject: "handle `nil` table without panicking", SHA: sha("1")},
				{Gitmoji: ":lipstick:", Scope: "tui", Subject: "align the 日本語 label | keep *bold* verbatim", SHA: sha("2")},
				{Gitmoji: ":wastebasket:", Subject: "deprecate the --legacy flag", SHA: sha("3")},
			},
		},
		{
			// A single section renders with no separator blank line at the end.
			name: "single_section",
			commits: []parser.Commit{
				{Gitmoji: ":pencil2:", Subject: "fix a typo in the help text", SHA: sha("4")},
				{Gitmoji: ":bug:", Subject: "fix the exit code on empty input", SHA: sha("5")},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sections, err := Group(c.commits, table)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			got, err := Render(sections)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			again, err := Render(sections)
			if err != nil || got != again {
				t.Fatalf("Render must be deterministic (err %v)", err)
			}
			path := filepath.Join("testdata", c.name+".golden.md")
			if *update {
				if werr := os.WriteFile(path, []byte(got), 0o644); werr != nil {
					t.Fatalf("update golden: %v", werr)
				}
				return
			}
			want, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("read golden: %v", rerr)
			}
			if got != string(want) {
				t.Fatalf("render mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", c.name, got, want)
			}
		})
	}
}

// TestRenderEmpty: no sections render to the empty string — the "nothing to
// release" verdict belongs to the caller, not the renderer.
func TestRenderEmpty(t *testing.T) {
	got, err := Render(nil)
	if err != nil || got != "" {
		t.Fatalf("Render(nil) = %q, %v; want empty and no error", got, err)
	}
}
