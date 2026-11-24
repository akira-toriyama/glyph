package draftplan

import "strings"

// handMarker is the line that splits a rolling draft's body into a hand
// region (above) and a machine region (below). It is an HTML comment on
// purpose — GitHub renders it as nothing, so a published release carries no
// trace of it — and its text is the contract, stated in the one place a human
// is when the contract matters: the draft's edit box (DESIGN §4, "The hand
// region").
//
// Everything ABOVE the marker is preserved byte-for-byte across every upsert;
// everything below is glyph's and is rewritten every time. An edit below the
// marker is lost: that boundary has to exist for the machine region to stay
// truthful.
const handMarker = "<!-- glyph: notes written ABOVE this line survive every push; glyph rewrites everything BELOW it -->"

// ComposeBody assembles the draft body the upsert writes: the hand region of
// the kept draft's body (everything above the marker line, byte-for-byte —
// including the CRLF line endings a web edit stores), the marker, then the
// freshly generated machine region. keep is the plan's Keep, nil when the
// plan creates a draft, which has no hand region. A body with no marker — a
// pre-marker draft, or one where a human deleted the marker line —
// contributes no hand region: glyph cannot tell that body's hand-written
// lines from its own stale output, and guessing would resurrect old machine
// text as if a human had written it.
//
// The marker counts only on a line of its own, white space around it allowed
// (a CR before its newline, or padding a human left): the notes render a
// commit subject that quotes the marker inside a bullet, and a substring match
// there would read that machine line as the boundary (t-cjck).
func ComposeBody(keep *Draft, generated string) string {
	var hand string
	if keep != nil {
		hand = handRegion(keep.Body)
	}
	return hand + handMarker + "\n\n" + generated
}

// handRegion returns everything above body's first line that holds the marker
// and nothing but white space, and nothing when no line does.
func handRegion(body string) string {
	at := 0
	for line := range strings.Lines(body) {
		if strings.TrimSpace(line) == handMarker {
			return body[:at]
		}
		at += len(line)
	}
	return ""
}
