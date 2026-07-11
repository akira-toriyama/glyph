package core

import (
	"errors"
	"fmt"
	"testing"
)

// The exit-code contract is a product API — scripts and Claude Code branch on
// these numbers, so pin them explicitly.
func TestCodeValues(t *testing.T) {
	cases := []struct {
		name string
		got  Code
		want int
	}{
		{"OK", CodeOK, 0},
		{"NoRelease", CodeNoRelease, 1},
		{"Usage", CodeUsage, 2},
		{"Lint", CodeLint, 3},
		{"API", CodeAPI, 4},
		{"Interrupted", CodeInterrupted, 130},
	}
	for _, c := range cases {
		if int(c.got) != c.want {
			t.Errorf("Code %s = %d, want %d", c.name, int(c.got), c.want)
		}
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is OK", nil, 0},
		{"usage", Usagef("bad flag %d", 1), 2},
		{"lint", Lintf("unknown gitmoji %q", ":nope:"), 3},
		{"api", APIf("network down"), 4},
		{"no release", NoReleasef("nothing to ship"), 1},
		// An unclassified (non-*Error) failure is an internal/IO failure by
		// definition — it must NEVER fall through to usage(2).
		{"bare error is API", errors.New("boom"), 4},
		{"wrapped *Error keeps its code", fmt.Errorf("ctx: %w", Lintf("x")), 3},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestErrorFields(t *testing.T) {
	e := Lintf("bad %s", "commit")
	if e.Error() != "bad commit" {
		t.Errorf("Error() = %q, want %q", e.Error(), "bad commit")
	}
	if e.Code != CodeLint {
		t.Errorf("Code = %d, want %d", int(e.Code), int(CodeLint))
	}
}

func TestAsError(t *testing.T) {
	le := Lintf("x")
	if got := AsError(fmt.Errorf("wrap: %w", le)); got != le {
		t.Errorf("AsError(wrapped) = %v, want %v", got, le)
	}
	if got := AsError(errors.New("bare")); got != nil {
		t.Errorf("AsError(bare) = %v, want nil", got)
	}
	if got := AsError(nil); got != nil {
		t.Errorf("AsError(nil) = %v, want nil", got)
	}
}
