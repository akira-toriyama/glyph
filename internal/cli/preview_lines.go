package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/attribution"
	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/github"
	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
	"github.com/akira-toriyama/glyph/v4/internal/notes"
	"github.com/akira-toriyama/glyph/v4/internal/preview"
)

// This file is `glyph preview` for a repository that declares [[packages]]
// (DESIGN §4.1, "Preview"): one verdict per line the pull's commits are
// attributed to, in config order, each folded with that line's pending
// side from ONE walk. A line the pull does not touch is not mentioned; a
// pull whose commits carry nothing says it moves nothing. The single line's
// preview (cmd_preview.go) is untouched.

// packagePreview is one line's verdict inside previewResult.packages —
// bump's vocabulary (bare versions, the folded level, the two sides) per
// line.
type packagePreview struct {
	Path     string `json:"path"`
	Current  string `json:"current"`
	Untagged bool   `json:"untagged"`
	Level    string `json:"level"`
	Next     string `json:"next,omitempty"`
	PR       string `json:"pr"`
	Pending  string `json:"pending"`
}

// previewWalkEscape is the remedy preview can actually offer: it has no
// --since-tag flag, so the refusal names only the tag the message already
// told the operator to cut.
const previewWalkEscape = "; cut the tag named above and re-run — preview reads the pull request, and has no --since-tag flag to name a base with"

