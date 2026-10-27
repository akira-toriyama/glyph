package cli

import (
	"context"

	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/akira-toriyama/glyph/v6/internal/hook"
)

// warnIfHookStale tells the developer, at the moment the hook runs, that the
// hook running is not the one this binary writes.
//
// The installed hook drifts with nobody watching, in the quiet direction
// (DESIGN §7). `doctor` detects it, and nothing in this repository or the fleet
// runs `doctor` on a schedule, so the detector existed and the signal never
// reached anyone. The hook's own run is the one moment the developer is
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
	installed, err := k.Read(dir)
	if err != nil || installed.State != hook.Stale {
		return
	}
	warnf("the installed %s hook was written by glyph and no longer matches this binary's — "+
		"everything it decides was frozen then, including the exit code it treats as a violation, "+
		"and it fails silently (exit 0). Refresh it: glyph hook install", k.Name)
}
