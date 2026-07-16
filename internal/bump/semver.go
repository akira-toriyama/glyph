package bump

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// Version is a plain semver triple. House tags are exactly vX.Y.Z — no
// pre-release, no build metadata — so nothing looser is representable.
type Version struct {
	Major, Minor, Patch int
}

// versionRE is the accepted tag shape: an optional leading v, then three
// dot-separated non-negative decimals without leading zeros (semver §2).
var versionRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseVersion parses a house version tag, accepting an optional leading v.
// It returns a plain error — the caller classifies it, since only the caller
// knows whether the string came from a flag (usage) or from git (API).
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a plain semver version (want vX.Y.Z)", s)
	}
	var v Version
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("%q is not a plain semver version: %v", s, err)
		}
		*dst = n
	}
	return v, nil
}

// String renders the version in the house tag form, always with the v prefix.
func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare orders two versions numerically: -1 when v < o, 0 when equal,
// 1 when v > o — the primitive behind the published-floor guard (which needs
// STRICTLY greater, so equality must be distinguishable from above).
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		switch {
		case d[0] < d[1]:
			return -1
		case d[0] > d[1]:
			return 1
		}
	}
	return 0
}

// Next steps the version by one bump level; none holds it still. A 0.x major
// steps to 1.0.0 — plain semver, deliberately without a 0.x-keeps-0.x rule;
// the shadow-mode phase compares this against git-cliff before any publish
// relies on it.
func (v Version) Next(level gitmoji.Bump) Version {
	switch level {
	case gitmoji.BumpMajor:
		return Version{Major: v.Major + 1}
	case gitmoji.BumpMinor:
		return Version{Major: v.Major, Minor: v.Minor + 1}
	case gitmoji.BumpPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}
