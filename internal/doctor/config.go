package doctor

// This file is the config half of the local diagnosis: the v2 invariant is
// config-first — glyph.toml is read before any commit is judged, so a
// repository whose pin moves before its config exists fails every gate at
// exit 2 with nothing having checked for that state in advance. This check is
// that advance warning. The file is loaded ONCE, by internal/cli at the top
// level git named (the same file every verdict command reads), and handed in
// with the loader's error verbatim: this check classifies that error, and the
// checks that read the loaded file — package paths, root-line tags, the hook
// probe's claim — read what it loaded and never open the file themselves.

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// checkConfig reports whether the glyph.toml every verdict command reads
// exists and loads. root is the checkout's top level; rootErr is git's
// failure to name one, which degrades the check to could-not-run exactly like
// an unreadable input anywhere else. cfg / loadErr are config.LoadFile's
// answer at that root.
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
func checkConfig(root string, rootErr error, cfg *config.Config, loadErr error) Check {
	c := Check{ID: IDConfigLoads, Expected: "glyph.toml at the checkout's top level exists and loads (valid schema, compilable patterns, a semver_sigil for every match)"}
	if rootErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not name this checkout's top level: %v", rootErr)
		c.Message = "glyph.toml lives at the top level of the working tree and git is the authority on where that is, so " +
			"outside a repository the file's location does not exist to check. Unverified, not a verdict"
		c.Fix = "re-run from inside a git checkout of the repository being diagnosed"
		return c
	}
	_, isPathErr := errors.AsType[*fs.PathError](loadErr)
	switch {
	case loadErr == nil:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("glyph.toml loads: schema %d, %d pattern(s), %d note section(s)", cfg.Schema, len(cfg.Patterns), len(cfg.Note.Sections))
		c.Message = "the config every verdict is judged under is there and valid, so the verdict commands can run at all"
		return c
	case errors.Is(loadErr, fs.ErrNotExist):
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("no glyph.toml at %s", root)
		c.Message = "this repository is not initialized for glyph: the message grammar and the semver_sigil mapping live in " +
			"glyph.toml, and without the file every verdict command — lint, bump, notes, preview, release — refuses to run " +
			"(exit 2, before any commit is judged). A repository that pins glyph's workflows without a config has its whole " +
			"gate down, which is exactly the state this check exists to name before the pin moves"
		c.Fix = "glyph init --gemoji (or --conventional) — then edit the generated glyph.toml freely"
		return c
	case isPathErr:
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("glyph.toml could not be read: %v", loadErr)
		c.Message = "the file is there but its content was never observed, so this is unverified — not broken, not fine"
		c.Fix = "fix the file permissions and re-run"
		return c
	}
	c.Status = StatusFail
	c.Observed = fmt.Sprintf("glyph.toml does not load: %v", loadErr)
	c.Message = "the file exists but violates its own contract, and the loader rejects rather than repairs — a silently " +
		"misread key would be a silently changed verdict. Every verdict command refuses this file the same way, so until it " +
		"loads the repository's gate is down"
	c.Fix = "edit glyph.toml until `glyph doctor` passes — the observed error names the offending key or pattern"
	return c
}

// configUnread degrades c to could-not-run when glyph.toml was not loaded,
// and reports that it did. The config check owns the reason — no top level
// to resolve, a missing or invalid file, a read the filesystem refused — so a
// check that reads the loaded file says only that it never did, in the one
// spelling, and points at the owner: the same shape checkTokenWrite takes
// when the repository object was never read.
func configUnread(c *Check, cfg *config.Config) bool {
	if cfg != nil {
		return false
	}
	c.Status = StatusUnknown
	c.Observed = "glyph.toml was not loaded (see " + IDConfigLoads + "), so its [[packages]] were never read"
	c.Message = "unverified, not a verdict — this check reads the same file as " + IDConfigLoads + " and answers only once it loads"
	c.Fix = "resolve " + IDConfigLoads + " and re-run"
	return true
}
