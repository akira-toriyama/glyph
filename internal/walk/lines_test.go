package walk

import (
	"slices"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// TestLinePathspec pins the construction of a line's own files, not git's
// reading of it (gitsource's TestFirstChangeReadsTheOldestCommitTouchingThePathspec
// measures that over a hand-written pathspec): a nested package is taken out
// of its parent, so onsen's first change is never travel's; the path is read
// from the top level (`:(top)`), so a `bump --since-tag` run from a
// subdirectory asks the same question as one run from the root; and the
// root package is the whole tree minus every other package, so a root line
// declared after the lines it sits beside has a first change of its own.
// Each is a baseline offered or withheld (walk.baselineOn), and each
// survived the suite when broken until this test (review of #361,
// 2026-11-08). Mutation rows
// packages-baseline-counts-a-nested-package-as-its-parents-first-change,
// packages-baseline-reads-the-path-from-the-working-directory,
// packages-baseline-root-package-owns-every-line.
func TestLinePathspec(t *testing.T) {
	cfg := &config.Config{Packages: []config.Package{{Path: "travel"}, {Path: "travel/onsen"}, {Path: "haiku"}, {Path: "."}}}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"travel", []string{":(top)travel", ":(top,exclude)travel/onsen"}},
		{"travel/onsen", []string{":(top)travel/onsen"}},
		{"haiku", []string{":(top)haiku"}},
		{".", []string{":(top)", ":(top,exclude)travel", ":(top,exclude)travel/onsen", ":(top,exclude)haiku"}},
	} {
		var p config.Package
		for _, q := range cfg.Packages {
			if q.Path == tc.path {
				p = q
			}
		}
		if got := linePathspec(cfg, p); !slices.Equal(got, tc.want) {
			t.Errorf("linePathspec(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
