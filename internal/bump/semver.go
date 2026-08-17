package bump

import (
	"fmt"
	"regexp"
	"strconv"
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

// baseVersionRE is versionRE with semver's optional pre-release and build
// suffixes allowed after the triple, capturing the triple alone.
var baseVersionRE = regexp.MustCompile(
	`^(v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))` +
		`(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?` +
		`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// ParseBaseVersion parses a tag that MAY carry semver's pre-release or build
// suffix and returns the plain triple in front of it: v3.0.0-rc.1 → v3.0.0,
// v3.0.0 → v3.0.0. It exists for one caller, --since-tag=below:TAG, and is
// deliberately not what ParseVersion became.
//
// The split matters because the two questions differ. ParseVersion decides
// which tags may BE an answer — a walk base, a version to step from — and house
// releases are exactly vX.Y.Z, so a candidate set that admitted v1.0.0-rc1
// would hand a release the predecessor of a candidate (measured, and the reason
// the shell derivation this replaced was retired: t-s5n4). ParseBaseVersion
// decides only what may be ASKED about, and a release candidate is a perfectly
// good question: it is a tag being cut, which is the whole situation below:
// exists for.
//
// Substituting the base is exact rather than approximate, because the candidate
// set holds plain versions only. For any plain p and any pre-release of B:
// p < B-rc ⟺ p < B — if p < B then p ≤ B's predecessor < B-rc; if p = B then
// p > B-rc (semver §11: a pre-release precedes its release); if p > B then
// p > B-rc. So "strictly below the candidate" and "strictly below its base"
// select the same tag, and every rc of B reports the same predecessor — which
// is what makes each candidate's notes cover everything since the last real
// release rather than since the previous candidate.
func ParseBaseVersion(s string) (Version, error) {
	m := baseVersionRE.FindStringSubmatch(s)
	if m == nil {
		// Report against the whole input, not the unmatched remainder: the
		// caller's error names the tag the operator typed.
		return Version{}, fmt.Errorf("%q is not a semver version (want vX.Y.Z, optionally with a -pre-release suffix)", s)
	}
	return ParseVersion(m[1])
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
// steps to 1.0.0 — plain semver, deliberately without a 0.x-keeps-0.x rule.
//
// Nothing double-checks that arithmetic before a release goes out, and nothing
// is meant to: the safety net is that glyph only ever writes a DRAFT, a human
// presses Publish, and checkPublishedFloor refuses a version that is not
// strictly above the highest published release.
func (v Version) Next(level Level) Version {
	switch level {
	case LevelMajor:
		return Version{Major: v.Major + 1}
	case LevelMinor:
		return Version{Major: v.Major, Minor: v.Minor + 1}
	case LevelPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}
