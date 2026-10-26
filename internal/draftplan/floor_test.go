package draftplan

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
)

func TestHighestPublishedIgnoresUnparseableTags(t *testing.T) {
	releases := []Draft{
		{ID: 1, TagName: "nightly", Draft: false},
		{ID: 2, TagName: "0.9.9", Draft: false},       // no v prefix — rejected before ParseVersion
		{ID: 6, TagName: "v2", Draft: false},          // v-prefixed, unparseable — reaches ParseVersion
		{ID: 7, TagName: "v1.0.0-rc.1", Draft: false}, // v-prefixed pre-release — likewise
		{ID: 3, TagName: "v0.5.0", Draft: false},
		{ID: 4, TagName: "v0.4.0", Draft: false},
		{ID: 5, TagName: "v9.9.9", Draft: true}, // a draft is no floor
		{ID: 8, TagName: "v8.8", Draft: true},   // ...and an unparseable one is doubly not
	}
	floor, ok := highestPublished(config.Line{}, releases)
	if !ok || floor.String() != "v0.5.0" {
		t.Fatalf("floor = %v ok=%v, want v0.5.0 over the parseable published set", floor, ok)
	}
	if err := CheckPublishedFloor(config.Line{}, bump.Version{Minor: 5}, releases); err == nil {
		t.Fatalf("v0.5.0 equals the floor and must be refused (STRICTLY greater)")
	}
	if err := CheckPublishedFloor(config.Line{}, bump.Version{Minor: 5, Patch: 1}, releases); err != nil {
		t.Fatalf("v0.5.1 clears the floor, got %v", err)
	}

	// Named rather than sliced by index: this set must keep meaning "every
	// published release here is unparseable", through both rejection steps, and
	// a releases[:N] would silently start including a house tag the moment
	// somebody inserts one above the cut.
	foreignOnly := []Draft{
		{ID: 1, TagName: "nightly", Draft: false},
		{ID: 2, TagName: "0.9.9", Draft: false},
		{ID: 6, TagName: "v2", Draft: false},
		{ID: 7, TagName: "v1.0.0-rc.1", Draft: false},
	}
	if _, ok := highestPublished(config.Line{}, foreignOnly); ok {
		t.Fatalf("foreign-only published releases must yield no floor")
	}
	if err := CheckPublishedFloor(config.Line{}, bump.Version{Patch: 1}, foreignOnly); err != nil {
		t.Fatalf("with no floor any version passes, got %v", err)
	}
}

// TestHighestPublishedIsPerLine pins the published floor's line boundary
// (DESIGN §4.1): a published release on one line is no floor under another —
// a bare v2.0.0 must not refuse haiku/v0.2.0, and a haiku/ release must not
// refuse the bare line or curry/. Read across lines, every package's first
// release would collide with the root's history.
func TestHighestPublishedIsPerLine(t *testing.T) {
	rels := []Draft{
		{ID: 1, TagName: "v2.0.0"},
		{ID: 2, TagName: "haiku/v1.5.0"},
		{ID: 3, TagName: "haiku/v1.4.0"},
		{ID: 4, TagName: "haiku/sub/v9.0.0"},
		{ID: 5, TagName: "curry/v0.9.0", Draft: true}, // a draft is never a floor
	}
	cases := map[string]struct {
		floor bump.Version
		ok    bool
	}{
		"":           {bump.Version{Major: 2}, true},
		"haiku/":     {bump.Version{Major: 1, Minor: 5}, true},
		"haiku/sub/": {bump.Version{Major: 9}, true},
		"curry/":     {bump.Version{}, false},
	}
	for prefix, want := range cases {
		floor, ok := highestPublished(config.Line{Prefix: prefix}, rels)
		if ok != want.ok || floor != want.floor {
			t.Errorf("highestPublished(%q) = %v, %t; want %v, %t", prefix, floor, ok, want.floor, want.ok)
		}
	}
	if err := CheckPublishedFloor(config.Line{Prefix: "curry/"}, bump.Version{Minor: 1}, rels); err != nil {
		t.Errorf("curry/v0.1.0 under a published bare v2.0.0 and haiku/v1.5.0 must pass — different lines, no floor on curry/; got %v", err)
	}
	if err := CheckPublishedFloor(config.Line{}, bump.Version{Minor: 5}, rels); err == nil {
		t.Errorf("bare v0.5.0 under a published bare v2.0.0 must be refused — same line")
	}
	if err := CheckPublishedFloor(config.Line{Prefix: "haiku/"}, bump.Version{Major: 1, Minor: 5}, rels); err == nil || !strings.Contains(err.Error(), "haiku/v1.5.0") {
		t.Errorf("haiku/v1.5.0 under a published haiku/v1.5.0 must be refused naming the line's tag; got %v", err)
	}
}

// TestCheckPublishedFloorRefusesAtFour: a version at or below the line's
// published floor is the deadlock refusal, exit 4 — nothing broken
// underneath, no retry clears it — and it names both tags as the line spells
// them.
func TestCheckPublishedFloorRefusesAtFour(t *testing.T) {
	err := CheckPublishedFloor(config.Line{Prefix: "haiku/"}, bump.Version{Major: 1, Minor: 4}, []Draft{{ID: 2, TagName: "haiku/v1.5.0"}})
	ce, ok := errors.AsType[*core.Error](err)
	if !ok || ce.Code != core.CodeAPI {
		t.Fatalf("err = %v, want *core.Error with CodeAPI (exit 4)", err)
	}
	if want := "computed version haiku/v1.4.0 is not greater than the latest published release haiku/v1.5.0"; !strings.Contains(err.Error(), want) {
		t.Errorf("refusal %q is missing %q", err, want)
	}
}
