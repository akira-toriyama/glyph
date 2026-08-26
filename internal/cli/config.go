package cli

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
)

// loadConfig reads the ONE glyph.toml every verdict is judged under: the one
// at the root of the checkout glyph runs in (ratified Q1 — the currently
// checked-out tree decides, accepting that a config change reinterprets past
// commits). Resolution is by git's own answer to "where is this working
// tree's top level", so a command run from a subdirectory reads the same
// file the hook and CI read.
//
// A missing file is usage, not lint: the invocation assumed a v2 repository
// and the repository is not one yet — the fix (run `glyph init`) is the
// caller's, and no commit was judged. A file that exists but does not load
// is the lint class: the repository's own configuration violates its
// contract, the same verdict class doctor hands a broken repository.
func loadConfig(ctx context.Context) (*config.Config, error) {
	top, err := gitsource.TopLevel(ctx, ".")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(top, "glyph.toml")
	cfg, lerr := config.LoadFile(path)
	if lerr != nil {
		if errors.Is(lerr, fs.ErrNotExist) {
			return nil, core.Usagef("no glyph.toml at %s — this repository is not initialized for glyph; write one with `glyph init --gemoji` (or --conventional), or start from either preset and edit", top)
		}
		return nil, core.Lintf("%v", lerr)
	}
	return cfg, nil
}
