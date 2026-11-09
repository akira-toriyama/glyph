package workflows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

const (
	ledgerFixtureTest = "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F() != 1 {\n\t\tt.Fatal(\"F is not one\")\n\t}\n}\n"

	// fReturnsTwoPatch breaks F; TestF is what must then fail.
	fReturnsTwoPatch = "diff --git a/p/p.go b/p/p.go\n--- a/p/p.go\n+++ b/p/p.go\n@@ -1,3 +1,3 @@\n package p\n \n" +
		"-func F() int { return 1 }\n+func F() int { return 2 }\n"

	// testDoesNotBuildPatch leaves every package's source compiling and breaks the
	// test binary alone, so `go build ./...` has nothing to say about it.
	testDoesNotBuildPatch = "diff --git a/p/p_test.go b/p/p_test.go\n--- a/p/p_test.go\n+++ b/p/p_test.go\n@@ -7,3 +7,5 @@\n" +
		" \t\tt.Fatal(\"F is not one\")\n \t}\n }\n+\n+func zzBroken() { undefinedSymbol() }\n"
)

// newLedgerFixture is scripts/mutations.sh's real text over a one-package module,
// a ledger of one row, under the real git and go: the script's reconciliation and
// its refusals are about what those two tools do. .git is there because the
// snapshot lists the tree through it; nothing need be committed, since it takes
// untracked files too.
func newLedgerFixture(t *testing.T, ledger string, patches map[string]string) string {
	t.Helper()
	testutil.RequireGit(t)
	requireTool(t, "go")
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"scripts/mutations.sh":          repoFile(t, "scripts/mutations.sh"),
		"scripts/lib/trap.sh":           repoFile(t, "scripts/lib/trap.sh"),
		"go.mod":                        fuzzFixtureMod,
		"p/p.go":                        "package p\n\nfunc F() int { return 1 }\n",
		"p/p_test.go":                   ledgerFixtureTest,
		"testdata/mutations/ledger.tsv": ledger,
	} {
		testutil.WriteFile(t, dir, rel, content)
	}
	for name, patch := range patches {
		testutil.WriteFile(t, dir, "testdata/mutations/"+name, patch)
	}
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	return dir
}

func runLedger(t *testing.T, dir string, extra ...string) stepRun {
	t.Helper()
	env := append(realGoEnv(t), "MUTATIONS_JOBS=2")
	return execute(t, []string{requireTool(t, "sh"), filepath.Join(dir, "scripts", "mutations.sh")}, dir, append(env, extra...))
}

// lastLine returns s's last non-empty line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

const oneRowLedger = "f-returns-two.patch\t./p\tTestF\tF must answer one\n"

// TestMutationsRunnerReconcilesPatchesAgainstTheLedger: a patch that loses its row
// runs nowhere and says nothing. Unreconciled, a ledger cut to one row prints `✓ 1/1`
// over 434 patches pinning nothing (measured 2026-11-04; t-8nfr), so the final line
// sets the two sets against each other.
func TestMutationsRunnerReconcilesPatchesAgainstTheLedger(t *testing.T) {
	t.Run("every patch has a row", func(t *testing.T) {
		dir := newLedgerFixture(t, oneRowLedger, map[string]string{"f-returns-two.patch": fReturnsTwoPatch})
		run := runLedger(t, dir)
		if run.exit != 0 {
			t.Fatalf("mutations.sh exited %d, want 0\n%s", run.exit, run)
		}
		if got, want := lastLine(run.stdout), "✓ 1/1 ledger rows bite, 1/1 patches on a row"; got != want {
			t.Errorf("the final line is %q, want %q", got, want)
		}
	})

	t.Run("a patch with no row", func(t *testing.T) {
		dir := newLedgerFixture(t, oneRowLedger, map[string]string{
			"f-returns-two.patch": fReturnsTwoPatch,
			"orphan.patch":        fReturnsTwoPatch,
		})
		run := runLedger(t, dir)
		if run.exit != 1 {
			t.Fatalf("mutations.sh exited %d, want 1\n%s", run.exit, run)
		}
		if !strings.Contains(run.stderr, "no ledger row names orphan.patch") {
			t.Errorf("the refusal does not name the patch\n%s", run)
		}
		if got, want := lastLine(run.stderr), "✗ 1/1 ledger rows bite, 1/2 patches on a row — 0 row(s) and 1 patch(es) above need a look"; got != want {
			t.Errorf("the final line is %q, want %q", got, want)
		}
	})
}

