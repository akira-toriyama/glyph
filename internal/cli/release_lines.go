package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v3/internal/bump"
	"github.com/akira-toriyama/glyph/v3/internal/config"
	"github.com/akira-toriyama/glyph/v3/internal/core"
	"github.com/akira-toriyama/glyph/v3/internal/draftplan"
	"github.com/akira-toriyama/glyph/v3/internal/github"
	"github.com/akira-toriyama/glyph/v3/internal/gitsource"
	"github.com/akira-toriyama/glyph/v3/internal/notes"
	"github.com/spf13/cobra"
)

// This file is `glyph release` for a repository that declares [[packages]]
// (DESIGN §4.1, "Drafts, one per line"): one walk, one verdict per line, one
// rolling draft per line, converged by draftplan on that line's own tag
// prefix so the founding invariants — never a second draft, retagged in
// place, nothing glyph did not manage touched — hold per line. The single
// line's release (cmd_release.go) is untouched: a repository without packages
// never reaches here.

// packageRelease is one line's verdict inside releaseResult.packages: what
// bump's packageVerdict carries, plus the draft this line converges to — its
// tag, its body, the convergence performed and, after a real write, its URL.
type packageRelease struct {
	Path    string              `json:"path"`
	Current string              `json:"current"`
	Level   string              `json:"level"`
	Next    string              `json:"next,omitempty"`
	Tag     string              `json:"tag,omitempty"`
	Body    string              `json:"body,omitempty"`
	Action  string              `json:"action"`
	URL     string              `json:"url,omitempty"`
	Commits []bump.SigilVerdict `json:"commits"`
	Reason  string              `json:"reason"`
}

// lineDraft is one line's planned write: the verdict it reports and the
// draft it converges to (nil plan Keep means create).
type lineDraft struct {
	verdict packageRelease
	plan    draftplan.Plan
	params  github.ReleaseParams
}

