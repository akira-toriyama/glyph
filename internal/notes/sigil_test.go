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
	cfg, err := config.Load([]byte("schema = 1\n[[patterns]]\npattern = '^(?P<semver_sigil>.) (?P<subject>.+)'\n[note]\nline = '- $subject'\n"))
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
line = '- $scope $subject $pr @$author $hash'
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
	if strings.Contains(first, "$") {
		t.Errorf("unresolved placeholders must render empty, got %q", first)
	}
	second := sections[0].Lines[1]
	if strings.Contains(second, "#") {
		t.Errorf("a pull-less commit should substitute $pr as empty, got %q", second)
	}
}

// TestRenderLineOptionalSpan asserts BOTH arms of one template, because
// either alone is vacuous: with only the empty arm the test passes when the
// span feature is deleted outright, and with only the populated arm it passes
// when Optional is ignored and every span always renders. Together they pin
// the defect that shipped: the line "- add the demo feature () @akira-toriyama"
// with the parens around an empty $pr, for every commit that reached main
// without a merged pull — which is every commit the --range walk sees.
func TestRenderLineOptionalSpan(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 61, Author: "akira-toriyama", Message: ":bug:~ fix the demo crash"},
		{SHA: "bbbbbbbbbbbb", Pull: 0, Author: "akira-toriyama", Message: ":bug:~ fix it again by direct push"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 1 || len(sections[0].Lines) != 2 {
		t.Fatalf("sections = %+v", sections)
	}
	if got, want := sections[0].Lines[0], "- fix the demo crash (#61) @akira-toriyama"; got != want {
		t.Errorf("a commit WITH a pull must cite it:\n got %q\nwant %q", got, want)
	}
	if got, want := sections[0].Lines[1], "- fix it again by direct push @akira-toriyama"; got != want {
		t.Errorf("a commit with NO pull must lose the parens and the space with them:\n got %q\nwant %q", got, want)
	}
}

// TestGroupSigilsMultiCommitPullRepeatsTheCitation pins, for the first time,
// what a multi-commit pull looks like in the notes: one line PER COMMIT, each
// citing the SAME (#N). This is the mechanical consequence of renderLine
// binding $pr per commit, and until t-njmw nothing asserted it — the shape
// could have changed to dedup-by-pull (or drop the citation) without a test
// noticing. Whether the repetition is the *right* rendering is a separate,
// unratified question; this test freezes the current answer so a change to it
// has to be a decision.
func TestGroupSigilsMultiCommitPullRepeatsTheCitation(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 61, Author: "akira", Message: ":bug:~ fix the first thing"},
		{SHA: "bbbbbbbbbbbb", Pull: 61, Author: "akira", Message: ":bug:~ fix the second thing"},
		{SHA: "cccccccccccc", Pull: 61, Author: "akira", Message: ":bug:~ fix the third thing"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 1 || len(sections[0].Lines) != 3 {
		t.Fatalf("sections = %+v, want one Fixes section with three lines", sections)
	}
	want := []string{
		"- fix the first thing (#61) @akira",
		"- fix the second thing (#61) @akira",
		"- fix the third thing (#61) @akira",
	}
	for i, w := range want {
		if sections[0].Lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, sections[0].Lines[i], w)
		}
	}
}

// TestRenderLineBuiltinsOutrankGroups: a pattern free to name its groups can
// name one $pr, and the built-in still wins — so a subject-supplied group can
// never dress itself as the pull the walk resolved. The span reads the same
// resolution, so the built-in also decides whether the span renders.
func TestRenderLineBuiltinsOutrankGroups(t *testing.T) {
	cfg, err := config.Load([]byte(`schema = 1
[[patterns]]
pattern = '^(?P<semver_sigil>[=~^!]) (?P<pr>\S+) (?P<subject>.+)'
[note]
line = '- $subject$[ ($pr)]'
[[note.sections]]
semver = "patch"
title = "Fixes"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 61, Author: "akira", Message: "~ #999 fix the thing"},
		{SHA: "bbbbbbbbbbbb", Pull: 0, Author: "akira", Message: "~ #999 fix the other thing"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if got, want := sections[0].Lines[0], "- fix the thing (#61)"; got != want {
		t.Errorf("the built-in $pr must outrank the pattern group of the same name: got %q, want %q", got, want)
	}
	if got, want := sections[0].Lines[1], "- fix the other thing"; got != want {
		t.Errorf("the span must read the BUILT-IN, so a group named pr cannot keep it alive: got %q, want %q", got, want)
	}
}

// TestRenderLineNeutralizesMentions pins the safety property v1 lines carry
// AND its one ratified exemption (2026-08-17): a subject cannot page someone
// from a release body — the mention fence runs over the assembled v2 line —
// while the built-in $author, rendered through the template's own "@", goes
// out as a live mention. Crediting the contributor is the point; the subject's
// strangers are not.
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
	if !strings.HasSuffix(line, " @akira") {
		t.Errorf("the author credit must be a live mention, not fenced: %q", line)
	}
}

// TestRenderLineFencesFreeTextAuthor pins the gate on the exemption: the
// author value is git's free-text %an, and only a whole handle-shaped value
// may go live. "Akira Toriyama" would page the stranger @Akira if it slipped
// through raw — the exact t-hykw failure the fence exists for.
func TestRenderLineFencesFreeTextAuthor(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 1, Author: "Akira Toriyama", Message: ":bug:~ fix a thing"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	line := sections[0].Lines[0]
	if strings.Contains(line, "@Akira") && !strings.Contains(line, "`@Akira`") {
		t.Errorf("a free-text author name rendered as a live mention: %q", line)
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

// TestGroupSigilsPromoteLandsInBreaking is the standing detector for the one
// way a fifth level word could have been introduced without anything failing
// to compile: a section filters on `semver = "major"`, and a promoting commit
// that classified as anything else would simply not be written into the
// release body — no error, no empty section, just a missing line in the notes
// for the commit that decided the version.
func TestGroupSigilsPromoteLandsInBreaking(t *testing.T) {
	cfg := sigilCfg(t)
	sections, err := GroupSigils([]SigilCommit{
		{SHA: "aaaaaaaaaaaa", Pull: 20, Author: "akira", Message: ":rocket:% call it 1.0"},
	}, cfg)
	if err != nil {
		t.Fatalf("GroupSigils: %v", err)
	}
	if len(sections) != 1 || sections[0].Title != "Breaking Changes" {
		t.Fatalf("sections = %+v, want one Breaking Changes section", sections)
	}
	if len(sections[0].Lines) != 1 || !strings.Contains(sections[0].Lines[0], "call it 1.0") {
		t.Fatalf("lines = %+v, want the promoting commit", sections[0].Lines)
	}
}
