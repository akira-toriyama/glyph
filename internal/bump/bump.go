// Package bump owns glyph's version semantics: classifying one commit onto the
// bump lattice, folding a set of levels (max — order-independent, so squash
// order can never change the version), stepping a version, and deciding which
// commits participate in lint and the fold at all. It is pure — no I/O — and
// layers on gitmoji.Lookup / Bump.Rank.
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

// Excluded reports whether a commit stays out of lint and the version fold,
// and why: bot authors (`*[bot]`, github-actions*, web-flow), merge commits
// (structurally by parent count, and by subject for API-sourced commits where
// parents are unknown), and git-generated subjects (autosquash artifacts, raw
// `git revert` messages). An excluded commit is skipped, never a violation.
func Excluded(author, subject string, parents int) (reason string, excluded bool) {
	switch {
	case strings.HasSuffix(author, "[bot]"):
		return "bot author " + author, true
	case strings.HasPrefix(author, "github-actions"), author == "web-flow":
		return "automation author " + author, true
	case parents >= 2:
		return "merge commit", true
	}
	for _, p := range generatedSubjects {
		if strings.HasPrefix(subject, p) {
			return "git-generated subject (" + strings.TrimSpace(strings.TrimSuffix(p, "!")) + ")", true
		}
	}
	return "", false
}
