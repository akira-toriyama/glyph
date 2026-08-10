package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
)

// warnIfHookStale tells the developer, at the moment the hook runs, that the
// hook running is not the one this binary writes.
//
// The installed hook is the only artefact in this system that drifts with
// nobody watching (DESIGN §7): hooks are untracked, so no pull, no fleet-sync
// and no CI job refreshes one, and a stale hook still exits 0 — indistinguishable
// from a clean message. `doctor` detects it, and nothing in this repository or
// the fleet runs `doctor` on a schedule, so the detector existed and the signal
// never reached anyone. The hook's own run is the one moment the developer is
// certain to be present.
//
// Every failure is silence, without exception. This is advice riding inside a
// gate whose founding contract is that it never returns non-zero on its own
// account; a repository where git cannot report its hooks directory, or where
// the file is unreadable, must commit exactly as before. Silence also covers a
// hook glyph did not write — that is a standing choice `hook.Install` refuses to
// overwrite, and saying so on every commit is the noise that trains people to
// stop reading warnings.
func warnIfHookStale(ctx context.Context, k hook.Kind) {
	dir, err := gitsource.HooksDir(ctx, ".")
	if err != nil {
		return
	}
	body, err := os.ReadFile(filepath.Join(dir, k.Name)) // #nosec G304 -- the path git itself reported
	if err != nil {
		return
	}
	if !k.Stale(string(body)) {
		return
	}
	warnf("the installed %s hook was written by an older glyph and no longer matches this one — "+
		"everything it decides was frozen then, including the exit code it treats as a violation, "+
		"and it fails silently (exit 0). Refresh it: glyph hook install", k.Name)
}
