package doctor

// This file is the packages half of the config diagnosis (DESIGN §4.1,
// "Doctor"): a declared [[packages]] path that does not exist in the checkout
// claims no file, so every commit under the directory the author meant is
// attributed elsewhere — a typo here is a silently moved verdict, the class
// this report exists to name. Like checkConfig it reads the file at the path
// git resolved and nothing else; the paths it stats are relative to that
// file's directory, because a package is a subtree of the checkout glyph.toml
// sits in.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/internal/config"
)

// checkPackagePaths reports whether every declared package path is a
// directory in the checkout. The severities:
//
//   - No [[packages]] is a pass with nothing to check — the single line.
//   - A declared path that is observed absent, or present as a file, is a
//     FAILURE: the config names a subtree the repository does not have, and
//     the verdict commands would run over that mismatch without a word.
//   - A glyph.toml that could not be resolved or did not load is UNKNOWN
//     here: its packages were never read. The config check carries the
//     failure; this one says only that it could not run — the same shape
//     checkTokenWrite takes when the repository object was never read.
//   - A stat that failed for a reason other than absence is unknown too:
//     nothing about that path was observed.
func checkPackagePaths(path string, pathErr error) Check {
	c := Check{ID: IDPackagePaths, Expected: "every [[packages]] path in glyph.toml is a directory in the checkout"}
	if pathErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not name this checkout's top level: %v", pathErr)
		c.Message = "the package paths are subtrees of the checkout glyph.toml sits in, and without a top level neither the file nor the subtrees have a location to check. Unverified, not a verdict"
		c.Fix = "re-run from inside a git checkout of the repository being diagnosed"
		return c
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		c.Status = StatusUnknown
		c.Observed = "glyph.toml was not loaded (see " + IDConfigLoads + "), so its [[packages]] were never read"
		c.Message = "unverified, not a verdict — this check reads the same file as " + IDConfigLoads + " and answers only once it loads"
		c.Fix = "resolve " + IDConfigLoads + " and re-run"
		return c
	}
	if len(cfg.Packages) == 0 {
		c.Status = StatusPass
		c.Observed = "no [[packages]] declared: one version line (vX.Y.Z), nothing to check"
		c.Message = "the repository versions as a single line, the shape every repository has without the key"
		return c
	}

	root := filepath.Dir(path)
	var missing, notDir, unreadable []string
	for _, p := range cfg.Packages {
		info, serr := os.Stat(filepath.Join(root, filepath.FromSlash(p.Path)))
		switch {
		case serr == nil && info.IsDir():
		case serr == nil:
			notDir = append(notDir, p.Path)
		case errors.Is(serr, fs.ErrNotExist):
			missing = append(missing, p.Path)
		default:
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", p.Path, serr))
		}
	}
	switch {
	case len(missing) > 0 || len(notDir) > 0:
		c.Status = StatusFail
		var parts []string
		if len(missing) > 0 {
			parts = append(parts, "absent from the checkout: "+strings.Join(missing, ", "))
		}
		if len(notDir) > 0 {
			parts = append(parts, "present but not a directory: "+strings.Join(notDir, ", "))
		}
		c.Observed = fmt.Sprintf("%d package(s) declared; %s", len(cfg.Packages), strings.Join(parts, "; "))
		c.Message = "a package is the subtree its path names; a path with no such subtree claims no file, so every commit under " +
			"the directory the author meant is attributed to another line or to none — a typo here silently moves the verdict"
		c.Fix = "fix the path in glyph.toml to the directory the line versions (or create the directory before declaring it)"
		c.Details = append(missing, notDir...)
		return c
	case len(unreadable) > 0:
		c.Status = StatusUnknown
		c.Observed = "could not stat: " + strings.Join(unreadable, "; ")
		c.Message = "nothing about these paths was observed, so this is unverified — not broken, not fine"
		c.Fix = "fix the permissions on the named paths and re-run"
		return c
	}
	c.Status = StatusPass
	paths := make([]string, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		paths = append(paths, p.Path)
	}
	c.Observed = fmt.Sprintf("%d package(s), every path a directory in the checkout: %s", len(cfg.Packages), strings.Join(paths, ", "))
	c.Message = "every declared line has the subtree its tags and its walk are named after"
	return c
}
