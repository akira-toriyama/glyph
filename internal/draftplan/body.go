package draftplan

import "strings"

// handMarker is the line that splits a rolling draft's body into a hand
// region (above) and a machine region (below). It is an HTML comment on
// purpose — GitHub renders it as nothing, so a published release carries no
// trace of it — and its text is the contract, stated in the one place a human
// is when the contract matters: the draft's edit box. The rolling draft is
// the only place release prose can be written ("the exit codes changed, fix
// your gates"), and before this marker existed the next push rewrote the
// whole body and destroyed that prose with no warning (t-qgps).
//
// Everything ABOVE the marker is preserved byte-for-byte across every upsert;
// everything below is glyph's and is rewritten every time. An edit below the
// marker is still lost — that boundary has to exist for the machine region to
// stay truthful — but now the boundary is printed where the human is typing,
// instead of being an unwritten rule they learn by losing work.
const handMarker = "<!-- glyph: notes written ABOVE this line survive every push; glyph rewrites everything BELOW it -->"

// ComposeBody assembles the draft body the upsert writes: the hand region of
// the kept draft's body (everything above the marker, byte-for-byte —
// including the CRLF line endings a web edit stores), the marker, then the
// freshly generated machine region. keep is the plan's Keep, nil when the
// plan creates a draft, which has no hand region. A body with no marker — a
// pre-marker draft, or one where a human deleted the marker line —
// contributes no hand region: glyph cannot tell that body's hand-written
// lines from its own stale output, and guessing would resurrect old machine
// text as if a human had written it.
func ComposeBody(keep *Draft, generated string) string {
	var existing string
	if keep != nil {
		existing = keep.Body
	}
	hand, _, found := strings.Cut(existing, handMarker)
	if !found {
		hand = ""
	}
	return hand + handMarker + "\n\n" + generated
}
