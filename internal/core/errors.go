// Package core holds glyph's cross-cutting types: the process exit-code
// contract and the structured error the CLI renders to stderr. It has no
// dependencies on the other internal packages so everything can import it, and
// it holds no I/O and no logic beyond error classification.
package core

import (
	"errors"
	"fmt"
)

// Code is glyph's process exit-code contract. The CLI maps a returned error to
// one of these on the way out. Keep the meanings stable — scripts, CI gates,
// and Claude Code branch on them.
type Code int

const (
	CodeOK        Code = 0 // success
	CodeNoRelease Code = 1 // no release-worthy change (all commits classify none) — a soft miss, not an error
	CodeUsage     Code = 2 // bad usage or invalid input — fix the args, do not retry
	// CodeLint is the gate code: what glyph was asked to judge violates the
	// convention. That is a commit message under `lint` (the CI commit-lint
	// gate), and a repository's own configuration under `doctor` — the subject
	// differs, the verdict is the same class. Deliberately distinct from usage
	// (2: the invocation itself was fine) and from API (4: glyph could not
	// reach an answer at all).
	CodeLint Code = 3

	// CodeAPI is the no-trustworthy-answer code. It covers the failures its
	// name suggests — GitHub API, git, network, IO — and equally a deliberate
	// REFUSAL to hand down a verdict with nothing broken underneath: an
	// incomplete walk (`cmd_release.go`, ratified t-pysg) and
	// `checkPublishedFloor` both return it, because a verdict computed over a
	// range glyph could not read is worse than no verdict at all. Read as
	// "glyph has no answer it will stand behind", not as "retry later" — the
	// two refusals never clear on a retry, they clear when a human moves the
	// tag or fixes the checkout.
	CodeAPI Code = 4

	// CodeInterrupted is returned when the user interrupts a run with SIGINT
	// (Ctrl-C) or SIGTERM: the first signal cancels in-flight work and exits with
	// the conventional 128+signal code; a second Ctrl-C hard-kills. It is emitted
	// silently (no stderr envelope) since the abort is the user's own doing.
	CodeInterrupted Code = 130
)

// Error is glyph's structured error. On a non-zero exit the CLI prints it to
// stderr as {"error":{"code","message"[,"details"]}} so callers get a
// machine-readable failure. Plain (non-*Error) errors are treated as CodeAPI.
type Error struct {
	Code Code
	Msg  string
	// Details is optional machine-actionable payload for errors where the
	// message alone isn't enough to act on (e.g. the offending commit SHAs for a
	// lint failure). Rendered as "details" in the envelope when non-nil.
	Details any
	// Silent suppresses the stderr error envelope in the CLI's Execute — used
	// when a command already wrote its verdict to stdout and needs only a
	// nonzero exit code.
	Silent bool
}

func (e *Error) Error() string { return e.Msg }

// Usagef builds a CodeUsage error (bad input — do not retry).
func Usagef(format string, a ...any) *Error {
	return &Error{Code: CodeUsage, Msg: fmt.Sprintf(format, a...)}
}

// Lintf builds a CodeLint error (a commit-convention violation).
func Lintf(format string, a ...any) *Error {
	return &Error{Code: CodeLint, Msg: fmt.Sprintf(format, a...)}
}

// APIf builds a CodeAPI error: glyph could not reach an answer it will stand
// behind — a GitHub API / git / network / IO failure, or a refusal to judge
// what it could not read.
func APIf(format string, a ...any) *Error {
	return &Error{Code: CodeAPI, Msg: fmt.Sprintf(format, a...)}
}

// NoReleasef builds a CodeNoRelease error (nothing release-worthy — a soft miss).
func NoReleasef(format string, a ...any) *Error {
	return &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(format, a...)}
}

// ExitCode resolves any error to a process exit code. nil -> 0; a *core.Error
// anywhere in the chain -> its Code; anything else -> CodeAPI.
//
// The CodeAPI fallback is a BACKSTOP, not a policy the CLI relies on: an error
// that reaches a process boundary unclassified is glyph having failed to say
// what went wrong, which is an internal failure, and answering `usage` there
// would tell a caller to fix its arguments over a fault that has nothing to do
// with them. The one shape that legitimately arrives unclassified — cobra's own
// parse error, which IS a usage problem — is classified by the CLI's own funnel
// before it gets here (internal/cli.finish), so in a shipped binary this arm is
// unreachable. That is deliberate: the sole source of truth for the mapping is
// this function, and the CLI decides only what an unclassified error MEANS in
// its context, never what code it carries.
func ExitCode(err error) int {
	if err == nil {
		return int(CodeOK)
	}
	var ce *Error
	if errors.As(err, &ce) {
		return int(ce.Code)
	}
	return int(CodeAPI)
}

// AsError returns the *Error in err's chain, or nil, when the CLI needs the
// structured fields rather than just the exit code.
func AsError(err error) *Error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	return nil
}
