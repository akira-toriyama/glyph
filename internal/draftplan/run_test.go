package draftplan

import (
	"slices"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// writes spells a run as "<kind> <tag>" per write, the shape the order tests
// compare.
func writes(ws []Write) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		kind := map[Kind]string{Upsert: "upsert", Residual: "residual", Stale: "stale"}[w.Kind]
		tag := w.Tag
		if w.Action == ActionDelete {
			tag = w.Draft.TagName
		}
		out = append(out, kind+" "+tag)
	}
	return out
}

// TestPlanRunWritesEveryUpsertBeforeAnyDelete pins the write order DESIGN §4.1
// argues: every line's upsert lands before any draft is deleted, so a write
// that fails leaves nothing destroyed; the residual deletes — a none line's
// whole action — go before the stale ones, so a run an interrupt cuts short
// has spent itself on the verdict first; and within each kind the lines keep
// their order. The kinds travel with the writes, and each write names the
// line it belongs to, which is where its URL lands once written.
func TestPlanRunWritesEveryUpsertBeforeAnyDelete(t *testing.T) {
	haiku := config.Line{Prefix: "haiku/"}
	curry := config.Line{Prefix: "curry/"}
	soup := config.Line{Prefix: "soup/"}
	rice := config.Line{Prefix: "rice/"}
	moving := PlanDraft(haiku, "minor", "haiku/v0.2.0", false, []Draft{d(1, "haiku/v0.1.5"), d(2, "haiku/v0.1.6")})
	none := PlanDraft(curry, "none", "", false, []Draft{d(3, "curry/v0.1.1")})
	held := PlanDraft(soup, "none", "", true, []Draft{d(4, "soup/v0.3.0"), d(6, "soup/v0.2.9")})
	gone := PlanDraft(rice, "none", "", false, []Draft{d(5, "rice/v1.0.1")})
	run := PlanRun([]LinePlan{{Plan: moving, Body: "haiku notes"}, {Plan: none}, {Plan: held, Body: "placeholder"}, {Plan: gone}}, nil)

	want := []string{"upsert haiku/v0.2.0", "upsert soup/Unreleased", "residual curry/v0.1.1", "residual rice/v1.0.1", "stale haiku/v0.1.6", "stale soup/v0.2.9"}
	if got := writes(run); !slices.Equal(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	if run[0].Line != 0 || run[0].Action != ActionUpdate || run[0].Draft == nil || run[0].Draft.ID != 1 || run[0].Body != "haiku notes" {
		t.Errorf("first write = %+v, want haiku's update of draft 1 with its body", run[0])
	}
	if run[1].Line != 2 || run[1].Action != ActionUpdate || run[1].Draft == nil || run[1].Draft.ID != 4 || run[1].Body != "placeholder" {
		t.Errorf("second write = %+v, want soup's placeholder retagging its draft 4", run[1])
	}
	if run[2].Line != 1 || run[2].Draft.ID != 3 || run[3].Line != 3 || run[3].Draft.ID != 5 || run[4].Line != 0 || run[4].Draft.ID != 2 || run[5].Line != 2 || run[5].Draft.ID != 6 {
		t.Errorf("deletes = %+v, want curry's residual 3 and rice's 5, then haiku's stale 2 and soup's 6, each naming its line", run[2:])
	}
	for _, w := range run {
		if w.IsResidue() {
			t.Errorf("write %+v is the residue, want none: no residue was handed in", w)
		}
	}
}

// TestPlanRunResidueTakesTheRunsKind pins t-xz1z's rule for the bare line's
// residue, which is no line's: a stale delete when any line writes a draft,
// the run's whole action — a residual — when none does; last among its kind
// either way, after the lines' own, and recognisable as the residue so the
// run can say what it deleted once the DELETE went.
func TestPlanRunResidueTakesTheRunsKind(t *testing.T) {
	haiku := config.Line{Prefix: "haiku/"}
	residue := []Draft{d(61, "v0.9.0"), d(62, placeholderTag)}
	moving := PlanDraft(haiku, "minor", "haiku/v0.2.0", false, []Draft{d(1, "haiku/v0.1.5"), d(2, "haiku/v0.1.6")})
	beside := PlanRun([]LinePlan{{Plan: moving, Body: "notes"}}, residue)
	if got, want := writes(beside), []string{"upsert haiku/v0.2.0", "stale haiku/v0.1.6", "stale v0.9.0", "stale Unreleased"}; !slices.Equal(got, want) {
		t.Fatalf("beside a write: %q, want %q", got, want)
	}
	if beside[1].IsResidue() || !beside[2].IsResidue() || !beside[3].IsResidue() {
		t.Errorf("beside a write: residue flags = %v %v %v, want the line's own stale unmarked and the residue marked", beside[1].IsResidue(), beside[2].IsResidue(), beside[3].IsResidue())
	}

	none := PlanDraft(haiku, "none", "", false, []Draft{d(1, "haiku/v0.1.5")})
	alone := PlanRun([]LinePlan{{Plan: none}}, residue)
	if got, want := writes(alone), []string{"residual haiku/v0.1.5", "residual v0.9.0", "residual Unreleased"}; !slices.Equal(got, want) {
		t.Fatalf("with no write: %q, want %q", got, want)
	}
	if alone[0].IsResidue() || !alone[1].IsResidue() || !alone[2].IsResidue() {
		t.Errorf("with no write: residue flags = %v %v %v, want the line's own first, unmarked, then the residue marked", alone[0].IsResidue(), alone[1].IsResidue(), alone[2].IsResidue())
	}

	if quiet := PlanRun([]LinePlan{{Plan: PlanDraft(haiku, "none", "", false, nil)}}, nil); len(quiet) != 0 {
		t.Errorf("a none line with nothing to delete and no residue plans %+v, want no write", quiet)
	}
}

// TestResidueIsClaimedOnlyWithoutARootPackage pins what the residue IS: the
// bare FREE line's managed drafts, in a repository that declares packages and
// no root package — the drafts the single line left behind and no line would
// converge again. A declared root-level vN keeps its own major's bare drafts
// (the free line is what config.Config.LineOf leaves over); a declared root
// package makes every bare draft that package's; without packages the bare
// line plans its own. A published bare release is never residue: it is not
// glyph's draft.
func TestResidueIsClaimedOnlyWithoutARootPackage(t *testing.T) {
	releases := []Draft{d(61, "v0.9.0"), d(53, "haiku/v0.2.0"), d(62, placeholderTag), {ID: 70, TagName: "v0.5.0"}, d(63, "nightly"), d(64, "v2.3.0")}
	two := &config.Config{Packages: []config.Package{{Path: "haiku"}, {Path: "curry"}}}
	if got := Residue(two, releases); len(got) != 3 || got[0].ID != 61 || got[1].ID != 62 || got[2].ID != 64 {
		t.Errorf("residue = %+v, want the bare v0.9.0 and v2.3.0 drafts and the bare placeholder, in listing order", got)
	}
	locked := &config.Config{Packages: []config.Package{{Path: "v2", Name: "core"}, {Path: "haiku"}}}
	if got := Residue(locked, releases); len(got) != 2 || got[0].ID != 61 || got[1].ID != 62 {
		t.Errorf("residue beside a root-level v2 line = %+v, want v0.9.0 and the placeholder only: v2.3.0 is that line's draft", got)
	}
	root := &config.Config{Packages: []config.Package{{Path: ".", Name: "core"}, {Path: "haiku"}}}
	if got := Residue(root, releases); got != nil {
		t.Errorf("residue with a root package = %+v, want none: the bare drafts are the root package's", got)
	}
	if got := Residue(&config.Config{}, releases); got != nil {
		t.Errorf("residue without packages = %+v, want none: the bare line is the walk's one line", got)
	}
}
