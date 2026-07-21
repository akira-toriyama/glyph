// Package gitsource reads commits and tags out of a local repository by
// shelling out to git (exec.CommandContext — a Ctrl-C cancels the child). It is
// an I/O adapter: it moves bytes and classifies failures (all git failures are
// core.CodeAPI by contract), and holds no commit semantics — parsing belongs to
// internal/parser, participation and folding to internal/bump.
package gitsource

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
)

// RawCommit is one commit as git reports it, before any parsing: the fields
// the range assembler needs to decide participation (author, parent count) and
// to parse (the verbatim message).
type RawCommit struct {
	SHA     string
	Author  string // author name (%an) — what bot/automation matching runs on
	Parents int    // parent count; >= 2 marks a merge commit
	Message string // full raw message (%B), verbatim
}

// logFormat renders one record per commit: SHA, author name, parent SHAs and
// the raw message, unit-separated (\x1f). Records themselves are NUL-separated
// by -z — the only byte a message cannot contain.
const logFormat = "%H%x1f%an%x1f%P%x1f%B"

// Log returns the commits in revRange (e.g. "BASE..HEAD"), oldest first. An
// empty range is a successful empty result. --end-of-options pins revRange as
// a revision, so an option-shaped argument is a git error, never a flag.
func Log(ctx context.Context, dir, revRange string) ([]RawCommit, error) {
	out, err := run(ctx, dir, "log", "-z", "--reverse", "--format="+logFormat, "--end-of-options", revRange, "--")
	if err != nil {
		return nil, err
	}
	return parseLog(out)
}

// Tags lists all tags version-sorted descending (highest first). The caller
// picks the first entry it can parse as a version — deliberately not filtered
// here, so the policy of what counts as a version stays with internal/bump.
func Tags(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "tag", "--list", "--sort=-v:refname")
	if err != nil {
		return nil, err
	}
	var tags []string
	for l := range strings.SplitSeq(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, l)
		}
	}
	return tags, nil
}

// Head returns the checkout's HEAD commit sha — the target_commitish a
// rolling draft records so the eventual Publish tags the commit the verdict
// was computed at.
func Head(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HooksDir returns the directory git will look in for hooks, as a path relative
// to dir (or absolute, when git reports one). It asks git rather than assuming
// .git/hooks because core.hooksPath relocates them — the family's older repos
// set it to scripts/hooks, and writing to .git/hooks there would install a hook
// git never runs. Worktrees are handled by the same delegation.
func HooksDir(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", core.APIf("git rev-parse --git-path hooks: empty result")
	}
	return path, nil
}

// Have reports which of shas this repository actually holds, as a set. It is the
// cheap first half of the footprint question the release walk asks about a pull
// request's commit listing (internal/cli: mainFootprint): most listed shas — a
// squash's pre-squash commits, a rebase-merge's pre-rebase ones — exist in no
// clone of the released branch, and answering that for the whole listing in ONE
// call keeps the expensive per-sha ancestry test off them.
//
// A malformed or unknown sha is simply absent from the result, never an error:
// the listing is GitHub's, and this asks a question about the local repository.
func Have(ctx context.Context, dir string, shas []string) (map[string]bool, error) {
	have := map[string]bool{}
	if len(shas) == 0 {
		return have, nil
	}
	// --batch-check reads the shas on stdin and answers one line each; a missing
	// object answers "<sha> missing" rather than failing the command.
	out, err := runStdin(ctx, dir, strings.Join(shas, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		name, typ, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && typ == "commit" {
			have[name] = true
		}
	}
	return have, nil
}

// IsAncestor reports whether sha is an ancestor of rev, or is rev itself.
//
// A sha this repository does not hold answers false rather than an error, and so
// does one on a history rev cannot reach. Both are ordinary answers to the
// question the walk asks — "did this listed commit land on the released branch
// under its own identity?" — and the callers must not have to tell git's exit 1
// (no) from its exit 128 (no such object) to read them.
func IsAncestor(ctx context.Context, dir, sha, rev string) (bool, error) {
	// #nosec G204 -- the binary is the fixed literal "git"; sha and rev are
	// object names the caller took from git or from a GitHub sha field, and
	// --end-of-options pins both as revisions rather than options.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", "--end-of-options", sha, rev)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return false, &core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true}
		}
		return false, core.APIf("git merge-base: %s", distill(stderr.Bytes(), err))
	}
	return true, nil
}

