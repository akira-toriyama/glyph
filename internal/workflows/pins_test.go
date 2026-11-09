package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// usesRef matches an executable `uses:` line that names a remote action or
// reusable with a ref, capturing the ref. `$/…` self references carry no `@`
// and are not pinned to anything but the running commit.
var usesRef = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*["']?[A-Za-z0-9_.-]+/[^@\s"']+@([^\s"'#]+)`)

// pinnedRef is what an executable ref may be: a full commit SHA, or a
// version tag (`v2`, `v6.1.0`). Anything else — `main`, `master`, `HEAD`, a
// topic branch — moves under the pin.
var pinnedRef = regexp.MustCompile(`^([0-9a-f]{40}|v[0-9][0-9A-Za-z.+-]*)$`)

// TestExecutableUsesArePinnedToATagOrASHA holds every executable `uses:` in
// the workflows and the composite actions to a ref that does not move on its
// own: a commit SHA or a version tag. The fleet's zizmor policy
// (.github/zizmor.yml, ref-pin for actions/* and akira-toriyama/*) accepts
// any symbolic ref for those owners, so `actions/checkout@main` passes it —
// measured at 2f9428a with zizmor 1.31.1 (t-j0es) — and lint.yml would then
// install whatever that branch holds tomorrow into every fleet repo's lint
// gate. TestCallerStubsCarryNoConcreteVersion holds glyph's own refs to a
// release tag; this is the same rule for everything else.
func TestExecutableUsesArePinnedToATagOrASHA(t *testing.T) {
	// Positive controls: the shape that must be refused, and the two that pass.
	for ref, ok := range map[string]bool{
		"main": false, "master": false, "HEAD": false, "release/v7": false, "v7.0.1-beta": true,
		"3d3c42e5aac5ba805825da76410c181273ba90b1": true, "v2": true, "v6.1.0": true,
	} {
		if pinnedRef.MatchString(ref) != ok {
			t.Fatalf("pinnedRef judges %q as %v, want %v — the rule below is not the rule described", ref, !ok, ok)
		}
	}
	const canary = "      - uses: actions/checkout@main"
	if m := usesRef.FindStringSubmatch(canary); m == nil || m[1] != "main" {
		t.Fatalf("usesRef does not read the ref out of %q — every assertion below is vacuous", canary)
	}

	var files []string
	for _, name := range workflowFiles(t) {
		files = append(files, filepath.Join(".github", "workflows", name))
	}
	actions, err := filepath.Glob(filepath.Join("..", "..", ".github", "actions", "*", "action.yml"))
	if err != nil || len(actions) == 0 {
		t.Fatalf("listing the composite actions: %v (%d found)", err, len(actions))
	}
	for _, path := range actions {
		files = append(files, filepath.Join(".github", "actions", filepath.Base(filepath.Dir(path)), "action.yml"))
	}

	seen := 0
	for _, rel := range files {
		body := code(repoFile(t, rel))
		for _, m := range usesRef.FindAllStringSubmatch(body, -1) {
			seen++
			if !pinnedRef.MatchString(m[1]) {
				t.Errorf("%s pins an executable `uses:` to %q (%s), which is a moving ref: the action that runs "+
					"tomorrow is whatever that branch holds then, in every repository pinned to this tag. Pin a "+
					"full commit SHA (third parties) or a version tag", rel, m[1], strings.TrimSpace(m[0]))
			}
		}
	}
	// Non-vacuity: the tree carries executable refs today (the reusables'
	// checkouts at least); a scan that finds none is a scan that stopped
	// reading the files.
	if seen < 4 {
		t.Errorf("found %d executable `uses:` refs across %d files, want at least 4 — the matcher no longer "+
			"reads how the workflows write them", seen, len(files))
	}
	if _, err := os.Stat(filepath.Join("..", "..", ".github", "zizmor.yml")); err != nil {
		t.Errorf("no .github/zizmor.yml: the policy this test is stricter than has moved, re-read the premise")
	}
}
