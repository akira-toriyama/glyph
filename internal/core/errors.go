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
	CodeAPI  Code = 4 // GitHub API / git / network / IO failure

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

// APIf builds a CodeAPI error (GitHub API / git / network / IO failure).
func APIf(format string, a ...any) *Error {
	return &Error{Code: CodeAPI, Msg: fmt.Sprintf(format, a...)}
}

// NoReleasef builds a CodeNoRelease error (nothing release-worthy — a soft miss).
func NoReleasef(format string, a ...any) *Error {
	return &Error{Code: CodeNoRelease, Msg: fmt.Sprintf(format, a...)}
}

// ExitCode resolves any error to a process exit code. nil -> 0; a *core.Error
// -> its Code; anything else -> CodeAPI (an unclassified failure is an
// internal/IO failure by definition, and must never fall through to usage).
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
