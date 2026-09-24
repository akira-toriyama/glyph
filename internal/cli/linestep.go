package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
)

// nextOn steps current by dec ON a line (DESIGN §4.1, "The tag line"). The
// bare single line and a free line with no locked sibling step exactly as
// bump.Version.Next does. The two shapes a major version subdirectory adds:
//
//   - A LOCKED line's first release is its own major's floor, vN.0.0,
//     whatever the level: the line holds no version below its major, and
//     a walk base of v0.0.0 only says nothing of it was released yet.
//   - A step that leaves the majors the line holds is refused, lint class,
//     like the wedge it is: a `!` on pubsub/v2 would tag pubsub/v3.0.0, a
//     version no module claims (the module path says /v2; Go's rule puts the
//     v3 in pubsub/v3), and a `!` on the pubsub/ line beside a declared
//     pubsub/v2 would tag the v2 line's own floor. The commit is on a
//     published branch, so the escape is the wedge escape — a tag cut by
//     hand past it on the line it wedges — or the change landing where the
//     next major lives.
func nextOn(l config.Line, current bump.Version, dec bump.Decision) (bump.Version, error) {
	if dec.Level == bump.LevelNone {
		return current, nil
	}
	if l.Locked() && current.Major < l.Major {
		return bump.Version{Major: l.Major}, nil
	}
	next := current.Next(dec)
	if l.Holds(next.Major) {
		return next, nil
	}
	where := fmt.Sprintf("%sv%d", l.Prefix, next.Major)
	if l.Locked() {
		return bump.Version{}, &core.Error{Code: core.CodeLint, Msg: fmt.Sprintf(
			"the %s line steps from %s to %s, a version off its line — the line is major %d by its directory (a Go major version subdirectory, go.dev/ref/mod), so no module claims %s; a %s-level change to it belongs in %s as its own [[packages]] entry. The commit that decides the level is already on a published branch, so this line wedges here until a %s tag at or past it is cut by hand (DESIGN §4.1)",
			l.Label(), current, next.TagOn(l.Prefix), l.Major, next.TagOn(l.Prefix), dec.Level, where, l.Label())}
	}
	return bump.Version{}, &core.Error{Code: core.CodeLint, Msg: fmt.Sprintf(
		"the %s line steps from %s to %s, a version on the %s line, which this repository declares as its own package — the %s-level change belongs there. The commit that decides the level is already on a published branch, so this line wedges here until a %s tag at or past it is cut by hand (DESIGN §4.1)",
		l.Label(), current, next.TagOn(l.Prefix), config.Line{Prefix: l.Prefix, Major: next.Major}.Label(), dec.Level, l.Label())}
}
