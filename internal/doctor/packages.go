package doctor

// This file is the packages half of the config diagnosis (DESIGN §4.1,
// "Doctor"): a declared [[packages]] path that is no subtree of the repository
// claims no file, so every commit under the directory the author meant is
// attributed elsewhere — a typo here is a silently moved verdict, the class
// this report exists to name. It reads the file at the path git resolved, and
// judges each path against the trees HEAD records — git's answer, handed in by
// internal/cli like the hooks directory — never against the filesystem: the
// question is the one attribution asks, and attribution reads git's paths.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/config"
)

// checkPackagePaths reports whether every declared package path is a
// directory HEAD records, byte for byte. trees / treesErr are
// gitsource.HeadTrees' answer. The severities:
//
//   - No [[packages]] is a pass with nothing to check — the single line. It
//     needs no tree listing, so an unborn HEAD cannot make it unknown.
//   - A declared path HEAD does not record as a tree is a FAILURE: the config
//     names a subtree the repository's history does not have, and the verdict
//     commands would run over that mismatch without a word. Asking the
//     filesystem instead passed a case-different `Haiku` on APFS and a
//     symlink (a 120000 blob to git) on every OS, while `lint --range` under
//     the same config refused a commit under haiku/ at 3 (t-fdd8 (1)).
//   - A glyph.toml that could not be resolved or did not load is UNKNOWN
//     here: its packages were never read. The config check carries the
//     failure; this one says only that it could not run — the same shape
//     checkTokenWrite takes when the repository object was never read.
//   - Packages declared and no tree listing (an unborn HEAD, git failing) is
//     unknown too: nothing was compared.
func checkPackagePaths(path string, pathErr error, trees []string, treesErr error) Check {
	c := Check{ID: IDPackagePaths, Expected: "every [[packages]] path in glyph.toml is a directory HEAD records (git's path, byte for byte)"}
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
	if treesErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not list the directories HEAD records: %v", treesErr)
		c.Message = "a package is a subtree git records, and without git's listing nothing was compared — the filesystem is " +
			"not asked instead, because it answers a different question than attribution does. Unverified, not a verdict"
		c.Fix = "commit at least once (an unborn HEAD records no tree), or fix the checkout so `git ls-tree HEAD` works, and re-run"
		return c
	}

	recorded := make(map[string]bool, len(trees))
	for _, t := range trees {
		recorded[t] = true
	}
	root := filepath.Dir(path)
	var missing []string
	for _, p := range cfg.Packages {
		// The root package is HEAD's own tree, which any listing that
		// answered implies.
		if p.Path == "." || recorded[p.Path] {
			continue
		}
		missing = append(missing, p.Path+" — "+whyNotATree(root, p.Path, trees))
	}
	if len(missing) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d package(s) declared; %d name no directory HEAD records", len(cfg.Packages), len(missing))
		c.Message = "a package is the subtree its path names in git, and attribution matches a commit's changed paths against " +
			"it byte for byte; a path with no such subtree claims no file, so every commit under the directory the author " +
			"meant is attributed to another line or to none — a typo here silently moves the verdict"
		c.Fix = "fix the path in glyph.toml to the directory the line versions, spelled exactly as git records it " +
			"(or commit the directory before declaring it)"
		c.Details = missing
		return c
	}
	c.Status = StatusPass
	paths := make([]string, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		paths = append(paths, p.Path)
	}
	c.Observed = fmt.Sprintf("%d package(s), every path a directory HEAD records: %s", len(cfg.Packages), strings.Join(paths, ", "))
	c.Message = "every declared line has the subtree its tags and its walk are named after"
	return c
}

// whyNotATree explains a path git does not record as a tree, from the two
// shapes the filesystem used to bless: a spelling HEAD records under another
// case, and a symlink. The filesystem is consulted only for this wording —
// never for the verdict, which git's listing has already given.
func whyNotATree(root, p string, trees []string) string {
	for _, t := range trees {
		if strings.EqualFold(t, p) {
			return "HEAD tracks " + t + "; git compares paths byte for byte, so this spelling claims none of its files"
		}
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
	switch {
	case err != nil:
		return "absent from HEAD"
	case info.Mode()&os.ModeSymlink != 0:
		return "a symlink, which git records as a link (mode 120000), not a directory: nothing under it is ever a changed path"
	case info.IsDir():
		return "a directory on disk that HEAD does not record (uncommitted or ignored)"
	}
	return "a file, not a directory"
}
