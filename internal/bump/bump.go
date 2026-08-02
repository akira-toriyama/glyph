// Package bump owns glyph's version semantics: classifying one commit onto the
// bump lattice, folding a set of levels (max — order-independent, so squash
// order can never change the version), stepping a version, and deciding which
// commits participate at all — in lint and the fold
// (ExcludedFromClassification) and in the release walk's pull-request
// resolution (ExcludedFromResolution), two questions kept apart on purpose. It
// is pure — no I/O — and layers on gitmoji.Lookup / Bump.Rank.
package bump

import (
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// Classify maps one commit to its bump level: an unknown gitmoji is a lint
// error (checked first — membership holds even for breaking commits, never a
// silent fallback), a breaking commit short-circuits to major, and everything
// else takes its table rung.
func Classify(c parser.Commit, t *gitmoji.Table) (gitmoji.Bump, error) {
	rule, ok := t.Lookup(c.Gitmoji)
	if !ok {
		where := ""
		if c.SHA != "" {
			where = " in commit " + c.SHA
		}
		return "", core.Lintf("unknown gitmoji %s%s: not in the embedded rules table (see `glyph rules`)", c.Gitmoji, where)
	}
	if c.Breaking {
		return gitmoji.BumpMajor, nil
	}
	return rule.Bump, nil
}

// Reduce folds levels with max over the lattice. The fold is order-independent
// and idempotent (fuzz-pinned); an empty input is none — no release.
func Reduce(levels []gitmoji.Bump) gitmoji.Bump {
	top := gitmoji.BumpNone
	for _, l := range levels {
		if l.Rank() > top.Rank() {
			top = l
		}
	}
	return top
}

// UnknownParents marks a caller that cannot know a commit's parent count: the
// commit-msg hook lints a MESSAGE before any commit exists. Everywhere else
// parents come from git or the API — github.go fills them for API commits too
// — and 0 means a root commit, which is a different fact from "unknown".
const UnknownParents = -1

// generatedSubjects are the EXACT prefixes git itself writes: an autosquash
// artifact, or a raw `git revert` message — whose subject git wraps in quotes,
// which is why the prefix carries one. They are git's text on a single-parent
// commit, so parent count cannot recognise them and they stay excluded on
// every path. "Merge " is deliberately NOT here: a real merge is recognised
// structurally wherever parents are known, and matching the word instead let
// any single-parent commit whose subject happened to open with "Merge …" slip
// past lint and the version fold, silently (t-fs5y). The word is honoured only
// where parents are genuinely unknown — see ExcludedFromClassification.
var generatedSubjects = []struct{ prefix, label string }{
	{`Revert "`, "Revert"},
	{"fixup! ", "fixup!"},
	{"squash! ", "squash!"},
}

// ExcludedFromClassification reports whether a commit's OWN MESSAGE stays out
// of lint and the version fold, and why: bot authors (`*[bot]`,
// github-actions*, web-flow), merge commits (structurally by parent count —
// by subject only for the hook, which sees a message before the commit exists
// and must pass parents=UnknownParents), and git-generated subjects
// (autosquash artifacts, raw `git revert` messages in git's exact quoted
// form). An excluded commit is skipped, never a violation.
//
// This answers "is this commit's own message classifiable?" — the question
// wherever a MESSAGE is the input: the lint gate, the --range walk, and the
// release walk's fallback path for a commit no pull request explains.
//
// It is emphatically NOT the question the release walk asks of a commit on main
// before resolving it — that one is ExcludedFromResolution. Asking this one
// there is what made a merge-commit-merged pull request vanish out of both the
// version and the notes, silently (t-7zt7).
func ExcludedFromClassification(author, subject string, parents int) (reason string, excluded bool) {
	if reason, excluded := ExcludedFromResolution(author); excluded {
		return reason, true
	}
	if parents >= 2 {
		return "merge commit", true
	}
	// Only the hook may honour the WORD: it lints a message with no commit
	// behind it, so a merge in progress is recognisable by nothing but the
	// subject git is about to write for it. Every parents-aware path relies on
	// the structure above — a single-parent "Merge the two parsers into one"
	// is an author's sentence and must face the lint like any other.
	if parents == UnknownParents && strings.HasPrefix(subject, "Merge ") {
		return "git-generated subject (Merge)", true
	}
	for _, p := range generatedSubjects {
		if strings.HasPrefix(subject, p.prefix) {
			return "git-generated subject (" + p.label + ")", true
		}
	}
	return "", false
}

// ExcludedFromResolution reports whether a commit on main cannot POINT AT a
// merged pull request, and why. It answers a different question from
// ExcludedFromClassification and therefore takes different evidence: the author
// alone. A bot's or an automation's commit is a direct push by construction and
// can never move the version (ratified policy — bot commits are excluded from
// the fold even inside a pull request), so resolving one would buy nothing and
// must not cost an API round-trip: the routine fleet-sync push runs on every
// repository, every day.
//
// Nothing about the commit's own text or shape may be consulted here. The
// message of a merge point is GitHub's, not an author's — `Fix a crash (#7)`
// and `Merge pull request #7 from …` are both POINTERS to a pull request — and
// two parents are how the merge-commit button SHAPES that pointer, not a reason
// to skip it. Whether a merge commit is a pull request is answered by resolving
// it: a local `git merge` resolves to nothing and falls through to the
// classification question, which still excludes it.
//
// web-flow stays excluded deliberately: GitHub sets it as the COMMITTER of a
// web-UI merge and never as the author (that is the human who pressed the
// button), so this gate cannot hide a real merge point; a commit web-flow
// genuinely authored is a browser-made edit, which glyph has never classified.
func ExcludedFromResolution(author string) (reason string, excluded bool) {
	switch {
	case strings.HasSuffix(author, "[bot]"):
		return "bot author " + author, true
	case strings.HasPrefix(author, "github-actions"), author == "web-flow":
		return "automation author " + author, true
	}
	return "", false
}
