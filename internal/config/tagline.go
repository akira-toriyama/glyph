package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// This file is the path-to-line rule (DESIGN §4.1, "The tag line"): the ONE
// place a package's tag namespace and the majors it holds are derived. Every
// resolver — the walk base, the published floor, the managed drafts, the
// step — asks its question ON a Line and never re-derives either half.
//
// The rule is Go's (go.dev/ref/mod, "Module paths"): the tag prefix is the
// module subdirectory NOT including a major version suffix, and a module at
// major N ≥ 2 may live in a /vN subdirectory. So `pubsub/v2` is versioned by
// `pubsub/v2.7.0`, on the same prefix as `pubsub/v1.9.1` — two lines, one
// prefix, told apart by the major. Deriving `pubsub/v2/` instead (the first
// cut) gave that module a tag line nothing in the Go ecosystem reads: zero
// tags, a current of v0.0.0, and a bump that rewound a published v2.7.0 to
// `pubsub/v2/v2.3.0` (t-z9d3, measured 2026-09-13 on google-cloud-go and
// etcd's client/v3).

// majorSubdir is the shape of a major version subdirectory name: v followed
// by an integer of 2 or more without a leading zero. v0 and v1 are plain
// directory names — Go forbids the /v1 suffix and a v0 module path has none.
var majorSubdir = regexp.MustCompile(`^v([2-9]|[1-9][0-9]+)$`)

// Line is a package's tag line: the prefix its tags carry, and which majors
// on that prefix are its. Major > 0 is a LOCKED line — a major version
// subdirectory, holding that major alone; Major == 0 is a FREE line, holding
// every major no sibling locked line on the same prefix claims (Claimed).
// The zero value is the bare single line: every vX.Y.Z tag is its.
type Line struct {
	Prefix  string
	Major   int
	Claimed []int
}

// Holds says whether a version of the given major is on this line.
func (l Line) Holds(major int) bool {
	if l.Major > 0 {
		return major == l.Major
	}
	return !slices.Contains(l.Claimed, major)
}

// Locked says whether the line is a major version subdirectory's.
func (l Line) Locked() bool { return l.Major > 0 }

// MajorDir is the subdirectory a locked line lives in under its prefix
// ("v2/"), "" for a free line — what a locked line's non-version artifacts
// (the placeholder draft) are named under, since two lines share the prefix.
func (l Line) MajorDir() string {
	if l.Major == 0 {
		return ""
	}
	return "v" + strconv.Itoa(l.Major) + "/"
}

// Label names the line in a message: the bare line as "bare v*", a package's
// by its prefix, a locked line by the major it holds.
func (l Line) Label() string {
	switch {
	case l.Major > 0 && l.Prefix == "":
		return fmt.Sprintf("bare v%d.*", l.Major)
	case l.Major > 0:
		return fmt.Sprintf("%sv%d.*", l.Prefix, l.Major)
	case l.Prefix == "":
		return "bare v*"
	}
	return l.Prefix
}

// Major is the major a package's path locks it to — N for a path whose last
// segment is a major version subdirectory vN (N ≥ 2) — and 0 otherwise.
func (p Package) Major() int {
	m := majorSubdir.FindStringSubmatch(path.Base(p.Path))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// TagPrefix is the line's tag namespace: "" for the root package (its line
// is the bare vX.Y.Z) and "<path>/" for every other, the path taken WITHOUT
// a major version subdirectory — `pubsub/v2` tags as `pubsub/vX.Y.Z`, and a
// root-level `v2` as bare `vX.Y.Z`. It is the ONE place the path-to-prefix
// rule lives; every resolver takes the prefix from here and none re-derives
// it (a hand-spelled prefix without the slash would name a line that exists
// nowhere, silently).
func (p Package) TagPrefix() string {
	dir := p.Path
	if p.Major() > 0 {
		dir = path.Dir(dir)
	}
	if dir == "." {
		return ""
	}
	return dir + "/"
}

// refnameSegment says why git refuses seg as one slash-separated component of
// a refname (git check-ref-format), "" when it does not. Every segment of a
// package path is such a component twice over — in each tag <prefix>vX.Y.Z
// and in the placeholder <path>/Unreleased — so a segment git refuses is a
// line no tag can ever be cut on. The rules that bind a whole name's end (no
// trailing dot, not a lone @) never reach a segment: both names end past it.
// The one that binds its start is refnameLead's. TestLoadRefusesAPathGitCannotTag
// holds both to git itself.
func refnameSegment(seg string) string {
	switch {
	case strings.HasPrefix(seg, "."):
		return "begins with a dot"
	case strings.HasSuffix(seg, ".lock"):
		return "ends with .lock"
	case strings.Contains(seg, ".."):
		return "holds two consecutive dots"
	case strings.Contains(seg, "@{"):
		return "holds @{"
	}
	for i := 0; i < len(seg); i++ {
		switch c := seg[i]; {
		case c < 0x20 || c == 0x7f:
			return "holds a control character"
		case c == ' ':
			return "holds a space"
		case strings.IndexByte(`~^:?*[\`, c) >= 0:
			return fmt.Sprintf("holds %q", c)
		}
	}
	return ""
}

// refnameLead says why `git tag` refuses every tag a non-root package path's
// line names because of how the path begins, "" when it does not. git tag
// refuses a tag name whose first character is '-' — a rule of git tag's own,
// on top of git check-ref-format, which accepts the name — and only the whole
// name's first character: `a/-b/v0.0.1` tags (measured, git 2.54.0). Every
// such tag, and the placeholder, begins with the path itself (TagPrefix keeps
// the first segment), so the path's first character is the tag's.
func refnameLead(p string) string {
	if strings.HasPrefix(p, "-") {
		return `begins with "-", which git tag refuses as a tag name's first character (git check-ref-format alone accepts it)`
	}
	return ""
}

// LineOf is the line a package is versioned on, among the packages declared:
// a locked line holds its major; a free line holds every major no sibling on
// the same prefix locks. p need not be declared — the bare line of a
// repository with no root package is LineOf(Package{Path: "."}), and it
// still yields to a declared root-level vN.
func (c *Config) LineOf(p Package) Line {
	l := Line{Prefix: p.TagPrefix(), Major: p.Major()}
	if l.Major > 0 {
		return l
	}
	for _, q := range c.Packages {
		if q.Path != p.Path && q.TagPrefix() == l.Prefix && q.Major() > 0 {
			l.Claimed = append(l.Claimed, q.Major())
		}
	}
	slices.Sort(l.Claimed)
	return l
}

// defaultName is the scope's default word for a package: the last path
// segment, or for a major version subdirectory the segment before it with
// the suffix kept — `pubsub/v2` for pubsub/v2, which is what monorepos write
// in the scope (google-cloud-go: `feat(pubsub/v2): …`), and `v2` for a
// root-level one. The loader refuses the default wherever the file's scope
// grammar cannot spell it (checkScopeWord) — under the presets always for a
// /vN subdirectory and for the root's ".", which then set `name`.
func defaultName(p string) string {
	base := path.Base(p)
	if majorSubdir.MatchString(base) {
		if parent := path.Dir(p); parent != "." {
			return path.Base(parent) + "/" + base
		}
	}
	return strings.TrimSuffix(base, "/")
}

// defaultNameRule names the arm of defaultName that produced p's default, for
// a refusal that has to say where an unwritten name came from.
func defaultNameRule(p string) string {
	switch {
	case p == ".":
		return "the root package's path"
	case strings.Contains(defaultName(p), "/"):
		return "a major version subdirectory keeps its parent"
	}
	return "the last path segment"
}
