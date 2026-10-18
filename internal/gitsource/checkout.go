package gitsource

import "context"

// Checkout binds this package's readers to one working tree: internal/walk's
// Git port is this method set, so the walk names no directory of its own and
// its tests point a Checkout at a fixture instead of changing directory. Each
// method is the package function of the same name with Dir as its dir.
type Checkout struct {
	Dir string
}

func (c Checkout) Log(ctx context.Context, revRange string) ([]RawCommit, error) {
	return Log(ctx, c.Dir, revRange)
}

func (c Checkout) IsShallow(ctx context.Context) (bool, error) { return IsShallow(ctx, c.Dir) }

func (c Checkout) Tags(ctx context.Context) ([]string, error) { return Tags(ctx, c.Dir) }

func (c Checkout) MergedTags(ctx context.Context, rev string) ([]string, error) {
	return MergedTags(ctx, c.Dir, rev)
}

func (c Checkout) IsTag(ctx context.Context, name string) (bool, error) {
	return IsTag(ctx, c.Dir, name)
}

func (c Checkout) Have(ctx context.Context, shas []string) (map[string]bool, error) {
	return Have(ctx, c.Dir, shas)
}

func (c Checkout) IsAncestor(ctx context.Context, sha, rev string) (bool, error) {
	return IsAncestor(ctx, c.Dir, sha, rev)
}

func (c Checkout) FirstParentLog(ctx context.Context, rev string, n int) ([]RawCommit, error) {
	return FirstParentLog(ctx, c.Dir, rev, n)
}

func (c Checkout) DiffTreeFiles(ctx context.Context, sha string) ([]string, error) {
	return DiffTreeFiles(ctx, c.Dir, sha)
}

func (c Checkout) MergeBase(ctx context.Context, revs []string) (string, error) {
	return MergeBase(ctx, c.Dir, revs)
}
