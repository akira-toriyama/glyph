package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// These two tests are the only place the SIGNAL path of the shipped binary is
// executed for real: Execute()'s NotifyContext registration, the delivery of an
// actual SIGINT to a running glyph process, and the exit that follows. The
// in-process suite cannot reach any of it — a test that calls finish() with a
// prefabricated error has already skipped the part that decides whether a
// Ctrl-C is seen at all — and hook_test.go's live commits exercise main() but
// never signal it. Without these, a simplification of Execute that drops the
// NotifyContext (or the disposition-restoring goroutine beside it) keeps the
// whole suite green.

// startGlyph launches the built binary with the given stdin and extra
// environment, returning the running command.
func startGlyph(t *testing.T, stdin *os.File, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(buildGlyph(t), args...)
	cmd.Stdin = stdin
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting glyph: %v", err)
	}
	return cmd
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
// cancellable call — so there is no sleep-and-hope in the ordering.
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

	cmd := startGlyph(t, nil, []string{"GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=t"},
		"lint", "--pr", "1", "--repo", "o/r")
	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("glyph never reached the API — nothing was blocked in the interruptible wait")
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering SIGINT: %v", err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatal("glyph exited 0 after a SIGINT — the interrupt was swallowed as success")
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status: %v", err)
	}
	if ws.Signaled() {
		t.Fatalf("glyph was KILLED by %v instead of classifying the interrupt and exiting 130 itself", ws.Signal())
	}
	if code := cmd.ProcessState.ExitCode(); code != 130 {
		t.Fatalf("exit %d, want 130 (core.CodeInterrupted) — the raw-signal contract every CI wrapper reads", code)
	}
}

// TestSecondSIGINTHardKills pins the other half of Execute's contract: once the
// first SIGINT has cancelled the context, the disposition-restoring goroutine
// puts the default handler back, so a SECOND Ctrl-C kills the process outright
// instead of being absorbed by a binary that is refusing to die.
//
// The process is parked in `lint --stdin` on a pipe that never closes — a read
// that is deliberately NOT context-aware, which is exactly the state the
// second Ctrl-C exists for: the first one cannot unblock it. Unlike the test
// above there is no in-band readiness witness for "registration happened", so
// the ordering is asserted from behind instead: after the first SIGINT the
// process must still be ALIVE (proving the handler absorbed it — this is the
// assertion that fails loudly if the signal ever beat the registration), and
// only the second one may take it down, as a real signal death this time.
func TestSecondSIGINTHardKills(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinW.Close()

	cmd := startGlyph(t, stdinR, nil, "lint", "--stdin")
	stdinR.Close() // the child holds its copy; ours would only leak
	// No witness can observe the in-process registration from outside, so give
	// it generous room; the aliveness assertion below turns "not enough room"
	// into a loud failure rather than a wrong answer.
	time.Sleep(1 * time.Second)

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the first SIGINT: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("glyph died on the FIRST SIGINT while blocked in a read the context cannot cancel — " +
			"either the handler was not yet registered (racy test) or the absorb half of the contract is gone")
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the second SIGINT: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("glyph survived a second SIGINT — the default disposition was never restored, " +
			"and a hung binary cannot be Ctrl-C'd out of")
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatal("no wait status")
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Fatalf("exit state %v (code %d), want death by SIGINT — the second Ctrl-C must be the raw signal, "+
			"not another absorbed one", ws, cmd.ProcessState.ExitCode())
	}
}
