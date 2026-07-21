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

// TestGroupBoomSectionVsFlagAreOrthogonal: a plain :boom: (no ! and no
// BREAKING CHANGE footer) reaches Breaking Changes through its rule's section,
// so Entry.Breaking stays false — the field records the orthogonal flag, not
// section membership. A refactor that fused the two (Breaking = title ==
// BreakingSection) would flip this field and change the JSON output; this test
// is the guard. A flagged commit in the same section keeps Breaking true.
func TestGroupBoomSectionVsFlagAreOrthogonal(t *testing.T) {
	table := loadTable(t)
	sections, err := Group([]parser.Commit{
		{Gitmoji: ":boom:", Subject: "drop the v1 config format", SHA: sha("a")},                 // section-routed, flag off
		{Gitmoji: ":bug:", Subject: "remove the broken fallback", SHA: sha("b"), Breaking: true}, // flag-hoisted
	}, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(sections) != 1 || sections[0].Title != BreakingSection {
		t.Fatalf("both commits belong in %q, got %+v", BreakingSection, sections)
	}
	es := sections[0].Entries
	if es[0].Code != ":boom:" || es[0].Breaking {
		t.Errorf("a plain :boom: is section-routed, so breaking must stay false, got %+v", es[0])
	}
	if es[1].Code != ":bug:" || !es[1].Breaking {
		t.Errorf("a flag-hoisted commit must record breaking=true, got %+v", es[1])
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

// TestGroupRemovalsAreNotesVisible: a removal/rename commit (:fire:/:coffin:/
// :truck:, all bump=none) is section-routed into Removals — notes inclusion
// follows the rule's section, not its bump, so an honest deletion no longer
// vanishes from the release notes. The flag stays false: a removal reaches its
// section on its own, it is not a breaking-flag hoist.
func TestGroupRemovalsAreNotesVisible(t *testing.T) {
	table := loadTable(t)
	commits := []parser.Commit{
		{Gitmoji: ":fire:", Scope: "Palette", Subject: "prune catppuccin-latte", SHA: sha("a")},
		{Gitmoji: ":coffin:", Subject: "drop the dead legacy shim", SHA: sha("b")},
		{Gitmoji: ":truck:", Subject: "rename paletteFor to themeFor", SHA: sha("c")},
	}
	sections, err := Group(commits, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(sections) != 1 || sections[0].Title != "Removals" {
		t.Fatalf("removals must group under a single Removals section, got %+v", sections)
	}
	if n := len(sections[0].Entries); n != 3 {
		t.Fatalf("Removals carries %d entries, want 3 (fire/coffin/truck)", n)
	}
	for _, e := range sections[0].Entries {
		if e.Breaking {
			t.Errorf("a removal is section-routed, not a breaking-flag hoist; breaking must stay false, got %+v", e)
		}
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

// TestGroupEveryRuleGroupsBySection: exhaustively over the embedded table, a
// code with a section groups into that section and a sectionless code stays out
// — inclusion tracks the section, not the bump, so a none-bump removal still has
// a home. So a rules.json edit can never orphan a code from the notes. Also pins
// that the table knows the breaking section by name.
func TestGroupEveryRuleGroupsBySection(t *testing.T) {
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
		if r.Section == "" {
			if len(sections) != 0 {
				t.Errorf("sectionless code %s must stay out of the notes, got %+v", r.Code, sections)
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
			// scoped and an unscoped entry, and a none commit dropped. Two
			// none-bump removals — a :fire: prune and a :truck: rename —
			// surface under Removals, pinning that it renders right after
			// Breaking Changes and that both codes coalesce into one section.
			name: "kitchen_sink",
			commits: []parser.Commit{
				{Gitmoji: ":recycle:", Subject: "rework the store layout", SHA: sha("d"), Breaking: true},
				{Gitmoji: ":sparkles:", Scope: "ui", Subject: "add a command palette", SHA: sha("a")},
				{Gitmoji: ":memo:", Subject: "document the palette", SHA: sha("9")},
				{Gitmoji: ":boom:", Subject: "drop the v1 config format", SHA: sha("e")},
				{Gitmoji: ":fire:", Subject: "prune the legacy theme presets", SHA: sha("7")},
				{Gitmoji: ":truck:", Scope: "api", Subject: "rename paletteFor to themeFor", SHA: sha("8")},
				{Gitmoji: ":bug:", Subject: "fix a crash when the config is empty", SHA: sha("b")},
				{Gitmoji: ":bug:", Scope: "parser", Subject: "keep CRLF messages parsing", SHA: sha("c")},
				{Gitmoji: ":zap:", Subject: "speed up rule lookup", SHA: sha("f")},
				{Gitmoji: ":arrow_up:", Subject: "bump cobra to 1.10.2", SHA: sha("0")},
			},
		},
		{
			// Subjects are rendered near-verbatim: backticks, a pipe, emphasis
			// markers, and CJK must survive untouched. The one rewrite is the
			// mention pass — a bare @token comes out inside a backtick fence,
			// which GitHub renders as code and links to nobody (the facet
			// v7.0.0 incident), while an already-quoted `@name`, an email, and
			// a pinned ref stay put. Two subjects here show the fence sizing
			// itself against the line it lands in (t-fbg3): the one that
			// already holds a `@name` span gets a two-backtick fence, and the
			// one carrying a stray backtick gets one too, because a fence that
			// merely tied the author's run could be closed by it. The last one
			// carries the backtick in its SCOPE, where the fence used to be
			// blind to it — see TestRenderSizesTheFenceAgainstTheWholeLine.
			name: "adversarial_subjects",
			commits: []parser.Commit{
				{Gitmoji: ":bug:", Subject: "handle `nil` table without panicking", SHA: sha("1")},
				{Gitmoji: ":lipstick:", Scope: "tui", Subject: "align the 日本語 label | keep *bold* verbatim", SHA: sha("2")},
				{Gitmoji: ":wastebasket:", Subject: "deprecate the --legacy flag", SHA: sha("3")},
				{Gitmoji: ":bug:", Subject: "pin release + update-tap callers to @v1", SHA: sha("5")},
				{Gitmoji: ":sparkles:", Scope: "view", Subject: "show `@name` chips that type @name for you", SHA: sha("6")},
				{Gitmoji: ":bug:", Subject: "mail dev@example.com when actions/checkout@v5 breaks", SHA: sha("7")},
				{Gitmoji: ":bug:", Scope: "lint", Subject: "accept a lone ` in the subject, as @octocat asked", SHA: sha("8")},
				{Gitmoji: ":bug:", Scope: "readme`", Subject: "credit @alice and @bob for the fix", SHA: sha("9")},
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

// TestRenderSizesTheFenceAgainstTheWholeLine pins the fix for the leak that a
// FIELD is not an inline context. The line concatenates the scope and the
// subject, so a backtick fence sized against the subject alone was stolen by a
// backtick the scope carried: the fence's opening backtick paired with the
// scope's stray one, the pairing fused the words between them into a code span,
// and the mention was pushed back out into live prose. Measured at GitHub on
// 2026-07-21, the one-backtick line below linked @alice for real.
//
// The scope reaches here with a backtick in it because it can: glyph's legacy
// token grammar accepts anything but parentheses in the scope slot, so
// ":bug: fix(readme`): credit @alice and @bob for the fix" parses and lints
// clean today.
func TestRenderSizesTheFenceAgainstTheWholeLine(t *testing.T) {
	table := loadTable(t)
	sections, err := Group([]parser.Commit{
		{Gitmoji: ":bug:", Scope: "readme`", Subject: "credit @alice and @bob for the fix", SHA: sha("9")},
	}, table)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	got, err := Render(sections)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The scope's backtick now arrives ESCAPED — a scope is a plain-text field
	// (t-j0c6) — and the fence is still TWO backticks, which is the property
	// this test exists for. Measured: longestBacktickRun counts an escaped
	// backtick like any other, deliberately, because an over-long fence is
	// harmless while a fence that ties an authored run can be closed by it. So
	// escaping the scope did not quietly shrink the fence back to one.
	const want = "- 🐛 **readme\\`:** credit ``@alice`` and ``@bob`` for the fix (9999999)\n"
	if !strings.Contains(got, want) {
		t.Errorf("the fence was not sized against the whole line:\n--- got ---\n%s\n--- want the line ---\n%s", got, want)
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

// TestRenderFlattensLineTerminators pins the t-bz0r fix. A commit subject can
// carry a bare CR — parser.splitLines trims only a TRAILING "\r" per line, so an
// interior one survives Parse — and CommonMark treats a bare CR as a line
// terminator. Unflattened, the notes line ENDS there: the list item closes and
// whatever the subject said next is parsed as a fresh block, so a commit can
// fabricate a release-notes section in someone else's release.
//
// Measured against GitHub 2026-07-21, on bytes written in binary mode (an
// earlier investigation read the same probe through a text-mode reader, which
// silently turned the CR into an LF and measured nothing):
//
//	in   "- 🐛 **parser:** fix the thing\r## Fabricated Section (8e0abc1)"
//	out  <li>🐛 <strong>parser:</strong> fix the thing</li>
//	     <h2>Fabricated Section (8e0abc1)</h2>      ← a REAL document section
func TestRenderFlattensLineTerminators(t *testing.T) {
	table := loadTable(t)
	for _, c := range []struct{ name, subject string }{
		{"a bare CR", "fix the thing\r## Fabricated Section"},
		{"CRLF", "fix the thing\r\n## Fabricated Section"},
		{"LF", "fix the thing\n## Fabricated Section"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sections, err := Group([]parser.Commit{
				{Gitmoji: ":bug:", Scope: "parser", Subject: c.subject, SHA: sha("8")},
			}, table)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			got, err := Render(sections)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			// The entry must be exactly ONE line. The heading marker may
			// survive as TEXT — it is inert mid-line (measured) — but a line
			// terminator inside the entry is what promotes it to a block.
			var entries []string
			for line := range strings.SplitSeq(got, "\n") {
				if strings.HasPrefix(line, "- ") {
					entries = append(entries, line)
				}
			}
			if len(entries) != 1 {
				t.Fatalf("want exactly one entry line, got %d — the subject split the item:\n%q", len(entries), got)
			}
			const want = "- 🐛 **parser:** fix the thing ## Fabricated Section (8888888)"
			if entries[0] != want {
				t.Errorf("subject not flattened:\n got %q\nwant %q", entries[0], want)
			}
		})
	}
}

// TestRenderNeutralizesMarkup pins the t-j0c6 fix at the notes sink. Every case
// is a shape measured against GitHub's renderer on 2026-07-21, and the first
// three are not hypothetical attacks — they are subjects this fleet has already
// shipped, whose words GitHub's sanitizer DELETED from the release body.
func TestRenderNeutralizesMarkup(t *testing.T) {
	table := loadTable(t)
	cases := []struct {
		name    string
		subject string
		want    string
	}{
		{
			// Measured before the fix: "MUI  + " — <Chip> is deleted outright
			// and <kbd> renders as a live tag that leaks past the </li>.
			name:    "a tag GitHub deletes, beside one it keeps",
			subject: "ThemedChip — MUI <Chip> + <kbd> compact token",
			want:    `- ✨ ThemedChip — MUI \<Chip> + \<kbd> compact token (7777777)`,
		},
		{
			// Measured before the fix: "unwrap Optional before NSNull check".
			name:    "a generic type vanishes today",
			subject: "unwrap Optional<Any> before NSNull check",
			want:    `- ✨ unwrap Optional\<Any> before NSNull check (7777777)`,
		},
		{
			// Measured before the fix: the <h1> escaped the preview's
			// <details> block and landed at the top of the PR comment.
			name:    "a closing tag breaking out of its container",
			subject: "speed up </details><h1>OWNED</h1> the loop",
			want:    `- ✨ speed up \</details>\<h1>OWNED\</h1> the loop (7777777)`,
		},
		{
			// Measured before the fix: a live <img>, fetched through camo.
			name:    "a remote-image beacon",
			subject: `add a tracker <img src="https://evil.example/p.png" width=0>`,
			want:    `- ✨ add a tracker \<img src="https\://evil.example/p.png" width=0> (7777777)`,
		},
		{
			name:    "an arbitrary link",
			subject: "see [x](http://evil.example) for details",
			want:    `- ✨ see \[x](http\://evil.example) for details (7777777)`,
		},
		{
			// The half a '<' rule cannot reach: measured, a bare URL is
			// autolinked with no angle bracket anywhere in the input.
			name:    "a bare URL",
			subject: "read http://evil.example/x for details",
			want:    `- ✨ read http\://evil.example/x for details (7777777)`,
		},
		{
			// No fence can reach this one: the reference resolves to an
			// at-sign AFTER inline parsing, so there is no at-sign in the
			// source for the mention pattern to find. Measured LIVE before.
			name:    "a character reference that renders as a mention",
			subject: "pin callers to &#64;v1",
			want:    `- ✨ pin callers to \&#64;v1 (7777777)`,
		},
		{
			// The approved policy: a subject stays readable. Measured
			// byte-identical at GitHub before and after the fix.
			name:    "code spans and emphasis keep working",
			subject: "keep `code spans` and *emphasis* working",
			want:    "- ✨ keep `code spans` and *emphasis* working (7777777)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sections, err := Group([]parser.Commit{
				{Gitmoji: ":sparkles:", Subject: c.subject, SHA: sha("7")},
			}, table)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			got, err := Render(sections)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("subject not neutralized:\n--- got ---\n%s\n--- want the line ---\n%s", got, c.want)
			}
		})
	}
}

// TestRenderNeutralizesTheScope pins the other half of t-j0c6: the scope is a
// plain-text field, not prose. It can only carry markup through the LEGACY token
// grammar — the canonical scope slot is lowercase kebab-case, while the legacy
// slot is [^()]+ — and measured, ":bug: fix(<img src=x>): …" lints clean today
// and put a live tag in a release body.
//
// The hyphen case is the trap this pins from the other side: '-' is the one
// punctuation byte a well-formed canonical scope can carry, so escaping it would
// write a backslash into essentially every scoped line in the fleet for zero
// rendered difference. Without a hyphenated scope here, a later "escape every
// punctuation byte" simplification would pass every other test in this file.
func TestRenderNeutralizesTheScope(t *testing.T) {
	table := loadTable(t)
	cases := []struct{ name, scope, want string }{
		{"a canonical scope is untouched", "parser", "- ✨ **parser:** ship it (7777777)"},
		{"a hyphenated scope keeps its hyphens", "grid-1f-4", "- ✨ **grid-1f-4:** ship it (7777777)"},
		{"a raw tag in a legacy scope", "<img src=x>", `- ✨ **\<img src\=x\>:** ship it (7777777)`},
		{"a link in a legacy scope", "[x](http://evil)", `- ✨ **\[x\]\(http\:\/\/evil\):** ship it (7777777)`},
		{"a comma-separated legacy scope", "ThemeKit,prism", `- ✨ **ThemeKit\,prism:** ship it (7777777)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sections, err := Group([]parser.Commit{
				{Gitmoji: ":sparkles:", Scope: c.scope, Subject: "ship it", SHA: sha("7")},
			}, table)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			// Group must keep the RAW scope: the JSON surface is a machine
			// contract and escaping belongs to the render, not to the model.
			if got := sections[0].Entries[0].Scope; got != c.scope {
				t.Errorf("Group stored an escaped scope %q; the model must carry the raw value %q", got, c.scope)
			}
			got, err := Render(sections)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("scope not rendered as plain text:\n--- got ---\n%s\n--- want the line ---\n%s", got, c.want)
			}
		})
	}
}

// TestRenderClosesThePhantomSpan pins the t-dwra fix end to end, at the sink
// where it mattered — and it is the case that decided the shape of the whole
// change, so it is pinned by name rather than left to the unit tests.
//
// codeSpans knows only backticks. A construct that outranks one consumes it, so
// the NEXT backtick pairs with a later one and glyph reports a span GitHub never
// formed. A mention inside that phantom reads as already-inert and is left raw.
// Before this change the four subjects below came out of entryLine BYTE FOR BYTE
// — no fence, no escape — and GitHub linked @octocat in every one of them.
//
// Measured 2026-07-21, the rendered notes line before and after:
//
//	before  fix <http://a/`x> so @octocat can `see` it
//	        -> … <a class="user-mention" href="…/octocat">@octocat</a> …   LIVE
//	after   fix \<http\://a/`x> so @octocat can `see` it
//	        -> fix &lt;http://a/<code>x&gt; so @octocat can </code>see` it  inert
//
// The second line is the whole argument for killing the construct instead of
// parsing it: with the autolink dead the two backticks pair for real, so the
// PHANTOM SPAN BECOMES A REAL ONE and GitHub itself puts the at-sign inside
// <code>. glyph's belief stopped being wrong because the world changed to match
// it, not because the model got cleverer.
//
// The third case is why no amount of cleverness would have been enough: the
// backtick is eaten by a link DESTINATION, which an autolink-and-raw-HTML
// scanner does not model either.
func TestRenderClosesThePhantomSpan(t *testing.T) {
	table := loadTable(t)
	cases := []struct{ name, subject, want string }{
		{
			name:    "a URI autolink swallows the opening backtick",
			subject: "fix <http://a/`x> so @octocat can `see` it",
			want:    "- 🐛 fix \\<http\\://a/`x> so @octocat can `see` it (8888888)",
		},
		{
			name:    "a BARE url does it with no angle bracket at all",
			subject: "fix http://a/`x so @octocat can `see` it",
			want:    "- 🐛 fix http\\://a/`x so @octocat can `see` it (8888888)",
		},
		{
			name:    "a link destination does it too",
			subject: "see [a](b`c) @octocat `d` end",
			want:    "- 🐛 see \\[a](b`c) @octocat `d` end (8888888)",
		},
		{
			name:    "a raw tag's quoted attribute does it too",
			subject: "fix <i title=\"`\"> so @octocat can `see` it",
			want:    "- 🐛 fix \\<i title=\"`\"> so @octocat can `see` it (8888888)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sections, err := Group([]parser.Commit{
				{Gitmoji: ":bug:", Subject: c.subject, SHA: sha("8")},
			}, table)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			got, err := Render(sections)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("the phantom span is still open:\n--- got ---\n%s\n--- want the line ---\n%s", got, c.want)
			}
			// The premise, stated so the test cannot quietly stop testing
			// anything: the construct that stole the backtick must be gone.
			line := c.want
			if strings.Contains(line, "://") && !strings.Contains(line, `\://`) {
				t.Errorf("a live scheme survived in %q", line)
			}
		})
	}
}
