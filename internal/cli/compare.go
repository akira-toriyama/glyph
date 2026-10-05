package cli

import (
	"context"
	"net/url"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
)

// compareMark opens the compare link's line — GitHub's own generate-notes
// spelling of it (internal/github's generatenotes fixture).
const compareMark = "**Full Changelog**: "

// compareLink closes a rendered notes body with the range's one address
// (DESIGN §4, "The compare link"; §4.1 per line):
// `**Full Changelog**: https://<host>/<owner>/<repo>/compare/<base>...<end>`.
// It is the ONE renderer of the line: release's drafts and `notes
// --since-tag`'s stdout both call it, so a draft's machine region stays
// notes' output for the same walk followed by the --footer-file block — and
// the body goreleaser.yml publishes through --release-notes carries the same
// line the rolling drafts do.
//
// base is the tag the walk resolved, in its own spelling (line.BaseTag): ""
// renders nothing, because a walk with no tag base has no left side GitHub
// answers truthfully. end is a sha — the draft's target, or HEAD for notes —
// because a draft's tag does not exist until a human publishes. An empty
// body renders nothing either: notes prints no body there, and a link under
// a placeholder's bare marker would make the draft's machine region differ
// from notes' output for the same walk.
//
// The host is apiHost's, ports dropped: GHE.com data residency, whose API
// host is api.<sub>.ghe.com, would be linked under that API host — a known
// hole no fleet repository sits in.
func compareLink(body, owner, repo, base, end string) string {
	if body == "" || base == "" {
		return body
	}
	return body + "\n" + compareMark + "https://" + apiHost() + "/" + owner + "/" + repo +
		"/compare/" + escapeRef(base) + "..." + escapeRef(end) + "\n"
}

// escapeRef path-escapes each `/`-separated segment of a ref: git accepts `#`,
// `%`, `(` and `)` in a tag name, and pasted verbatim `#` starts a fragment
// and `%41` decodes to another ref, while a line's prefix (`curry/v1.1.0`)
// must keep its slash for GitHub to read it as the tag. t-9np1's markdown
// escaper never sees this line; this is its only escape.
func escapeRef(ref string) string {
	segs := strings.Split(ref, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// tagBase is the compare link's base for a --since-tag value typed by the
// caller: the value, when refs/tags/<value> exists, else "". The resolved
// forms (auto, below:) read their answer out of the tag listing and need no
// asking. A typed value can be a branch, a remote-tracking name, a sha or a
// revision expression, and GitHub resolves such a spelling against ITS refs,
// not the checkout's — measured 2026-10-05 on glyph: `main...<sha>` 200 and
// identical, `origin/main...<sha>` 404 — so only a tag is the walk's base on
// both sides.
func tagBase(ctx context.Context, value string) (string, error) {
	ok, err := gitsource.IsTag(ctx, ".", value)
	if err != nil || !ok {
		return "", err
	}
	return value, nil
}
