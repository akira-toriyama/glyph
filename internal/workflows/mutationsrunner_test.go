package workflows

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// fiveRows is a ledger of five rows, one patch each, so the rows a run measures
// can be read off the patch names it prints.
func fiveRows() (ledger string, patches map[string]string, names []string) {
	patches = map[string]string{}
	for i := 1; i <= 5; i++ {
		name := "row" + strconv.Itoa(i) + ".patch"
		ledger += name + "\t./p\tTestF\tF must answer one\n"
		patches[name] = fReturnsTwoPatch
		names = append(names, name)
	}
	return ledger, patches, names
}

// measuredRow is the line mutations.sh prints as it starts a row.
var measuredRow = regexp.MustCompile(`(?m)^→ \[\d+\] (\S+)$`)

func measuredRows(run stepRun) []string {
	var rows []string
	for _, m := range measuredRow.FindAllStringSubmatch(run.stdout, -1) {
		rows = append(rows, m[1])
	}
	return rows
}

// TestMutationsRunnerShardsPartitionTheLedger: CI splits the ledger across runners
// with MUTATIONS_SHARD=<k>/<n> (t-2nez). A split that loses a row leaves a decision
// nobody measures under a green line, and one that measures a row twice hides the
// loss in the count, so the shards of two must be each other's complement. Each
// shard's line names it, a shard's green being a claim about its rows alone; unset
// and empty are the whole ledger.
func TestMutationsRunnerShardsPartitionTheLedger(t *testing.T) {
	ledger, patches, all := fiveRows()
	dir := newLedgerFixture(t, ledger, patches)

	for _, c := range []struct {
		name  string
		extra []string
	}{{"unset", nil}, {"empty", []string{"MUTATIONS_SHARD="}}} {
		t.Run(c.name, func(t *testing.T) {
			run := runLedger(t, dir, c.extra...)
			if run.exit != 0 {
				t.Fatalf("mutations.sh exited %d, want 0\n%s", run.exit, run)
			}
			if got := measuredRows(run); !slices.Equal(got, all) {
				t.Errorf("the run measured %q, want every row %q", got, all)
			}
			if got, want := lastLine(run.stdout), "✓ 5/5 ledger rows bite, 5/5 patches on a row"; got != want {
				t.Errorf("the final line is %q, want %q", got, want)
			}
		})
	}

	measuredBy := map[string]string{}
	for _, shard := range []string{"1/2", "2/2"} {
		run := runLedger(t, dir, "MUTATIONS_SHARD="+shard)
		if run.exit != 0 {
			t.Fatalf("shard %s: mutations.sh exited %d, want 0\n%s", shard, run.exit, run)
		}
		rows := measuredRows(run)
		for _, row := range rows {
			if other, ok := measuredBy[row]; ok {
				t.Errorf("%s is measured by shard %s and again by shard %s", row, other, shard)
			}
			measuredBy[row] = shard
		}
		want := fmt.Sprintf("✓ %d/%d ledger rows bite in shard %s, 5/5 patches on a row", len(rows), len(rows), shard)
		if got := lastLine(run.stdout); got != want {
			t.Errorf("shard %s: the final line is %q, want %q", shard, got, want)
		}
	}
	for _, row := range all {
		if _, ok := measuredBy[row]; !ok {
			t.Errorf("no shard of two measures %s: a decision nobody measures, under two green lines", row)
		}
	}
}

// TestMutationsRunnerRefusesAMalformedShard: a value the script misread would
// measure some other slice of the ledger, or none of it, and print a green line
// over that. Every shape but <k>/<n> with 1 <= k <= n is refused in one stderr line
// naming it, at the exit MUTATIONS_JOBS's refusal uses, before the snapshot.
func TestMutationsRunnerRefusesAMalformedShard(t *testing.T) {
	dir := newLedgerFixture(t, oneRowLedger, map[string]string{"f-returns-two.patch": fReturnsTwoPatch})
	for _, value := range []string{"0/4", "5/4", "1/0", "a/b", "1", "1/4/2"} {
		run := runLedger(t, dir, "MUTATIONS_SHARD="+value)
		if run.exit != 2 {
			t.Errorf("MUTATIONS_SHARD=%s: mutations.sh exited %d, want 2\n%s", value, run.exit, run)
			continue
		}
		if !strings.Contains(run.stderr, "'"+value+"'") || !strings.Contains(run.stderr, "<k>/<n>") {
			t.Errorf("MUTATIONS_SHARD=%s: the refusal does not name the value and the accepted form\n%s", value, run)
		}
		if strings.Count(strings.TrimRight(run.stderr, "\n"), "\n") != 0 {
			t.Errorf("MUTATIONS_SHARD=%s: the refusal is not one line\n%s", value, run)
		}
		if run.stdout != "" {
			t.Errorf("MUTATIONS_SHARD=%s: the run printed before refusing, so it went on with a value it could not read\n%s", value, run)
		}
	}
}

