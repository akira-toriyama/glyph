package doctor

// This file is the tag half of the packages diagnosis (DESIGN §4.1,
// "Doctor"): a repository that declares packages but no root package has no
// line whose tags are the bare vX.Y.Z — so bare version tags it already
// carries baseline nothing from now on. That is not a defect (the tags are
// history, and the walk never reads them), but it is the one migration
// surprise a monorepo author meets first, and the fix is one declaration.
// Tags are read by the caller (internal/cli owns every subprocess) and handed
// over with their error, as the hooks directory is.

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/config"
)

// checkRootLineTags reports whether the bare v* tags in the checkout belong
// to a declared line. Severities:
//
//   - No [[packages]] → pass: the single line is the bare line, every bare
//     tag is its.
//   - A root package declared (path ".") → pass: bare tags are its line.
//   - Packages declared, no root, no bare version tag → pass.
//   - Packages declared, no root, bare version tags present → ADVICE: those
//     tags baseline no line, and the note says which declaration adopts them.
//     Never a failure — leaving history untouched is a legitimate choice.
//   - The config unresolved or unloaded, or the tags unlistable → unknown:
//     one of the two inputs was never observed.
func checkRootLineTags(path string, pathErr error, tags []string, tagsErr error) Check {
	c := Check{ID: IDRootLineTags, Expected: "every bare vX.Y.Z tag belongs to a declared line (no [[packages]], or a root package)"}
	if pathErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not name this checkout's top level: %v", pathErr)
		c.Message = "without glyph.toml's location the declared lines are unknown, so nothing can be said about which line a tag is on. Unverified, not a verdict"
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
		c.Observed = "no [[packages]] declared: the single line is the bare vX.Y.Z line, and every bare tag is its"
		c.Message = "the repository's tags and its one version line agree"
		return c
	}
	for _, p := range cfg.Packages {
		if p.Path == "." {
			c.Status = StatusPass
			c.Observed = fmt.Sprintf("a root package is declared (name %q): the bare vX.Y.Z line is its", p.Name)
			c.Message = "bare version tags baseline the root package, so nothing the repository already released is orphaned"
			return c
		}
	}
	if tagsErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not list this checkout's tags: %v", tagsErr)
		c.Message = "the declared lines have no root package, and whether any bare tag exists to be orphaned was never observed. Unverified, not a verdict"
		c.Fix = "fix the checkout (git tag --list must work) and re-run"
		return c
	}
	bare, highest := 0, ""
	var hv bump.Version
	for _, t := range tags {
		v, perr := bump.ParseVersionOn("", t)
		if perr != nil {
			continue
		}
		if bare == 0 || v.Compare(hv) > 0 {
			highest, hv = t, v
		}
		bare++
	}
	if bare == 0 {
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("%d package(s) declared, none at the root, and no bare vX.Y.Z tag exists", len(cfg.Packages))
		c.Message = "every version tag the walk could read is on a declared line"
		return c
	}
	c.Status = StatusAdvice
	c.Observed = fmt.Sprintf("%d package(s) declared, none at the root, and %d bare vX.Y.Z tag(s) exist (highest %s)", len(cfg.Packages), bare, highest)
	c.Message = "those tags are on the bare line, and with no root package declared no line's walk or floor reads them: they are history, not a base. " +
		"That is a legitimate state — nothing is broken — but if the root of the repository is itself a package they are its releases"
	c.Fix = "to adopt them, declare the root package in glyph.toml — [[packages]] path = \".\" (set name = \"…\" so a scope can name it); to leave them as history, nothing"
	return c
}
