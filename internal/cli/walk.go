package cli

import (
	"context"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/github"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/akira-toriyama/glyph/v6/internal/walk"
)

// This file is cli's side of internal/walk's ports: the checkout the walk
// reads (gitsource.Checkout at the working directory), the API client it
// resolves pulls through, and the diagnostic stream its warnings and notices
// land on — so DESIGN §5's stream order holds for the walk's lines exactly as
// for cli's own. The repository is resolved here too (resolveRepo), once, as
// for every API-side command.

// Both adapters are checked where they are composed, not where they are
// used: a method the walk adds to a port fails this file, never a caller.
var (
	_ walk.Git   = gitsource.Checkout{}
	_ walk.Pulls = (*github.Client)(nil)
)

// reporter is walk.Reporter over the diagnostic stream.
type reporter struct{}

func (reporter) Warnf(format string, a ...any)   { warnf(format, a...) }
func (reporter) Noticef(format string, a ...any) { noticef(format, a...) }

// localWalker is the walk over the checkout alone — the sources that resolve
// nothing over the API (a --range fold, a pull's listing on the bare line)
// and the step base a command reads out of the tags.
func localWalker() walk.Walker {
	return walk.Walker{Git: gitsource.Checkout{Dir: "."}, Report: reporter{}}
}

// apiWalker is the walk with GitHub behind it: the release walk and the
// files preview reads of a pull's commits.
func apiWalker(gh *github.Client) walk.Walker {
	w := localWalker()
	w.Pulls = gh
	return w
}

// sinceTagInput is the release walk as every command reaches it: the
// --since-tag value checked as the caller's input (usage, 2) before the
// repository is resolved, then one walk over every declared line. bump,
// notes, release and preview share it; the result's Facts say whether the
// walk read its range (release refuses on them, preview reports the
// shortfall, bump and notes discard them).
func sinceTagInput(ctx context.Context, cfg *config.Config, tagFlag, repoFlag string) (walk.Result, error) {
	return sinceTagInputScoped(ctx, cfg, tagFlag, repoFlag, nil)
}

// sinceTagInputScoped is sinceTagInput with the range narrowed to scope
// (walk.Scope; preview is its one user).
func sinceTagInputScoped(ctx context.Context, cfg *config.Config, tagFlag, repoFlag string, scope *walk.Scope) (walk.Result, error) {
	if err := walk.CheckTag(tagFlag); err != nil {
		return walk.Result{}, err
	}
	owner, repo, err := resolveRepo(ctx, repoFlag)
	if err != nil {
		return walk.Result{}, err
	}
	return apiWalker(newGitHub()).Since(ctx, cfg, tagFlag, owner, repo, scope)
}