// TestMutationsRunnerSnapshotsTheWorkingTree: the snapshot is the working tree, and
// a tracked file deleted and not staged is not in it. `git ls-files --cached` still
// lists one and tar cannot stat it, so a snapshot judged by tar's status alone
// refuses the edit/measure loop over a deletion git rm would have hidden (measured
// 2026-11-04, deleting a patch ahead of re-cutting it).
func TestMutationsRunnerSnapshotsTheWorkingTree(t *testing.T) {
	dir := newLedgerFixture(t, oneRowLedger, map[string]string{"f-returns-two.patch": fReturnsTwoPatch})
	testutil.WriteFile(t, dir, "p/gone.go", "package p\n")
	testutil.Git(t, dir, "akira-toriyama", "add", "-A")
	if err := os.Remove(filepath.Join(dir, "p", "gone.go")); err != nil {
		t.Fatal(err)
	}
	run := runLedger(t, dir)
	if run.exit != 0 {
		t.Fatalf("mutations.sh exited %d, want 0\n%s", run.exit, run)
	}
	if got, want := lastLine(run.stdout), "✓ 1/1 ledger rows bite, 1/1 patches on a row"; got != want {
		t.Errorf("the final line is %q, want %q", got, want)
	}
}

// TestMutationsRunnerRefusesAMutationThatBreaksTheTestBinary: `go build ./...` reads
// no _test.go, so a patch breaking only a test file passes that check, fails every
// test for the compiler's reasons, and reads as `✓ killed` (measured 2026-11-04 on
// internal/version; t-8nfr). The row's own package has to build with its tests.
func TestMutationsRunnerRefusesAMutationThatBreaksTheTestBinary(t *testing.T) {
	dir := newLedgerFixture(t, "test-does-not-build.patch\t./p\tTestF\tF must answer one\n",
		map[string]string{"test-does-not-build.patch": testDoesNotBuildPatch})
	run := runLedger(t, dir)
	if run.exit != 1 {
		t.Fatalf("mutations.sh exited %d, want 1\n%s", run.exit, run)
	}
	if !strings.Contains(run.stderr, "does not COMPILE") {
		t.Errorf("the refusal does not say the mutated tree does not compile\n%s", run)
	}
	if strings.Contains(run.stdout, "killed") {
		t.Errorf("a build error was credited as a kill\n%s", run)
	}
}

// TestMutationsRunnerNamesWhyItCannotMeasure: a run that cannot measure says what
// stopped it. Reading the empty listing either of these leaves as "the ledger
// names a test that does not exist" sends the reader to a ledger with nothing wrong
// in it, at the right exit and for the wrong reason (measured 2026-11-04 on a `git
// archive` tree; t-8nfr).
func TestMutationsRunnerNamesWhyItCannotMeasure(t *testing.T) {
	const blamesTheLedger = "has no test named"

	t.Run("a tree git cannot list", func(t *testing.T) {
		dir := newLedgerFixture(t, oneRowLedger, map[string]string{"f-returns-two.patch": fReturnsTwoPatch})
		if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
			t.Fatal(err)
		}
		run := runLedger(t, dir, "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
		if run.exit != 1 {
			t.Fatalf("mutations.sh exited %d, want 1\n%s", run.exit, run)
		}
		if !strings.Contains(run.stderr, "cannot snapshot the working tree") {
			t.Errorf("the refusal does not say the snapshot failed\n%s", run)
		}
		if strings.Contains(run.stdout, "baselining") {
			t.Errorf("the run went on to measure rows with no snapshot to measure them in\n%s", run)
		}
		if strings.Contains(run.stderr, blamesTheLedger) {
			t.Errorf("the refusal blames the ledger for an unlistable tree\n%s", run)
		}
	})

	t.Run("a package whose tests do not build", func(t *testing.T) {
		dir := newLedgerFixture(t, oneRowLedger, map[string]string{"f-returns-two.patch": fReturnsTwoPatch})
		testutil.WriteFile(t, dir, "p/broken_test.go", "package p\n\nfunc init() { undefinedSymbol() }\n")
		run := runLedger(t, dir)
		if run.exit != 1 {
			t.Fatalf("mutations.sh exited %d, want 1\n%s", run.exit, run)
		}
		for _, want := range []string{"go test -list cannot read ./p", "undefined: undefinedSymbol"} {
			if !strings.Contains(run.stderr, want) {
				t.Errorf("the refusal does not carry %q\n%s", want, run)
			}
		}
		if strings.Contains(run.stderr, blamesTheLedger) {
			t.Errorf("the refusal blames the ledger for a package that does not build\n%s", run)
		}
	})
}
