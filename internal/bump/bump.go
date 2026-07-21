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

// generatedSubjects are the prefixes git and GitHub write themselves; such
// commits carry no author-chosen gitmoji and are excluded rather than failed —
// the same tolerance the retired shell commit-lint gave existing history.
var generatedSubjects = []string{"Merge ", "Revert ", "fixup! ", "squash! "}

// ExcludedFromClassification reports whether a commit's OWN MESSAGE stays out
// of lint and the version fold, and why: bot authors (`*[bot]`,
// github-actions*, web-flow), merge commits (structurally by parent count, and
// by subject for API-sourced commits where parents are unknown), and
// git-generated subjects (autosquash artifacts, raw `git revert` messages). An
// excluded commit is skipped, never a violation.
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
	for _, p := range generatedSubjects {
		if strings.HasPrefix(subject, p) {
			return "git-generated subject (" + strings.TrimSpace(strings.TrimSuffix(p, "!")) + ")", true
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
