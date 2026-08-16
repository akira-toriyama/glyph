package notes

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
)

func sigilCfg(t *testing.T) *config.Config {
	t.Helper()
	data, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("Preset(gemoji) missing")
	}
	cfg, err := config.Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestGroupSigilsAxes(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 10, Author: "akira", Message: ":boom:! drop the flag"},
		{SHA: "bbbbbbbbbbbb", Pull: 11, Author: "akira", Message: ":sparkles:(cli)^ add a thing"},
		{SHA: "cccccccccccc", Pull: 12, Author: "akira", Message: ":bug:~ fix a thing"},
		{SHA: "dddddddddddd", Pull: 0, Author: "akira", Message: ":memo:= reword docs"},
		{SHA: "eeeeeeeeeeee", Pull: 13, Author: "dependabot[bot]", Message: "Bump x from 1 to 2"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	titles := make([]string, 0, len(sections))
	for _, s := range sections {
		titles = append(titles, s.Title)
	}
	want := []string{"Breaking Changes", "Features", "Fixes", "Dependencies"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %v, want %v (config order, empty sections omitted — no none-section is configured so the := commit appears nowhere)", titles, want)
	}
	if len(sections[0].Lines) != 1 || !strings.Contains(sections[0].Lines[0], "drop the flag") {
		t.Errorf("Breaking Changes = %q", sections[0].Lines)
	}
	if !strings.Contains(sections[1].Lines[0], "#11") {
		t.Errorf("Features line should cite the pull: %q", sections[1].Lines[0])
	}
	deps := sections[3].Lines
	if len(deps) != 1 || !strings.Contains(deps[0], "Bump x from 1 to 2") {
		t.Errorf("the unmatched bot commit should render its raw first line in the author section: %q", deps)
	}
}

// TestGroupSigilsDuplicates asserts the ratified stance that a commit lands
// in EVERY section whose filter matches it: deduplicating would make section
// order silently decide which section wins.
func TestGroupSigilsDuplicates(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 7, Author: "dependabot[bot]", Message: ":boom:! bot ships a breaking bump"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 2 || sections[0].Title != "Breaking Changes" || sections[1].Title != "Dependencies" {
		t.Fatalf("a breaking dependabot commit must land in both its semver and its author section, got %+v", sections)
	}
	if sections[0].Lines[0] != sections[1].Lines[0] {
		t.Errorf("both sections should carry the same rendered line")
	}
}

// TestGroupSigilsSkipIsTotal asserts what separates skip from
// exclude_authors: a skip-pattern commit is in NO section, even one whose
// author filter would catch it.
func TestGroupSigilsSkipIsTotal(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Author: "dependabot[bot]", Message: "Merge pull request #9 from x"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 0 {
		t.Fatalf("a skip-pattern commit must appear in no section, got %+v", sections)
	}
}

func TestGroupSigilsPropagatesConfigBug(t *testing.T) {
	cfg, err := config.Load([]byte("schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>.) '\n[note]\nline = '- $subject'\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = GroupSigils([]SigilCommit{{SHA: "abc9", Author: "akira", Message: "z boom"}}, cfg)
	if err == nil {
		t.Fatalf("GroupSigils accepted an unparseable sigil capture")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.CodeLint {
		t.Fatalf("err = %v, want *core.Error with CodeLint", err)
	}
}

func TestRenderLineTemplate(t *testing.T) {
	cfg, err := config.Load([]byte(`schema = 1
[[patterns]]
pattern = '^(\((?P<scope>[a-z]+)\) )?(?P<semver_sigil>[=~^!]) (?P<subject>.+)'
[note]
line = '- $scope $subject $pr @$author $hash $nosuch'
[[note.sections]]
semver = "patch"
title = "Fixes"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "0123456789abcdef", Pull: 42, Author: "akira", Message: "(cli) ~ fix the thing"},
		{SHA: "fedcba9876543210", Pull: 0, Author: "akira", Message: "~ fix without scope or pull"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 1 || len(sections[0].Lines) != 2 {
		t.Fatalf("sections = %+v", sections)
	}
	first := sections[0].Lines[0]
	for _, want := range []string{"cli", "fix the thing", "#42", "akira", "0123456"} {
		if !strings.Contains(first, want) {
			t.Errorf("line %q should contain %q", first, want)
		}
	}
	if strings.Contains(first, "$") || strings.Contains(first, "nosuch") {
		t.Errorf("unresolved placeholders must render empty, got %q", first)
	}
	second := sections[0].Lines[1]
	if strings.Contains(second, "#") {
		t.Errorf("a pull-less commit should substitute $pr as empty, got %q", second)
	}
}

// TestRenderLineNeutralizesMentions pins the safety property v1 lines carry:
// a subject cannot page someone from a release body — the mention fence runs
// over the assembled v2 line too.
func TestRenderLineNeutralizesMentions(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 1, Author: "akira", Message: ":bug:~ thank @someone for the report"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	line := sections[0].Lines[0]
	if strings.Contains(line, "@someone") && !strings.Contains(line, "`@someone`") {
		t.Errorf("a bare @mention survived into the rendered line: %q", line)
	}
}

func TestRenderSigils(t *testing.T) {
	got := RenderSigils([]SigilSection{
		{Title: "Breaking Changes", Lines: []string{"- one", "- two"}},
		{Title: "Fixes", Lines: []string{"- three"}},
	})
	want := "## Breaking Changes\n\n- one\n- two\n\n## Fixes\n\n- three\n"
	if got != want {
		t.Errorf("RenderSigils = %q, want %q", got, want)
	}
	if RenderSigils(nil) != "" {
		t.Errorf("no sections must render to the empty string")
	}
}
