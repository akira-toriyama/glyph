package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// These two tests and doctor_test.go's
// TestDoctorSIGINTDuringTheProbeClaimCarriesOut are the three that execute the
// SIGNAL path of the shipped binary for real: Execute()'s NotifyContext
// registration, the delivery of an actual SIGINT to a running glyph process,
// and the exit that follows. The in-process suite cannot reach any of it — a
// test that calls finish() with a prefabricated error has already skipped the
// part that decides whether a Ctrl-C is seen at all — and hook_test.go's live
// commits exercise main() but never signal it. The mutation ledger plants
// both halves of Execute's contract, and each mutant leaves the rest of the
// suite green: without the NotifyContext
// (sigint-kills-glyph-instead-of-cancelling-the-run) exactly these three fail,
// and without the disposition-restoring goroutine
// (second-sigint-is-absorbed-like-the-first) only TestSecondSIGINTHardKills
// does.

// startGlyph launches the built binary with the given stdin and extra
// environment, returning the running command and what it writes to stderr.
// Read that only once Wait has returned, which is when the copy is complete.
// Wait also waits out any child of glyph still holding the stream, and none
// outlives glyph in these tests: the shim's git is reaped before the first
// SIGINT, and the API test's one git call returns before the request.
func startGlyph(t *testing.T, stdin *os.File, env []string, args ...string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	cmd := exec.Command(buildGlyph(t), args...)
	// glyph reads glyph.toml before the API call these tests block
	// in, so the subprocess runs inside an initialized fixture repository.
	cmd.Dir = testutil.NewRepo(t)
	cmd.Stdin = stdin
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting glyph: %v", err)
	}
	return cmd, &stderr
}

// TestRealSIGINTExitsInterrupted delivers an actual SIGINT to a glyph process
// blocked in an interruptible wait and requires the CLEAN half of the signal
// contract: the process classifies the abort itself and exits with code 130
// (core.CodeInterrupted) — a normal exit carrying the number, not a death by
// signal — and stays silent, because the operator caused the stop and needs no
// envelope diagnosing it.
//
// The interruptible wait is an API read against a server that never answers:
// `lint --pr` blocks in the HTTP request, whose context is the NotifyContext
// Execute derived. The request ARRIVING at the server is the readiness
// witness — it proves the process is past signal registration and inside the
// cancellable call — so there is no sleep-and-hope in the ordering. A red
// "never reached the API" on an unmutated tree is the 30s net firing under
// heavy load: re-run the test alone — the stall itself is unexplained, and no
// mechanism tracks it.
func TestRealSIGINTExitsInterrupted(t *testing.T) {
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		<-r.Context().Done() // hold the request open until the client aborts
	}))
	defer srv.Close()

	cmd, stderr := startGlyph(t, nil, []string{"GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=t"},
		"lint", "--pr", "1", "--repo", "o/r")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("glyph exited before it reached the API: %v\nstderr: %s", err, stderr)
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("glyph never reached the API — nothing was blocked in the interruptible wait")
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering SIGINT: %v", err)
	}
	err := <-done
	if err == nil {
		t.Fatalf("glyph exited 0 after a SIGINT — the interrupt was swallowed as success\nstderr: %s", stderr)
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status: %v\nstderr: %s", err, stderr)
	}
	if ws.Signaled() {
		t.Fatalf("glyph was KILLED by %v instead of classifying the interrupt and exiting 130 itself\nstderr: %s", ws.Signal(), stderr)
	}
	if code := cmd.ProcessState.ExitCode(); code != 130 {
		t.Fatalf("exit %d, want 130 (core.CodeInterrupted) — the raw-signal contract every CI wrapper reads\nstderr: %s", code, stderr)
	}
	if stderr.Len() != 0 {
		t.Fatalf("glyph wrote to stderr on an interrupt the operator caused, want nothing (core.Interrupted is Silent):\n%s", stderr)
	}
}

// TestSecondSIGINTHardKills pins the other half of Execute's contract: once the
// first SIGINT has cancelled the context, the disposition-restoring goroutine
// puts the default handler back, so a SECOND Ctrl-C kills the process outright
// instead of being absorbed by a binary that is refusing to die.
//
// The process is parked in `lint --stdin` on a pipe that never closes — a read
// that is deliberately NOT context-aware, which is exactly the state the
// second Ctrl-C exists for: the first one cannot unblock it. The readiness
// witness is a git shim on glyph's PATH that writes its pid to a marker and
// execs the real git. `lint --stdin` asks git for the top level (loadConfig)
// before it reads, and Execute registers the handler before any RunE runs, so
// the marker proves registration happened; the pid going away proves glyph
// has reaped that git, its one context-aware call before the read — a SIGINT
// landing mid-call cancels it into exit 130 (with the marker alone as the
// witness, 26 of 30 runs did, measured). A red "never got past its git call"
// on an unmutated tree is the 30s net firing under heavy load: re-run the test
// alone — the stall itself is unexplained, and no mechanism tracks it.
//
// The ordering is then asserted from behind: after the first SIGINT the
// process must still be ALIVE — Wait, started before it, has not returned
// (Wait reaps, so it sees a dead child; on darwin Signal(0) succeeds on an
// unreaped one) — proving the handler absorbed it, and only the second one may
// take it down, as a real signal death this time.
func TestSecondSIGINTHardKills(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is not on PATH: %v", err)
	}
	shim := t.TempDir()
	marker := filepath.Join(shim, "pid")
	script := "#!/bin/sh\necho $$ >" + marker + "\nPATH=" + filepath.Dir(realGit) + "\nexec git \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinW.Close()

	cmd, stderr := startGlyph(t, stdinR, []string{"PATH=" + shim}, "lint", "--stdin")
	stdinR.Close() // the child holds its copy; ours would only leak
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	deadline := time.After(30 * time.Second)
	for !reaped(marker) {
		select {
		case err := <-done:
			t.Fatalf("glyph exited before it reached the read: %v\nstderr: %s", err, stderr)
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatal("glyph never got past its git call, so nothing shows it is parked in the read")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the first SIGINT: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("glyph died on the FIRST SIGINT (%v) while blocked in a read the context cannot cancel — "+
			"the absorb half of the contract is gone\nstderr: %s", err, stderr)
	case <-time.After(200 * time.Millisecond):
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the second SIGINT: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("glyph survived a second SIGINT — the default disposition was never restored, " +
			"and a hung binary cannot be Ctrl-C'd out of")
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status\nstderr: %s", stderr)
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Fatalf("exit state %v (code %d), want death by SIGINT — the second Ctrl-C must be the raw signal, "+
			"not another absorbed one\nstderr: %s", ws, cmd.ProcessState.ExitCode(), stderr)
	}
}

// reaped reports whether the git whose pid marker holds has exited and been
// reaped — kill(pid, 0) answers ESRCH only once its parent has waited for it,
// since a zombie still takes signals.
func reaped(marker string) bool {
	b, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
