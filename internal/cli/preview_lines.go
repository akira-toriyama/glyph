package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/internal/attribution"
	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/akira-toriyama/glyph/internal/preview"
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

// previewLines resolves the pull's commits, attributes each to the lines
// its own files move — over the API, one request per commit: a pull's
// commits exist on its branch only, and this checkout may not hold them —
// folds the PR side per touched line, walks the pending side once when any
// touched line has released, and renders. A commit attribution refuses is
// the same lint-class refusal the walk and lint --range hand down: preview
// says what CI will say.
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

	// Which lines does each participating commit move? Judged exactly as the
	// walk judges a squash-arm inner commit: not an excluded author, not a
	// skip, matched — then its files, then attribution.
	perLine := make([][]gitsource.RawCommit, len(cfg.Packages))
	for _, r := range raws {
		if slices.Contains(cfg.ExcludeAuthors, r.Author) {
			continue
		}
		m, merr := cfg.Match(r.Message)
		if merr != nil || !m.Matched || m.Skip {
			// An unmatched message refuses the fold below, as it does for
			// the single line; it is not attributed to anything first.
			continue
		}
		var files []string
		if r.Parents < 2 {
			var capped bool
			var ferr error
			files, capped, ferr = gh.CommitFiles(ctx, owner, repo, r.SHA)
			if ferr != nil {
				return ferr
			}
			if capped {
				warnf("commit %.7s in pull request #%d touches at least %d files, and GitHub lists no more than that — a package it touches past the cap is missing from this preview", r.SHA, previewPR, github.CommitFilesCap)
			}
		}
		moved, aerr := attribution.Attribute(files, m.Groups[config.ScopeGroup], m.Sigil, cfg.Packages)
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
		latest, current, verr := latestVersionTag(ctx, p.TagPrefix(), nil)
		if verr != nil {
			return verr
		}
		tl := touchedLine{idx: i, pkg: p, prRows: rows, prDec: dec, latest: latest, current: current, untagged: latest == ""}
		if !tl.untagged {
			walkNeeded = true
		}
		touched = append(touched, tl)
	}

	// The pending side: one walk over every line, run only when a touched
	// line has released at all — the single line's release-floor guard, per
	// line. A touched line with no tag reports its PR verdict alone.
	pending := map[string]bump.Decision{}
	pendingShort := ""
	if walkNeeded {
		w, serr := sinceTagInput(ctx, cfg, sinceTagAuto, previewRepo)
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

	in := preview.Input{PendingShort: pendingShort}
	var pkgs []packagePreview
	var noteBodies []string
	for _, tl := range touched {
		prefix := tl.pkg.TagPrefix()
		p := preview.Package{Path: tl.pkg.Path, Current: tl.current.TagOn(prefix), Untagged: tl.untagged,
			PR: preview.Verdict{Level: tl.prDec.Level, Commits: previewCommits(tl.prRows)}}
		pv := packagePreview{Path: tl.pkg.Path, Current: tl.current.String(), Untagged: tl.untagged, PR: string(tl.prDec.Level), Pending: string(bump.LevelNone)}
		if tl.prDec.Level != bump.LevelNone {
			p.PR.Next = tl.current.Next(tl.prDec).TagOn(prefix)
		}
		pd := pending[tl.pkg.Path]
		p.Pending.Level = orNone(pd.Level)
		pv.Pending = string(orNone(pd.Level))
		if pd.Level != bump.LevelNone && pd.Level != "" {
			p.Pending.Next = tl.current.Next(pd).TagOn(prefix)
		}
		d := foldDecision(tl.prDec, pd)
		pv.Level = string(d.Level)
		if d.Level != bump.LevelNone {
			pv.Next = tl.current.Next(d).String()
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
		body = truncateComment(preview.Marker + "\n⏸️ Merging this PR moves nothing — its " + fmt.Sprintf("%d", len(raws)) + " commit(s) touch no declared package.\n\n" + fmt.Sprintf("Computed from the %d commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. Pushing more commits updates this comment.", len(raws)) + "\n")
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