// releaseLines runs the release for a packages repository. The decisions it
// carries, each ratified in §4.1:
//
//   - every line's verdict, body and plan are computed BEFORE any write, so a
//     line that refuses (the published floor, an oversized body) stops the
//     run with nothing written;
//   - the write order is §4's write-first, extended: every line's upsert
//     lands before any line's strays are converged, and a write that fails
//     on the second line leaves the first line's notes standing and exits 4
//     — the next run heals it;
//   - a tag that selects one line converges that line ALONE: the other
//     lines' drafts are not this run's to touch;
//   - the bare vX.Y.Z draft of a repository that declares packages but no
//     root package is the single line's residue and is deleted with a notice
//     on the first packages run — a hand region it carried goes with it, and
//     the notice says so;
//   - --footer-file appends to every draft; make_latest is never set;
//   - exit 1 is answered only when every selected line folds to none.
func releaseLines(ctx context.Context, cmd *cobra.Command, cfg *config.Config, footer, owner, repoName string) error {
	tagFlag := releaseSinceTag
	if !cmd.Flags().Changed("since-tag") {
		tagFlag = sinceTagAuto
	}
	w, perr := sinceTagInput(ctx, cfg, tagFlag, releaseRepo)
	if perr != nil {
		return perr
	}
	if !w.Facts.complete() {
		return core.APIf("this walk did not read %s (%s) — refusing to hand down a verdict computed from a range it could not read; re-run the release once the walk can read the range",
			w.Source, w.Facts.shortfall(owner, repoName))
	}
	if err := checkLineSelection(releaseCurrent, w.Lines); err != nil {
		return err
	}
	// The union rows first: a message no pattern claims refuses the walk
	// before any line is folded, and the rows are what the verdict reports as
	// the commits it read.
	rows, _, cerr := bump.FoldSigils(walkedSigilCommits(w.All), cfg)
	if cerr != nil {
		return cerr
	}
	warnSigilVerdicts(rows)

	gh := newGitHub()
	releases, lerr := gh.Releases(ctx, owner, repoName)
	if lerr != nil {
		return lerr
	}
	drafted := planInput(releases)

	var verdicts []packageRelease
	var drafts []lineDraft
	var stale []github.Release
	var reasons []string
	moving := 0
	for _, lw := range w.Lines {
		lineVerdicts, dec, ferr := bump.FoldSigils(walkedSigilCommits(lw.Commits), cfg)
		if ferr != nil {
			return ferr
		}
		current, verr := currentVersion(ctx, releaseCurrent, lw.Base, lw.Prefix)
		if verr != nil {
			return verr
		}
		pv := packageRelease{Path: lw.Package.Path, Current: current.String(), Level: string(dec.Level), Commits: lineVerdicts}
		noneReason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(lineVerdicts), lw.Source)
		if dec.Level == bump.LevelNone && !cfg.Note.DraftOnNone {
			plan := draftplan.PlanDraft(lw.Prefix, dec.Level, "", false, drafted)
			pv.Action, pv.Reason = string(plan.Action), noneReason
			stale = append(stale, staleReleases(plan.Stale)...)
			verdicts = append(verdicts, pv)
			reasons = append(reasons, lw.Package.Path+": "+pv.Reason)
			continue
		}
		tagName := draftplan.PlaceholderTagOn(lw.Prefix)
		pv.Reason = noneReason
		if dec.Level != bump.LevelNone {
			tag := current.Next(dec)
			if gerr := checkPublishedFloor(lw.Prefix, tag, releases); gerr != nil {
				return gerr
			}
			tagName = tag.TagOn(lw.Prefix)
			pv.Next = tag.String()
			pv.Reason = decidingReason(lineVerdicts, dec)
			moving++
		}
		sections, gerr := notes.GroupSigils(walkedNoteCommits(lw.Commits), cfg)
		if gerr != nil {
			return gerr
		}
		body := notes.RenderSigils(sections)
		if footer != "" {
			body = body + "\n---\n\n" + footer
		}
		plan := draftplan.PlanDraft(lw.Prefix, dec.Level, tagName, cfg.Note.DraftOnNone, drafted)
		body = composeDraftBody(keptBody(plan.Keep, releases), body)
		if serr := checkReleaseBody(body); serr != nil {
			return serr
		}
		pv.Tag, pv.Body, pv.Action = tagName, body, string(plan.Action)
		stale = append(stale, staleReleases(plan.Stale)...)
		drafts = append(drafts, lineDraft{verdict: pv, plan: plan, params: github.ReleaseParams{TagName: tagName, Name: tagName, Body: body, Draft: true}})
		verdicts = append(verdicts, pv)
		reasons = append(reasons, lw.Package.Path+": "+pv.Reason)
	}
	reason := strings.Join(reasons, "; ")
	if moving == 0 {
		reason = "no release: every line folds to none (" + reason + ")"
	}

	// The bare line's residue: with packages declared and no root package,
	// a bare vX.Y.Z (or Unreleased) draft is what the single line left behind
	// and nothing will ever converge it again. Claimed on the precedent of the
	// placeholder being claimed with the flag off. A tag that selected one
	// line still clears it: the residue belongs to no line, so no line's
	// selection protects it.
	var residue []github.Release
	if _, hasRoot := packageOnLine(cfg, ""); !hasRoot {
		residue = staleReleases(draftplan.PlanDraft("", bump.LevelNone, "", false, drafted).Stale)
		for _, r := range residue {
			noticef("the bare draft %s (release id %d) is the single line's residue — this repository declares packages and no root package, so no line will converge it again; it is deleted, and a hand region it carried goes with it (move that prose into the line's own draft, above the marker)", r.TagName, r.ID)
		}
	}
	stale = append(stale, residue...)

	// The target resolves before the dry-run fork (Q4: only the writes are
	// skipped), once for every draft — one checkout, one HEAD.
	target := releaseTarget
	if target == "" {
		var herr error
		if target, herr = gitsource.Head(ctx, "."); herr != nil {
			return herr
		}
	}
	for i := range drafts {
		drafts[i].params.Target = target
	}

	result := releaseResult{Target: target, Commits: rows, Packages: verdicts, Pulls: w.Facts.Pulls, Reason: reason}
	finish := func() error {
		if moving == 0 {
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return nil
	}

	if releaseDryRun {
		for _, d := range drafts {
			noticef("dry run: the upsert would %s the rolling draft %s at %s", d.plan.Action, d.params.TagName, target)
		}
		if len(stale) > 0 {
			noticef("dry run: %d stale draft(s) to delete after the upserts", len(stale))
		}
		if releaseJSON {
			printCompact(result)
			return finish()
		}
		if len(drafts) == 0 {
			return core.NoReleasef("%s", reason)
		}
		blocks := make([]string, 0, len(drafts))
		for _, d := range drafts {
			blocks = append(blocks, d.params.TagName+"\n\n"+d.params.Body)
		}
		fmt.Fprint(out, strings.Join(blocks, "\n"))
		return finish()
	}

	// Every line's upsert lands before any stray goes: a failure here leaves
	// the lines already written standing and exits 4 — nothing was destroyed,
	// and the next run heals the rest.
	urls := make([]string, 0, len(drafts))
	for i, d := range drafts {
		var rel github.Release
		var werr error
		if d.plan.Keep != nil {
			rel, werr = gh.UpdateRelease(ctx, owner, repoName, d.plan.Keep.ID, d.params)
		} else {
			rel, werr = gh.CreateRelease(ctx, owner, repoName, d.params)
		}
		if werr != nil {
			if i > 0 {
				warnf("%d line(s) were written before this failure and stand; the remaining lines converge on the next run", i)
			}
			return werr
		}
		noticef("draft release %s %sd (unpublished — the tag is created when a human publishes): %s", d.params.TagName, d.plan.Action, rel.URL)
		urls = append(urls, rel.URL)
		for j := range result.Packages {
			if result.Packages[j].Path == d.verdict.Path {
				result.Packages[j].URL = rel.URL
			}
		}
	}
	if len(drafts) == 0 {
		// No line had a draft to write, so the deletes are the whole action —
		// loud, as releaseNone's are: absorbing a failure here would mean the
		// run did nothing and reported fine.
		for _, s := range stale {
			gone, derr := gh.DeleteRelease(ctx, owner, repoName, s.ID)
			if derr != nil {
				return derr
			}
			noticef("no release is due — %s the residual draft %s (release id %d)", discardedOrGone(gone), s.TagName, s.ID)
		}
	} else if cerr := convergeStrays(ctx, gh, owner, repoName, stale); cerr != nil {
		return cerr
	}

	if releaseJSON {
		printCompact(result)
		return finish()
	}
	for _, u := range urls {
		fmt.Fprintln(out, u)
	}
	if moving == 0 && len(drafts) == 0 {
		return core.NoReleasef("%s", reason)
	}
	return finish()
}