var (
	// shardMatrix reads the shard job's matrix list: `shard: [1, 2, 3, 4]`.
	shardMatrix = regexp.MustCompile(`(?m)^ +shard: *\[([^\]\n]*)\] *$`)
	// shardDenominator reads n off the job's `MUTATIONS_SHARD: ${{ matrix.shard }}/<n>`.
	shardDenominator = regexp.MustCompile(`(?m)^ +MUTATIONS_SHARD: *\$\{\{ *matrix\.shard *\}\}/(\d+) *$`)
	needsTheShards   = regexp.MustCompile(`(?m)^    needs: *(\[ *)?mutations-shard( *\])? *$`)
	runsUnderAlways  = regexp.MustCompile(`(?m)^    if: *\$\{\{ *always\(\) *\}\} *$`)
	// shardsResult captures the env name the aggregate step reads the shards' result through.
	shardsResult = regexp.MustCompile(`(?m)^ +([A-Z_]+): *\$\{\{ *needs\.mutations-shard\.result *\}\} *$`)
)

// shardList is the matrix list in s, or false when s has none or it is not integers.
func shardList(s string) ([]int, bool) {
	m := shardMatrix.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	var list []int
	for field := range strings.SplitSeq(m[1], ",") {
		i, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			return nil, false
		}
		list = append(list, i)
	}
	return list, true
}

// buildJob is the comment-stripped text of build.yml's job id.
func buildJob(t *testing.T, id string) string {
	t.Helper()
	body := code(repoFile(t, filepath.Join(".github", "workflows", "build.yml")))
	if !strings.Contains(body, "\n  "+id+":\n") {
		t.Fatalf("build.yml declares no job %q", id)
	}
	return jobBlock(t, body, id)
}

// TestBuildMutationShardsCoverTheLedger: the matrix lists the shards and the env
// says how many there are, written apart. A matrix that loses a shard, or a
// denominator past the list, leaves rows nobody measures while every shard that
// ran is green, and so is the check (t-2nez).
func TestBuildMutationShardsCoverTheLedger(t *testing.T) {
	// Positive controls: a reader that matches nothing leaves the list compared
	// against nothing.
	if got, ok := shardList("        shard: [1, 2, 3, 4]"); !ok || !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Fatalf("shardMatrix no longer reads a real matrix list (got %v); fix the reader", got)
	}
	if m := shardDenominator.FindStringSubmatch("      MUTATIONS_SHARD: ${{ matrix.shard }}/4"); m == nil || m[1] != "4" {
		t.Fatalf("shardDenominator no longer reads a real env line (got %q); fix the reader", m)
	}

	job := buildJob(t, "mutations-shard")
	list, ok := shardList(job)
	if !ok {
		t.Fatalf("build.yml's mutations-shard job has no `shard: [...]` matrix list of integers:\n%s", job)
	}
	m := shardDenominator.FindStringSubmatch(job)
	if m == nil {
		t.Fatalf("build.yml's mutations-shard job does not set MUTATIONS_SHARD to ${{ matrix.shard }}/<n>:\n%s", job)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		t.Fatalf("the shard denominator %q is not a positive integer", m[1])
	}
	want := make([]int, n)
	for i := range want {
		want[i] = i + 1
	}
	if !slices.Equal(list, want) {
		t.Errorf("the matrix lists shards %v and MUTATIONS_SHARD divides by %d, so the list must be exactly %v: "+
			"a shard missing from it is rows nobody measures, in green", list, n, want)
	}
}

// TestBuildMutationsAggregateFailsOnAnyShardFailure: branch protection reads the
// required check by the name `mutations`, and a job whose need failed is skipped
// rather than failed. So the aggregate carries that name, needs the shards, runs
// under always(), and its step, executed, fails on every result but success.
func TestBuildMutationsAggregateFailsOnAnyShardFailure(t *testing.T) {
	// Positive controls: a reader that matches nothing proves nothing about the
	// real job.
	for re, canary := range map[*regexp.Regexp]string{
		needsTheShards:  "    needs: mutations-shard",
		runsUnderAlways: "    if: ${{ always() }}",
		shardsResult:    "          SHARDS: ${{ needs.mutations-shard.result }}",
	} {
		if !re.MatchString(canary) {
			t.Fatalf("%s no longer matches the line it was written for (%q); fix the reader", re, canary)
		}
	}

	if jobNameOverride(repoFile(t, filepath.Join(".github", "workflows", "build.yml")), "mutations") {
		t.Errorf("build.yml's mutations job declares its own name:, so the required check `mutations` never reports")
	}
	job := buildJob(t, "mutations")
	if !needsTheShards.MatchString(job) {
		t.Errorf("build.yml's mutations job does not need mutations-shard:\n%s", job)
	}
	if !runsUnderAlways.MatchString(job) {
		t.Errorf("build.yml's mutations job does not run under if: ${{ always() }}, so a failed shard skips "+
			"it rather than failing it:\n%s", job)
	}
	m := shardsResult.FindStringSubmatch(job)
	if m == nil {
		t.Fatalf("build.yml's mutations job hands its step no env bound to needs.mutations-shard.result:\n%s", job)
	}
	script := extractRun(t, job, "every shard bit")
	for _, result := range []string{"success", "failure", "cancelled", "skipped"} {
		want := 1
		if result == "success" {
			want = 0
		}
		if run := runStep(t, script, unspecifiedShell, t.TempDir(), []string{m[1] + "=" + result}); run.exit != want {
			t.Errorf("the shards ended %s and the aggregate step exited %d, want %d\n%s", result, run.exit, want, run)
		}
	}
}
