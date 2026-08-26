// Package bump owns glyph's version semantics: the bump lattice, folding a
// set of levels (max — order-independent, so squash order can never change
// the version), stepping a version, and the sigil fold that maps a commit
// range onto the lattice under a v2 config. It is pure — no I/O.
package bump

// Level is a rung on the semver lattice none < patch < minor < major. It is
// a string so it round-trips through JSON surfaces as a human-readable token.
// It lives here — the package that owns version semantics — since v2 removed
// the embedded rules table that used to carry it.
type Level string

const (
	LevelNone  Level = "none"  // never moves the version
	LevelPatch Level = "patch" // a shipped, user-observable change
	LevelMinor Level = "minor" // a new feature
	LevelMajor Level = "major" // a breaking change
)

// Decision is the whole answer a fold produces: the rung the range reached,
// plus whether any commit asked to promote (the '%' sigil).
//
// Promote is a field rather than a fifth rung, and that is the load-bearing
// choice here. Level is a closed four-word vocabulary three consumers already
// read as data — a note section's semver filter, preview's icon and rank, and
// pr-verdict.yml's `[ "$level" = "major" ]` — and every one of them answers an
// unknown word by silently doing nothing rather than by failing. A fifth rung
// would therefore publish a version while telling the pull request it moved
// nothing, and drop the promoting commit out of the release notes. So a '%'
// commit classifies as major like any other breaking change, and carries its
// absoluteness beside the lattice instead of inside it.
type Decision struct {
	Level   Level
	Promote bool
}

// Merge folds two decisions: max on the lattice, OR on promote. Both halves
// are commutative and idempotent, so the whole fold stays order-independent —
// squash order cannot move the version, promotion included.
func (d Decision) Merge(o Decision) Decision {
	return Decision{Level: Reduce([]Level{d.Level, o.Level}), Promote: d.Promote || o.Promote}
}

// Reduce folds levels with max over the lattice. The fold is
// order-independent and idempotent (fuzz-pinned); an empty input is none —
// no release.
func Reduce(levels []Level) Level {
	top := LevelNone
	for _, l := range levels {
		if l.Rank() > top.Rank() {
			top = l
		}
	}
	return top
}

// Rank projects a Level onto the lattice ordering (0..3); an unknown level
// is -1. The max-fold compares ranks, so the fold is order-independent and
// idempotent.
func (l Level) Rank() int {
	switch l {
	case LevelNone:
		return 0
	case LevelPatch:
		return 1
	case LevelMinor:
		return 2
	case LevelMajor:
		return 3
	default:
		return -1
	}
}
