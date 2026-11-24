package draftplan

import (
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// TestComposeBodyCarriesTheHandRegion pins the t-qgps contract where the body
// is composed: everything above the marker in the kept draft's body survives
// byte-for-byte (CRLF included — a web edit stores CRLF) and everything below
// is regenerated; a body without the marker contributes nothing; a draft the
// plan creates opens with the marker (mutation row draft-hand-region-discarded).
// The marker counts only on a line of its own, white space around it allowed:
// a machine line quoting it — a commit subject the notes rendered, t-cjck — is
// no boundary, so a body whose marker line a human deleted still contributes
// nothing (mutation row draft-hand-marker-matched-inside-a-line), while a
// marker line a human left padded is still the boundary.
func TestComposeBodyCarriesTheHandRegion(t *testing.T) {
	const generated = "## Fixes\n\n- fix a crash\n"
	hand := "the exit codes changed, fix your gates\r\n\r\n"
	cases := []struct {
		name string
		keep *Draft
		want string
	}{
		{"kept, with a hand region", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "\n\n## Fixes\n\n- stale machine text\n"}, hand + handMarker + "\n\n" + generated},
		{"kept, no marker", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: "## Fixes\n\n- old body with no marker\n"}, handMarker + "\n\n" + generated},
		{"kept, marker as the first line", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: handMarker + "\n\n## Fixes\n\n- stale machine text\n"}, handMarker + "\n\n" + generated},
		{"kept, marker as the last line", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line ending in CRLF", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "\r\n\r\n## Fixes\r\n"}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line deleted, a subject quoting it", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: "## Fixes\n\n- :bug:~ stop the notes from printing \\" + handMarker + " twice\n"}, handMarker + "\n\n" + generated},
		{"kept, a hand line quoting it above the marker line", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: "keep " + handMarker + " above\n" + handMarker + "\n\nold"}, "keep " + handMarker + " above\n" + handMarker + "\n\n" + generated},
		{"kept, marker line with trailing spaces", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "  \n\nold"}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line with a trailing tab", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "\t\n\nold"}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line with a leading space", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + " " + handMarker + "\n\nold"}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line ending in two CRs", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "\r\r\n\nold"}, hand + handMarker + "\n\n" + generated},
		{"kept, marker line deleted, a bullet of the marker alone", &Draft{ID: 11, TagName: "v0.1.1", Draft: true, Body: "## Fixes\n\n- " + handMarker + "\nold"}, handMarker + "\n\n" + generated},
		{"created", nil, handMarker + "\n\n" + generated},
	}
	for _, c := range cases {
		if got := ComposeBody(c.keep, generated); got != c.want {
			t.Errorf("%s: ComposeBody = %q, want %q", c.name, got, c.want)
		}
	}

	// The body read is the one the plan kept: a retag keeps the draft, and
	// its hand region with it.
	plan := PlanDraft(config.Line{}, bump.LevelPatch, "v0.1.2", false, []Draft{{ID: 11, TagName: "v0.1.1", Draft: true, Body: hand + handMarker + "\n\nold"}})
	if got, want := ComposeBody(plan.Keep, generated), hand+handMarker+"\n\n"+generated; got != want {
		t.Errorf("ComposeBody(plan.Keep) = %q, want %q — the kept draft's hand region", got, want)
	}
}
