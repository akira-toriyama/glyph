package walk

import (
	"strings"
	"testing"
)

// TestShortfallNamesAnUnreadListingWithoutAPull: the PR side's shape — no
// pull, no escapes — renders bare shas joined by commas, as preview's caveat
// always has, and either unread listing makes the walk incomplete.
func TestShortfallNamesAnUnreadListingWithoutAPull(t *testing.T) {
	facts := Facts{filesCapped: []unreadListing{{sha: "h1"}, {sha: "h2"}}}
	if facts.Complete() {
		t.Fatalf("a walk with a capped file listing reports itself complete")
	}
	if s := facts.Shortfall("o", "r"); !strings.Contains(s, "maximum 3000 files") || !strings.HasSuffix(s, "(h1, h2)") {
		t.Fatalf("shortfall does not name the cap and the bare shas: %q", s)
	}
	if (Facts{filesUnknown: []unreadListing{{sha: "c1"}}}).Complete() {
		t.Fatalf("a walk with a file listing GitHub answered 422 for reports itself complete")
	}
}
