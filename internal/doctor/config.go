package doctor

// This file is the config half of the local diagnosis: the v2 invariant is
// config-first — glyph.toml is read before any commit is judged, so a
// repository whose pin moves before its config exists fails every gate at
// exit 2 with nothing having checked for that state in advance. This check is
// that advance warning. Like the workflow scan it reads files and nothing
// else; the PATH it reads is git's answer to "where is this checkout's top
// level", resolved by internal/cli beside every other subprocess.

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/akira-toriyama/glyph/internal/config"
)

// checkConfig reports whether the glyph.toml every verdict command reads
// exists and loads. path is the file's resolved location (checkout top level
// + glyph.toml); pathErr is the failure to resolve it, which degrades the
// check to could-not-run exactly like an unreadable input anywhere else.
//
// The severities draw the same line the rest of the report draws — on what
// was OBSERVED:
//
//   - MISSING is a failure, not usage and not unknown. The absence was
//     observed and it is a finding about the repository: every verdict
//     command (lint, bump, notes, preview, release) refuses to run without
//     the file, so a repository that pins glyph without one has its whole
//     gate down. The verdict commands themselves exit 2 there, because for
//     THEM the caller assumed a v2 repository and the invocation was the
//     mistake; doctor was asked whether the repository satisfies glyph's
//     preconditions and the honest answer is no — the same class as every
//     other violated precondition, exit 3.
//   - A file that exists but does not load — bad TOML, an unknown schema, a
//     pattern that cannot compile or captures no semver_sigil — is the same
//     failure: config.Load already rejects rather than repairs (no silent
//     none), and doctor repeats its verdict with the loader's own error.
//   - A file that could not be READ (a permission error, an I/O fault) is
//     unknown: nothing about its content was observed, and "we could not
//     check" is not "it is broken" any more than it is "it is fine".
func checkConfig(path string, pathErr error) Check {
	c := Check{ID: IDConfigLoads, Expected: "glyph.toml at the checkout's top level exists and loads (valid schema, compilable patterns, a semver_sigil for every match)"}
	if pathErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not name this checkout's top level: %v", pathErr)
		c.Message = "glyph.toml lives at the top level of the working tree and git is the authority on where that is, so " +
			"outside a repository the file's location does not exist to check. Unverified, not a verdict"
		c.Fix = "re-run from inside a git checkout of the repository being diagnosed"
		return c
	}
	cfg, err := config.LoadFile(path)
	var perr *fs.PathError
	switch {
	case err == nil:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("glyph.toml loads: schema %d, %d pattern(s), %d note section(s)", cfg.Schema, len(cfg.Patterns), len(cfg.Note.Sections))
		c.Message = "the config every verdict is judged under is there and valid, so the verdict commands can run at all"
		return c
	case errors.Is(err, fs.ErrNotExist):
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("no glyph.toml at %s", filepath.Dir(path))
		c.Message = "this repository is not initialized for glyph: the message grammar and the semver_sigil mapping live in " +
			"glyph.toml, and without the file every verdict command — lint, bump, notes, preview, release — refuses to run " +
			"(exit 2, before any commit is judged). A repository that pins glyph's workflows without a config has its whole " +
			"gate down, which is exactly the state this check exists to name before the pin moves"
		c.Fix = "glyph init --gemoji (or --conventional) — then edit the generated glyph.toml freely"
		return c
	case errors.As(err, &perr):
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("glyph.toml could not be read: %v", err)
		c.Message = "the file is there but its content was never observed, so this is unverified — not broken, not fine"
		c.Fix = "fix the file permissions and re-run"
		return c
	}
	c.Status = StatusFail
	c.Observed = fmt.Sprintf("glyph.toml does not load: %v", err)
	c.Message = "the file exists but violates its own contract, and the loader rejects rather than repairs — a silently " +
		"misread key would be a silently changed verdict. Every verdict command refuses this file the same way, so until it " +
		"loads the repository's gate is down"
	c.Fix = "edit glyph.toml until `glyph doctor` passes — the observed error names the offending key or pattern"
	return c
}
