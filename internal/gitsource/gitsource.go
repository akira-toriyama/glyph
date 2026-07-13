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
	var commits []RawCommit
	for _, record := range bytes.Split(out, []byte{0}) {
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

// Tags lists all tags version-sorted descending (highest first). The caller
// picks the first entry it can parse as a version — deliberately not filtered
// here, so the policy of what counts as a version stays with internal/bump.
func Tags(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "tag", "--list", "--sort=-v:refname")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, l)
		}
	}
	return tags, nil
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
