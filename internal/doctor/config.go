package doctor

// This file is the glyph.toml check, the first of the local diagnosis. glyph
// is config-first — glyph.toml is read before any commit is judged, so a
// repository whose pin moves before its config exists fails every gate at exit
// 2 with nothing having checked for that state in advance. This check is that
// advance warning (DESIGN §7). It classifies the loader's error; the file
// itself is loaded once, by internal/cli (Input.Config).

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
// was OBSERVED (DESIGN §7 argues them):
//
//   - MISSING is a failure (exit 3), not usage and not unknown, though the
//     verdict commands themselves exit 2 there: doctor was asked whether the
//     repository satisfies glyph's preconditions, and the observed absence
//     answers no.
//   - NOT A REGULAR FILE — a directory, a named pipe, a socket, a device or a
//     symbolic link that resolves to no file at the path — is the same
//     failure: the loader stat'ed what is there and never opened it, so it
//     was observed, and the verdict commands refuse it at 2 like a missing
//     file. Only the remedy differs: remove it first.
//   - A file that exists but does not load — bad TOML, an unknown schema, a
//     pattern that cannot compile or captures no semver_sigil — is the same
//     failure, carrying the loader's own error.
//   - A file that could not be READ (a permission error, an I/O fault) is
//     unknown: nothing about its content was observed.
func checkConfig(root string, rootErr error, cfg *config.Config, loadErr error) Check {
	c := Check{ID: idConfigLoads, Expected: "glyph.toml at the checkout's top level exists and loads (valid schema, compilable patterns, a semver_sigil for every match)"}
	if rootErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not name this checkout's top level: %v", rootErr)
		c.Message = "glyph.toml lives at the top level of the working tree and git is the authority on where that is, so " +
			"outside a repository the file's location does not exist to check. Unverified, not a verdict"
		c.Fix = "re-run from inside a git checkout of the repository being diagnosed"
		return c
	}
	notAFile, isNotAFile := errors.AsType[*config.NotAFileError](loadErr)
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
	case isNotAFile:
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("glyph.toml at %s is %s, not a regular file", root, notAFile.Kind())
		c.Message = "glyph reads its configuration from a regular file at that path and from nothing else: every verdict " +
			"command — lint, bump, notes, preview, release — refuses it the way it refuses a missing file (exit 2, before " +
			"any commit is judged), so the repository's gate is down until a file is there"
		c.Fix = "remove it, then glyph init --gemoji (or --conventional)"
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
// to resolve, a missing file, a path that is not a regular file, an invalid
// file, a read the filesystem refused — so a check that reads the loaded file
// says only that it never did, in the one spelling, and points at the owner:
// the same shape checkTokenWrite takes when the repository object was never
// read.
func configUnread(c *Check, cfg *config.Config) bool {
	if cfg != nil {
		return false
	}
	c.Status = StatusUnknown
	c.Observed = "glyph.toml was not loaded (see " + idConfigLoads + "), so its [[packages]] were never read"
	c.Message = "unverified, not a verdict — this check reads the same file as " + idConfigLoads + " and answers only once it loads"
	c.Fix = "resolve " + idConfigLoads + " and re-run"
	return true
}
