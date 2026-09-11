package cli

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/akira-toriyama/glyph/v3/internal/config"
	"github.com/akira-toriyama/glyph/v3/internal/core"
	"github.com/akira-toriyama/glyph/v3/internal/gitsource"
)

// loadConfig reads the ONE glyph.toml every verdict is judged under: the one
// at the root of the checkout glyph runs in (ratified Q1 — the currently
// checked-out tree decides, accepting that a config change reinterprets past
// commits). Resolution is by git's own answer to "where is this working
// tree's top level", so a command run from a subdirectory reads the same
// file the hook and CI read.
//
// None of the three failures here is the gate code. CodeLint is "what glyph
// was asked to judge violates the convention", and on these commands the
// subject is a COMMIT — the config is the yardstick, not the thing judged. A
// broken yardstick means no verdict was reached, so reporting 3 tells a CI
// gate a commit was rejected when none was even read (t-c6r5). `doctor` is
// the one command whose subject IS the repository's configuration, and it
// keeps answering 3 there; that asymmetry is the contract, not a lapse.
//
//   - missing (2): the invocation assumed an initialized repository and this
//     one is not yet — the fix, `glyph init`, is the caller's.
//   - unreadable (4): permission denied, EISDIR, a vanishing mount. glyph
//     could not reach an answer at all, which is what 4 means; `doctor` and
//     the hook installer already classify the identical event that way.
//   - unparseable (2): the file is there and readable but says something
//     invalid. Same position as a missing config — an unusable configuration
//     a human fixes by editing a file — so it takes the same code.
//
// Keeping these off 3 is also what lets a broken glyph.toml be FIXED: the
// installed commit-msg and pre-push hooks block on 3 alone, so classifying a
// typo as 3 blocks the very commit that would repair it.
func loadConfig(ctx context.Context) (*config.Config, error) {
	top, err := gitsource.TopLevel(ctx, ".")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(top, "glyph.toml")
	cfg, lerr := config.LoadFile(path)
	if lerr != nil {
		// Order matters: a missing file is itself a *fs.PathError, so the
		// not-exist arm has to be asked first.
		var perr *fs.PathError
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			return nil, core.Usagef("no glyph.toml at %s — this repository is not initialized for glyph; write one with `glyph init --gemoji` (or --conventional), or start from either preset and edit", top)
		case errors.As(lerr, &perr):
			return nil, core.APIf("%v — glyph could not read this repository's configuration, so no commit was judged", lerr)
		default:
			return nil, core.Usagef("%v", lerr)
		}
	}
	return cfg, nil
}
