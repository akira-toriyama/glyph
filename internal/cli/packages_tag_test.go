package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestBumpPackagesVerdictNamesTheLinesTag: bump's packages row carries the
// line's next TAG beside its bare next, under the key release's row already
// uses, present exactly when next is. A consumer composing it from path and
// next gets it wrong twice: a locked line folds its major into the version
// (pubsub/v2 + v2.1.0 is pubsub/v2.1.0, never pubsub/v2/v2.1.0) and the root
// package's tag is bare (t-c84j, measured 2026-11-04: no `tag` in bump's
// rows while release's carried camp/v0.1.1). A line at none carries neither
// key, so an all-none walk is a full array with no tag in it — the shape
// README's `select(.tag != null)` already reads. Mutation row
// bump-packages-row-drops-the-tag.
func TestBumpPackagesVerdictNamesTheLinesTag(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"pubsub\"\n\n[[packages]]\npath = \"pubsub/v2\"\nname = \"pubsub-v2\"\n\n[[packages]]\npath = \".\"\nname = \"core\"\n",
		map[string]string{"pubsub/p.go": "package pubsub\n", "pubsub/v2/p.go": "package pubsub\n", "README.md": "root\n"})
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "tag", "pubsub/v2.0.0")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.0")
	testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(pubsub-v2)^ start v2 for real", "pubsub/v2/p.go")
	testutil.Touch(t, dir, "akira-toriyama", ":bug:(core)~ fix the readme", "README.md")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump --range --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var raw struct {
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	res := decodeJSON[packagesVerdict](t, stdout)
	if len(res.Packages) != 3 {
		t.Fatalf("want three rows, got %s", stdout)
	}
	pubsub, v2, root := res.Packages[0], res.Packages[1], res.Packages[2]
	if pubsub.Level != "none" || pubsub.Next != "" || pubsub.Tag != "" {
		t.Errorf("the untouched free line must carry neither next nor tag: %+v", pubsub)
	}
	if _, has := raw.Packages[0]["tag"]; has {
		t.Errorf("a line at none must have no tag key at all, as it has no next key: %v", raw.Packages[0])
	}
	if v2.Next != "v2.1.0" || v2.Tag != "pubsub/v2.1.0" {
		t.Errorf("the locked line's row = next %q tag %q, want v2.1.0 and pubsub/v2.1.0 — the major folds into the version, the path does not repeat it", v2.Next, v2.Tag)
	}
	if root.Next != "v0.1.1" || root.Tag != "v0.1.1" {
		t.Errorf("the root package's row = next %q tag %q, want both bare v0.1.1", root.Next, root.Tag)
	}
	if !strings.Contains(stdout, `"tag":"pubsub/v2.1.0"`) || strings.Contains(stdout, `"tag":""`) {
		t.Errorf("tag must be present with its value and never as an empty string:\n%s", stdout)
	}
}
