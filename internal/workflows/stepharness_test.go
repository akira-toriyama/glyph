package workflows

// Harness for executing a step's run: block rather than reading it. A guard
// over a line cannot tell whether that line runs, so the steps a fleet gate
// rests on are executed: the script as the runner writes it, under the
// invocation GitHub documents for its shell, with real tools wherever the test
// host has them and stubs only for what a test cannot reach.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The invocations GitHub documents (workflow syntax,
// jobs.<job_id>.steps[*].shell; a composite step's shell: takes the same
// table): `shell: bash` runs `bash --noprofile --norc -eo pipefail {0}`, an
// unspecified shell `bash -e {0}`. Both turn errexit on before the script's
// first line, so a `set` that merely omits -e leaves it on — only `set +e`
// turns it off.
var (
	bashShell        = []string{"--noprofile", "--norc", "-eo", "pipefail"}
	unspecifiedShell = []string{"-e"}
)

// requireTool resolves a tool a harness runs for real. Missing is a failure,
// never a skip.
func requireTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is not on PATH, and the step under test runs it — install it rather than skip: "+
			"an unrun harness is a green nobody earned", name)
	}
	return p
}

type stepRun struct {
	exit           int
	stdout, stderr string
}

func (r stepRun) String() string {
	return "stdout:\n" + r.stdout + "\nstderr:\n" + r.stderr
}

// runStep runs script the way the runner does: written to a file, the file
// handed to bash with the shell's flags. env is the whole environment —
// nothing is inherited, so the CI runner's own GITHUB_PATH, RUNNER_TEMP or
// BASH_ENV cannot reach the script under test.
func runStep(t *testing.T, script string, shell []string, dir string, env []string) stepRun {
	t.Helper()
	if strings.Contains(script, "${{") {
		t.Fatal("the step's script carries a ${{ }} expression, which the runner substitutes before " +
			"the shell starts and this harness does not — move the value into the step's env:, or " +
			"teach the harness the substitution")
	}
	path := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, requireTool(t, "bash"), append(slices.Clone(shell), path)...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	run := stepRun{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ctx.Err() == nil:
		run.exit = ee.ExitCode()
	default:
		t.Fatalf("running the step: %v\n%s", err, run)
	}
	return run
}

// shq single-quotes s for a POSIX shell.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeStub puts an executable /bin/sh script named name in dir.
func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// recordArgv is the stub line that appends the stub's argv to log as one line,
// each argument terminated by US (0x1f). No argument these stubs see carries a
// newline.
func recordArgv(log string) string {
	return `printf '%s\037' "$@" >> ` + shq(log) + "\nprintf '\\n' >> " + shq(log) + "\n"
}

// recordedCalls reads a recordArgv log back: one argv per call, in call order.
func recordedCalls(t *testing.T, log string) [][]string {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for line := range strings.Lines(string(b)) {
		calls = append(calls, strings.Split(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\x1f"), "\x1f"))
	}
	return calls
}

// readIfAny returns a file's contents, or "" when the step never wrote it.
func readIfAny(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stepShape is a steps: item as the runner sees it before running anything:
// its own keys (inline values only), how many items its sequence holds, and
// the keys enclosing that sequence, innermost first.
type stepShape struct {
	keys    map[string]string
	items   int
	parents []string
}

// readStepShape reads the item `- name: <name>` by indentation — no YAML
// library, like the rest of this package; the files it reads are
// hand-written and uniformly indented. A nested mapping or block scalar sits
// deeper than the item's keys and is skipped whole.
func readStepShape(t *testing.T, raw, name string) stepShape {
	t.Helper()
	lines := strings.Split(raw, "\n")
	at := -1
	for i, l := range lines {
		if strings.TrimLeft(l, " ") == "- name: "+name {
			if at >= 0 {
				t.Fatalf("two lines read `- name: %s` — the reader cannot tell which step the runner runs", name)
			}
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no step is named %q — every assertion over its shape would be over nothing", name)
	}
	skip := func(l string) bool {
		s := strings.TrimSpace(l)
		return s == "" || strings.HasPrefix(s, "#")
	}
	indent := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

	dash := indent(lines[at])
	shape := stepShape{keys: map[string]string{"name": name}}
	for _, l := range lines[at+1:] {
		if skip(l) {
			continue
		}
		if indent(l) <= dash {
			break
		}
		if indent(l) == dash+2 {
			k, v, _ := strings.Cut(strings.TrimSpace(l), ":")
			shape.keys[k] = strings.TrimSpace(v)
		}
	}

	// Back to the line that opens the sequence, counting its items on the way;
	// then forward from the item to the sequence's end.
	open := at
	for i := at; i >= 0; i-- {
		if skip(lines[i]) {
			continue
		}
		if indent(lines[i]) < dash {
			open = i
			break
		}
		if indent(lines[i]) == dash && strings.HasPrefix(strings.TrimSpace(lines[i]), "- ") {
			shape.items++
		}
	}
	for _, l := range lines[at+1:] {
		if skip(l) {
			continue
		}
		if indent(l) < dash {
			break
		}
		if indent(l) == dash && strings.HasPrefix(strings.TrimSpace(l), "- ") {
			shape.items++
		}
	}
	for i, in := open, -1; i >= 0; i-- {
		if skip(lines[i]) || (in >= 0 && indent(lines[i]) >= in) {
			continue
		}
		key, _, _ := strings.Cut(strings.TrimSpace(lines[i]), ":")
		shape.parents = append(shape.parents, key)
		if in = indent(lines[i]); in == 0 {
			break
		}
	}
	return shape
}

// withStepKey returns raw with key: value added to the named step, at the
// step's key indentation — the canary an absence check must catch.
func withStepKey(t *testing.T, raw, name, key, value string) string {
	t.Helper()
	marker := "- name: " + name + "\n"
	i := strings.Index(raw, marker)
	if i < 0 {
		t.Fatalf("no step is named %q", name)
	}
	lineStart := strings.LastIndex(raw[:i], "\n") + 1
	pad := strings.Repeat(" ", i-lineStart+2)
	at := i + len(marker)
	return raw[:at] + pad + key + ": " + value + "\n" + raw[at:]
}
