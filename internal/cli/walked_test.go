package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/parser"
)

// TestNotesCommitsCiteThePermanentAddress pins the t-xxhj decision at the seam
// that decides it: the walk hands the notes a citation, and which half of it
// survives depends on whether the sha is a *landed* identity (glossary). A
// footprint-less commit — the squash arm — has its sha BLANKED, because the
// listing's sha exists on no branch and was published anyway (glance v1.0.0
// cited two of them); its pull is the one address that outlives the squash. A
// landed commit keeps the sha beside the pull, and the fallback path keeps its
// sha with no pull — the pull is ADDED where one is known, never put in the
// sha's place, or one pull's N commits would collapse into N identical lines.
func TestNotesCommitsCiteThePermanentAddress(t *testing.T) {
	landedSHA := strings.Repeat("a", 40)
	squashedSHA := strings.Repeat("b", 40)
	directSHA := strings.Repeat("c", 40)
	got := notesCommits([]walked{
		{Commit: parser.Commit{Gitmoji: ":bug:", Subject: "landed by the merge button", SHA: landedSHA}, Pull: 7, Landed: true},
		{Commit: parser.Commit{Gitmoji: ":bug:", Subject: "squash-expanded", SHA: squashedSHA}, Pull: 7, Landed: false},
		{Commit: parser.Commit{Gitmoji: ":bug:", Subject: "direct push", SHA: directSHA}, Landed: true},
	})
	if len(got) != 3 {
		t.Fatalf("notesCommits returned %d commits, want 3", len(got))
	}
	if got[0].SHA != landedSHA || got[0].Pull != 7 {
		t.Errorf("a landed pull commit must keep its on-branch sha beside the pull, got sha=%q pull=%d", got[0].SHA, got[0].Pull)
	}
	if got[1].SHA != "" || got[1].Pull != 7 {
		t.Errorf("a footprint-less commit's sha exists on no branch; it must be blanked and the pull kept, got sha=%q pull=%d", got[1].SHA, got[1].Pull)
	}
	if got[2].SHA != directSHA || got[2].Pull != 0 {
		t.Errorf("a fallback commit keeps its sha and has no pull to cite, got sha=%q pull=%d", got[2].SHA, got[2].Pull)
	}
}