// previewLines resolves the pull's commits, attributes each to the lines
// its own files move — over the API, one request per commit: a pull's
// commits exist on its branch only, and this checkout may not hold them —
// folds the PR side per touched line, walks the pending side once when any
// touched line has released, and renders. A commit attribution refuses is
// the same lint-class refusal the walk and lint --range hand down: preview
// says what CI will say — including that a refusal over a listing GitHub
// truncated is withheld (partitionLines): the commit is attributed to no
// line, and the body says the PR side is incomplete, because a reviewer
// reads this comment and never the log (preview.Input.PRShort).
func previewLines(ctx context.Context, cfg *config.Config) error {
	raws, _, perr := pullInput(ctx, previewPR, previewRepo)
	if perr != nil {
		return perr
	}
	owner, repo, rerr := resolveRepo(ctx, previewRepo)
	if rerr != nil {
		return rerr
	}
	gh := newGitHub()

	// Which lines does each commit move? Placed exactly as the walk places a
	// squash-arm inner commit (placeOf): by its files — scope and sigil too
	// when the fold reads the message, files alone for an exclude_authors
	// commit — or not at all: a skip appears nowhere, and an unmatched
	// message refuses the whole-listing fold below, as it does for the
	// single line, so it is not attributed first. The first cut dropped
	// excluded authors here while the walk placed them on every line — two
	// answers in one run (t-sr1c, measured 2026-09-11; mutation row
	// preview-packages-excluded-author-dropped).
	perLine := make([][]gitsource.RawCommit, len(cfg.Packages))
	var prCapped []string
	for _, r := range raws {
		place, scope, sigil := placeOf(cfg, r)
		if place != placedByFiles {
			continue
		}
		var files []string
		capped := false
		if r.Parents < 2 {
			var ferr error
			files, capped, ferr = gh.CommitFiles(ctx, owner, repo, r.SHA)
			if ferr != nil {
				return ferr
			}
			if capped {
				prCapped = append(prCapped, fmt.Sprintf("%.7s", r.SHA))
				warnf("commit %.7s in pull request #%d touches at least %d files, and GitHub lists no more than that — a package it touches past the cap is missing from this preview", r.SHA, previewPR, github.CommitFilesCap)
			}
		}
		moved, aerr := attribution.Attribute(files, scope, sigil, cfg.Packages)
		if aerr != nil && capped {
			warnf("commit %.7s: over the files GitHub listed, attribution would refuse it (%v) — but the listing was truncated, so that is not a verdict: the commit is attributed to no line", r.SHA, aerr)
			continue
		}
		if aerr != nil {
			return &core.Error{Code: core.CodeLint, Details: []rangeViolation{{SHA: r.SHA, Subject: bump.FirstLine(r.Message), Detail: aerr.Error()}},
				Msg: fmt.Sprintf("commit %.7s in pull request %s/%s#%d: %v — the release walk will refuse this commit the same way once it is merged, and a merged commit cannot be rewritten; fix it on the branch", r.SHA, owner, repo, previewPR, aerr)}
		}
		for i, p := range cfg.Packages {
			if slices.ContainsFunc(moved, func(q config.Package) bool { return q.Path == p.Path }) {
				perLine[i] = append(perLine[i], r)
			}
		}
	}
	// The whole listing is folded once first, so a message no pattern claims
	// refuses the preview before any line is rendered — the single line's
	// rule, and the reason unmatched commits were not attributed above.
	if _, _, cerr := bump.FoldSigils(sigilCommits(raws), cfg); cerr != nil {
		return cerr
	}

	type touchedLine struct {
		idx      int
		pkg      config.Package
		prRows   []bump.SigilVerdict
		prDec    bump.Decision
		latest   string
		current  bump.Version
		untagged bool
	}
	var touched []touchedLine
	walkNeeded := false
	for i, p := range cfg.Packages {
		if len(perLine[i]) == 0 {
			continue
		}
		rows, dec, cerr := bump.FoldSigils(sigilCommits(perLine[i]), cfg)
		if cerr != nil {
			return cerr
		}
		warnSigilVerdicts(rows)
		latest, current, verr := latestVersionTag(ctx, cfg.LineOf(p), nil)
		if verr != nil {
			return verr
		}
		tl := touchedLine{idx: i, pkg: p, prRows: rows, prDec: dec, latest: latest, current: current, untagged: latest == ""}
		if !tl.untagged {
			walkNeeded = true
		}
		touched = append(touched, tl)
	}

	// The pending side: one walk, run only when a touched line has released
	// at all — the single line's release-floor guard, per line.
	//
	// The walk's RANGE comes from the touched lines alone. Resolved over every
	// declared line it used to take one untouched line with no tag to the whole
	// history: past the cap that refused the whole command (exit 4) for a pull
	// that touches only released lines, and under it that bought one API
	// round-trip per commit of the history for a line nobody asked about
	// (t-60dc symptoms A and B, measured 2026-09-15 on a 211-commit fixture:
	// exit 4 for a haiku-only pull, and 9 round-trips where the touched line's
	// own range held 1).
	pending := map[string]bump.Decision{}
	pendingShort := ""
	if walkNeeded {
		only := make([]config.Package, 0, len(touched))
		for _, tl := range touched {
			only = append(only, tl.pkg)
		}
		w, serr := sinceTagInputScoped(ctx, cfg, sinceTagAuto, previewRepo, &walkScope{Only: only, Escape: previewWalkEscape})
		if serr != nil {
			return serr
		}
		if !w.Facts.complete() {
			pendingShort = w.Facts.shortfall(owner, repo)
		}
		for _, lw := range w.Lines {
			rows, dec, cerr := bump.FoldSigils(walkedSigilCommits(lw.Commits), cfg)
			if cerr != nil {
				return cerr
			}
			warnSigilVerdicts(rows)
			pending[lw.Package.Path] = dec
		}
	} else if len(touched) > 0 {
		warnf("no release tag on any line this PR touches — previewing the PR's own verdict per line (the pending walk needs a release floor)")
	}

	prShort := ""
	if len(prCapped) > 0 {
		prShort = walkFacts{FilesCapped: prCapped}.shortfall(owner, repo)
	}
	in := preview.Input{PendingShort: pendingShort, PRShort: prShort}
	var pkgs []packagePreview
	var noteBodies []string
	for _, tl := range touched {
		tline := cfg.LineOf(tl.pkg)
		prefix := tline.Prefix
		p := preview.Package{Path: tl.pkg.Path, Current: tl.current.TagOn(prefix), Untagged: tl.untagged,
			// PendingWalked is the half preview.Input.Untagged used to carry on
			// its own: "the pending side is UNCOMPUTED". On the packages path a
			// line with no tag still gets its pending walked whenever a tagged
			// sibling in the same pull pulls the walk to the whole history, and
			// reusing one flag for both meanings made the same run answer twice
			// — body "the first release here would be curry/v0.0.1", machine
			// verdict next=v0.1.0 (t-60dc symptom C, measured 2026-09-15).
			PendingWalked: walkNeeded,
			PR:            preview.Verdict{Level: tl.prDec.Level, Commits: previewCommits(tl.prRows)}}
		pv := packagePreview{Path: tl.pkg.Path, Current: tl.current.String(), Untagged: tl.untagged, PR: string(tl.prDec.Level), Pending: string(bump.LevelNone)}
		if tl.prDec.Level != bump.LevelNone {
			next, nerr := nextOn(tline, tl.current, tl.prDec)
			if nerr != nil {
				return nerr
			}
			p.PR.Next = next.TagOn(prefix)
		}
		pd := pending[tl.pkg.Path]
		p.Pending.Level = orNone(pd.Level)
		pv.Pending = string(orNone(pd.Level))
		if pd.Level != bump.LevelNone && pd.Level != "" {
			next, nerr := nextOn(tline, tl.current, pd)
			if nerr != nil {
				return nerr
			}
			p.Pending.Next = next.TagOn(prefix)
		}
		d := foldDecision(tl.prDec, pd)
		pv.Level = string(d.Level)
		if d.Level != bump.LevelNone {
			next, nerr := nextOn(tline, tl.current, d)
			if nerr != nil {
				return nerr
			}
			pv.Next = next.String()
		}
		in.Packages = append(in.Packages, p)
		pkgs = append(pkgs, pv)
		if previewNotes {
			sections, gerr := notes.GroupSigils(noteCommits(perLine[tl.idx], previewPR), cfg)
			if gerr != nil {
				return gerr
			}
			if len(sections) > 0 {
				noteBodies = append(noteBodies, "# "+tl.pkg.Path+"\n\n"+notes.RenderSigils(sections))
			}
		}
	}
	if len(noteBodies) > 0 {
		in.Notes = strings.Join(noteBodies, "\n")
	}

	var body string
	if len(touched) == 0 {
		body = truncateComment(preview.Marker + "\n⏸️ Merging this PR moves nothing — its " + fmt.Sprintf("%d", len(raws)) + " commit(s) touch no declared package.\n" + preview.PRShortBlock(prShort) + "\n" + fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. Pushing more commits updates this comment.", len(raws)) + "\n")
	} else {
		body = truncateComment(preview.Render(in))
	}
	if previewJSON {
		if pkgs == nil {
			pkgs = []packagePreview{}
		}
		printCompact(previewResult{Pending: string(bump.LevelNone), PR: string(bump.LevelNone), Body: body, Packages: pkgs})
		return nil
	}
	fmt.Fprint(out, body)
	return nil
}
