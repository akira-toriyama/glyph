package bump

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// TestNextOnStepsOnTheLine: the bare line and a free line with no locked
// sibling step exactly as Next, a none verdict holds the version, a locked
// line steps within its major, and a locked line's first release is its
// major's floor whatever the level (DESIGN §4.1).
func TestNextOnStepsOnTheLine(t *testing.T) {
	v2 := config.Line{Prefix: "pubsub/", Major: 2}
	cases := []struct {
		name    string
		line    config.Line
		current Version
		dec     Decision
		want    Version
	}{
		{"bare line, the 0.x major rule", config.Line{}, Version{0, 5, 3}, Decision{Level: LevelMajor}, Version{0, 6, 0}},
		{"free line, no locked sibling, steps its major", config.Line{Prefix: "pubsub/"}, Version{1, 9, 1}, Decision{Level: LevelMajor}, Version{2, 0, 0}},
		{"none holds the version", v2, Version{2, 7, 0}, Decision{Level: LevelNone}, Version{2, 7, 0}},
		{"locked line steps within its major", v2, Version{2, 7, 0}, Decision{Level: LevelMinor}, Version{2, 8, 0}},
		{"locked line's first release is its major", v2, Version{}, Decision{Level: LevelPatch}, Version{2, 0, 0}},
	}
	for _, c := range cases {
		got, err := NextOn(c.line, c.current, c.dec)
		if err != nil || got != c.want {
			t.Errorf("%s: NextOn(%+v, %s, %+v) = %s, %v; want %s", c.name, c.line, c.current, c.dec, got, err, c.want)
		}
	}
}

// TestNextOnRefusesAStepOffTheLine: a `!` on the locked pubsub/v2 line would
// tag pubsub/v3.0.0, which no module claims, and a `!` on the free pubsub/
// line beside it would tag the v2 line's own floor — both refused in the
// wedge's class, exit 3, naming the tag that was not cut (mutation row
// locked-line-steps-past-its-major).
func TestNextOnRefusesAStepOffTheLine(t *testing.T) {
	cases := []struct {
		name    string
		line    config.Line
		current Version
		want    []string
	}{
		{"locked line", config.Line{Prefix: "pubsub/", Major: 2}, Version{2, 7, 0}, []string{"pubsub/v3.0.0", "off its line", "belongs in pubsub/v3", "DESIGN §4.1"}},
		{"free line beside a locked one", config.Line{Prefix: "pubsub/", Claimed: []int{2}}, Version{1, 9, 1}, []string{"pubsub/v2.0.0", "on the pubsub/v2.* line", "declares as its own package", "DESIGN §4.1"}},
	}
	for _, c := range cases {
		got, err := NextOn(c.line, c.current, Decision{Level: LevelMajor})
		ce, ok := errors.AsType[*core.Error](err)
		if !ok || ce.Code != core.CodeLint {
			t.Fatalf("%s: NextOn = %s, %v; want *core.Error with CodeLint (exit 3)", c.name, got, err)
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q is missing %q", c.name, err, want)
			}
		}
	}
}
