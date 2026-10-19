package draftplan

import (
	"slices"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// Kind is which write a release run performs, and so what its failure means:
// §4's write-first order and the two severities it gives the deletes, per line
// under §4.1 (DESIGN §4, "the order is the general fix"; §4.1, "Drafts, one
// per line"). The policy each kind names is the executor's (internal/cli);
// the plan only says which applies.
type Kind int

const (
	// Upsert creates a line's rolling draft or retags the kept one in place.
	Upsert Kind = iota
	// Residual deletes a draft whose deletion is the whole action of its line
	// or of the run: a none line's managed drafts, and the residue when no
	// line writes a draft.
	Residual
	// Stale deletes a draft beside a draft a line writes — bookkeeping after
	// that write — the residue included when a line does write.
	Stale
)

// Write is one write of a release run, in the order PlanRun gives them.
type Write struct {
	// Line indexes the LinePlan the write belongs to; -1 for the residue,
	// which is no line's (IsResidue).
	Line int
	Kind Kind
	// Action is ActionCreate or ActionUpdate for an Upsert, ActionDelete
	// otherwise.
	Action Action
	// Draft is the draft the write acts on — kept and retagged in place
	// (update), or deleted; nil on a create.
	Draft *Draft
	// Tag and Body are an Upsert's: the tag the draft converges to and the
	// final body it carries (ComposeBody).
	Tag, Body string
}

// IsResidue reports the bare line's residue (Residue), which is no line's.
func (w Write) IsResidue() bool { return w.Line < 0 }

// LinePlan is one line's convergence as PlanRun reads it: PlanDraft's answer
// and, when the line writes a draft, the body that draft carries.
type LinePlan struct {
	Plan Plan
	Body string
}

// PlanRun orders a release run's writes: every line's upsert, in line order;
// then the residual deletes, in line order; then the stale deletes, in line
// order. The residue goes last among whichever kind the run gives it: it is
// no line's, so its deletion is the run's — stale beside a draft any line
// writes, the whole action when none does (DESIGN §4.1, "Drafts, one per
// line").
func PlanRun(lines []LinePlan, residue []Draft) []Write {
	var upserts, residuals, stales []Write
	for i, l := range lines {
		switch l.Plan.Action {
		case ActionCreate, ActionUpdate:
			upserts = append(upserts, Write{Line: i, Kind: Upsert, Action: l.Plan.Action, Draft: l.Plan.Keep, Tag: l.Plan.Tag, Body: l.Body})
			for _, s := range l.Plan.Stale {
				stales = append(stales, Write{Line: i, Kind: Stale, Action: ActionDelete, Draft: &s})
			}
		case ActionDelete:
			for _, s := range l.Plan.Stale {
				residuals = append(residuals, Write{Line: i, Kind: Residual, Action: ActionDelete, Draft: &s})
			}
		case ActionNone:
		}
	}
	for _, r := range residue {
		w := Write{Line: -1, Kind: Residual, Action: ActionDelete, Draft: &r}
		if len(upserts) > 0 {
			w.Kind = Stale
			stales = append(stales, w)
			continue
		}
		residuals = append(residuals, w)
	}
	return slices.Concat(upserts, residuals, stales)
}

// Residue is the bare line's residue: with packages declared and no root
// package, the bare vX.Y.Z and Unreleased drafts the single line left behind,
// which no line would converge again, claimed and deleted on the first
// packages run (DESIGN §4.1, "Drafts, one per line"). The residue line is the
// bare FREE line — config.Config.LineOf on the undeclared root, which leaves a
// declared root-level vN its own major's bare drafts. The residue is no
// line's, so a tag that selects one line clears it too: no line's selection
// protects it. A declared root package makes the bare drafts that package's,
// and without packages the bare line is the walk's one line and plans its
// own — nil in both cases.
func Residue(cfg *config.Config, releases []Draft) []Draft {
	if len(cfg.Packages) == 0 || slices.ContainsFunc(cfg.Packages, func(p config.Package) bool { return p.Path == "." }) {
		return nil
	}
	return managed(cfg.LineOf(config.Package{Path: "."}), releases)
}