// FirstParentLog returns the n commits ending at rev along the FIRST-PARENT
// chain, oldest first — the shape a rebase-merge leaves on the released branch,
// and the only reading under which a rebased pull's commits are consecutive.
// Fewer than n exist near the root of a history, and the caller decides what a
// short answer means.
func FirstParentLog(ctx context.Context, dir, rev string, n int) ([]RawCommit, error) {
	out, err := run(ctx, dir, "log", "-z", "--first-parent", "-n", strconv.Itoa(n),
		"--format="+logFormat, "--end-of-options", rev, "--")
	if err != nil {
		return nil, err
	}
	commits, err := parseLog(out)
	if err != nil {
		return nil, err
	}
	slices.Reverse(commits)
	return commits, nil
}

// IsShallow reports whether this checkout is a shallow clone. The release walk
// asks because a truncated history makes every ancestry answer a maybe: a commit
// git cannot see is indistinguishable from one that never landed, and the walk
// would rather say so than quietly grade itself on a partial repository.
func IsShallow(ctx context.Context, dir string) (bool, error) {
	out, err := run(ctx, dir, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// parseLog splits the NUL-separated, unit-separated records logFormat produces.
// Shared by every reader of that format so a field added to logFormat cannot be
// decoded two ways.
func parseLog(out []byte) ([]RawCommit, error) {
	var commits []RawCommit
	for record := range bytes.SplitSeq(out, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		fields := strings.SplitN(string(record), "\x1f", 4)
		if len(fields) != 4 {
			return nil, core.APIf("git log: malformed record %q", string(record))
		}
		commits = append(commits, RawCommit{
			SHA:     fields[0],
			Author:  fields[1],
			Parents: len(strings.Fields(fields[2])),
			Message: fields[3],
		})
	}
	return commits, nil
}

// runStdin is run with bytes fed to the child's stdin — what --batch-check needs.
func runStdin(ctx context.Context, dir, stdin string, args ...string) ([]byte, error) {
	// #nosec G204 -- the binary is the fixed literal "git"; args are structured
	// by this package's exported funcs and carry no caller-supplied revision.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, &core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true}
		}
		return nil, core.APIf("git %s: %s", args[0], distill(stderr.Bytes(), err))
	}
	return stdout.Bytes(), nil
}

// run executes one git command against dir with stdout and stderr captured
// separately, and classifies any failure as a git/IO error carrying git's most
// diagnostic stderr line.
func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// #nosec G204 -- the binary is the fixed literal "git"; args are structured
	// by this package's exported funcs, and the one caller-supplied value
	// (revRange) is pinned as a revision by --end-of-options, never an option.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Only a *canceled* context is the user's own abort — that is what
		// CodeInterrupted means (SIGINT/SIGTERM). A deadline, which only a
		// caller-imposed timeout produces, is a wedged or unreachable git: an
		// ordinary I/O failure. Classifying it as an interrupt would exit 130
		// silently and tell a release job the operator stopped it. Mirrors
		// internal/github's split of the same two causes.
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, &core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true}
		}
		return nil, core.APIf("git %s: %s", args[0], distill(stderr.Bytes(), err))
	}
	return stdout.Bytes(), nil
}

// distill picks the most diagnostic line out of git's stderr — the first
// fatal:/error: line, else the first non-empty line, else the exec error.
func distill(stderr []byte, err error) string {
	lines := strings.Split(string(stderr), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			return l
		}
	}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
