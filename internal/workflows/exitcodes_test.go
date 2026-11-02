package workflows

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// The exit-code contract is a machine API with ONE implementation
// (internal/core/errors.go) and many hand-typed copies the compiler never
// sees: full copies in prose (two lists and a table), a subset table, and the
// shell and jq branches across .github/workflows, .github/actions and scripts.
// A Go test that renumbered a code would go red everywhere at once; a doc that
// spells the old integer, or a `jq -e '.error.code == 3'` left pointing at a
// code glyph stopped emitting, goes green forever. The tests below are what
// stands between the two.
//
// They are deliberately not one test, because each asks a different way for
// the same contract to rot and fails with its own message:
// TestExitCodeContractIsSpelledInEveryDoc "does every prose copy write every
// code, and no other", TestOutOfGoConsumersBranchOnTheContractIntegers "does
// every machine consumer branch on an integer the binary really emits",
// TestExitCodeFourIsNotOnlyAnIOFailure "does the prose still say what 4
// MEANS", and TestExitCodeFourPointerCopiesNameCodeAPIsDoc "does each copy
// that states 4 in one sentence lead to the one list of its refusals".

// contractConstants is the compile-time half of the reconciliation: every code
// the SOURCE declares must appear here and vice versa. The AST walk below can
// tell us what internal/core/errors.go says today; only a reference to the
// real identifier makes "a seventh code was added" a compile-or-fail event
// rather than something the walk silently absorbs.
var contractConstants = map[string]core.Code{
	"CodeOK":          core.CodeOK,
	"CodeNoRelease":   core.CodeNoRelease,
	"CodeUsage":       core.CodeUsage,
	"CodeLint":        core.CodeLint,
	"CodeAPI":         core.CodeAPI,
	"CodeInterrupted": core.CodeInterrupted,
}

// contractCode is one exit code as internal/core/errors.go declares it: the
// integer, and the prose attached to the constant. Both halves are read from
// the AST rather than retyped, so this file cannot be the copy that drifts.
type contractCode struct {
	Name string
	Val  int
	Doc  string
}

// contractFromSource parses internal/core/errors.go and returns every
// `Code`-typed constant with its documented meaning.
func contractFromSource(t *testing.T) []contractCode {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "core", "errors.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var out []contractCode
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// Within one const block Go carries the type down from the first spec
		// that names it, so track it rather than requiring every line to
		// repeat `Code`.
		typed := false
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); ok {
				typed = id.Name == "Code"
			}
			if !typed || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				continue
			}
			n, err := strconv.Atoi(lit.Value)
			if err != nil {
				t.Fatalf("%s: constant %s has a non-integer value %q", path, vs.Names[0].Name, lit.Value)
			}
			out = append(out, contractCode{
				Name: vs.Names[0].Name,
				Val:  n,
				Doc:  vs.Doc.Text() + vs.Comment.Text(),
			})
		}
	}

	// NON-EMPTINESS. A matcher that finds nothing must never read as "nothing
	// to check" — that is precisely how a guard of this shape dies green.
	if len(out) < 2 {
		t.Fatalf("the AST walk over %s found %d Code constants; it has stopped matching the "+
			"declaration (a const block reshaped, or the type renamed), so every assertion "+
			"below would be vacuously true", path, len(out))
	}
	for _, c := range out {
		if !strings.HasPrefix(c.Name, "Code") {
			t.Errorf("%s declares a Code-typed constant %q that does not start with \"Code\"; "+
				"the walk is picking up something that is not part of the contract", path, c.Name)
		}
	}
	return out
}

// TestExitCodeContractIsSpelledInEveryDoc reconciles the three FULL prose
// copies of the contract against the source. There are three, not two: the
// task that commissioned this test listed README.md and docs/DESIGN.md and
// missed docs/glossary.md §6, which is exactly the point — a fact with three
// homes and no reconciliation has already lost track of one of them.
func TestExitCodeContractIsSpelledInEveryDoc(t *testing.T) {
	codes := contractFromSource(t)

	// The AST and the compile-time map must agree BOTH ways. One direction
	// catches a deleted constant; the other catches an added one, which is the
	// case that matters — a seventh code stays unwritten in all three docs
	// until someone notices, and nothing else in the tree would notice.
	seen := map[string]bool{}
	for _, c := range codes {
		seen[c.Name] = true
		if _, ok := contractConstants[c.Name]; !ok {
			t.Errorf("internal/core/errors.go declares %s = %d but this test's contractConstants "+
				"does not list it: a new exit code must be added here, spelled in all three doc "+
				"tables, and either branched on by a consumer or listed in notBranched with a "+
				"reason", c.Name, c.Val)
		}
	}
	for name := range contractConstants {
		if !seen[name] {
			t.Errorf("contractConstants lists %s but internal/core/errors.go no longer declares "+
				"it; a code was removed from a FROZEN machine API — every fleet repo's lint gate "+
				"branches on these integers", name)
		}
	}

	for _, d := range exitCodeDocs {
		t.Run(d.file, func(t *testing.T) {
			entries := d.entries(t, d.file, docRegion(t, repoFile(t, d.file), d.start, d.stop))

			// POSITIVE CONTROL, the distgate_test.go shape: prove the parser
			// finds entries in this region before trusting its verdict on the
			// rest. A parser that stopped matching this file's notation would
			// otherwise report every code as missing for the wrong reason.
			if len(entries) == 0 {
				t.Fatalf("%s: the region holds no entry this file's notation opens with a code — "+
					"the file has changed how it writes the contract, so the assertions below "+
					"are about a notation nothing uses", d.file)
			}

			declared := map[int]bool{}
			for _, c := range codes {
				declared[c.Val] = true
				if _, ok := entries[c.Val]; !ok {
					t.Errorf("%s has no entry of its own for %s (%d), reading the copy as %s. The "+
						"contract has %d codes and this copy documents fewer; a caller reading this file "+
						"learns an exit it will never handle correctly", d.file, c.Name, c.Val, d.notation, len(codes))
				}
			}
			for _, n := range slices.Sorted(maps.Keys(entries)) {
				if !declared[n] {
					t.Errorf("%s has an entry for exit %d, which internal/core/errors.go does not "+
						"declare; a caller reading this file branches on an exit glyph never emits", d.file, n)
				}
			}
		})
	}
}

// exitCodeDoc is one full prose copy of the contract: the anchors of its
// region, how that file writes one code's entry (and that notation in words,
// for the failure message), and whether its `4` entry is a pointer copy
// (TestExitCodeFourPointerCopiesNameCodeAPIsDoc).
type exitCodeDoc struct {
	file        string
	start, stop string
	entries     func(t *testing.T, file, region string) map[int]string
	notation    string
	pointer     bool
}

const (
	listNotation = "the region's first paragraph, one list of \" · \"-separated items each opening with a backticked code"
	rowNotation  = "the rows of the region's first table, one \"| N | … |\" row per code, up to the first blank line"
)

// exitCodeDocs are the three full prose copies. Each assertion reads one
// code's own entry, never the whole region: DESIGN §5's region runs on for a
// hundred lines past the list, where `4` and the refusal words recur, so a
// region-wide match stays green with DESIGN's own `4` entry stripped of its
// refusal or deleted.
var exitCodeDocs = []exitCodeDoc{
	{file: "README.md", start: "## Exit codes", stop: "\n## ", entries: listEntries, notation: listNotation},
	{file: filepath.Join("docs", "DESIGN.md"), start: "**Exit-code contract** (`internal/core`):", stop: "\n\n**Stream contract:**", entries: listEntries, notation: listNotation, pointer: true},
	{file: filepath.Join("docs", "glossary.md"), start: "## 6. Exit codes and streams", stop: "\n## ", entries: rowEntries, notation: rowNotation, pointer: true},
}

var (
	listItemCode = regexp.MustCompile("^`([0-9]+)`")
	tableRowCode = regexp.MustCompile(`^\| ([0-9]+) \|`)
)

// listEntries reads the contract as README.md and DESIGN §5 write it: the
// region's first paragraph is one list of "`N` meaning" items separated by
// " · ". An item that does not open with a backticked code means the
// notation changed, and the test stops rather than guess.
func listEntries(t *testing.T, file, region string) map[int]string {
	t.Helper()
	para, _, _ := strings.Cut(strings.TrimSpace(region), "\n\n")
	out := map[int]string{}
	for item := range strings.SplitSeq(oneLine(para), " · ") {
		m := listItemCode.FindStringSubmatch(item)
		if m == nil {
			t.Fatalf("%s: the exit-code list holds an item that opens with no backticked code: %q", file, item)
		}
		addEntry(t, file, out, m[1], item)
	}
	return out
}

// rowEntries reads the contract as glossary §6 writes it: the region's first
// table, one "| N | … |" row per code, up to the first blank line. A later
// table in the section that happens to hold a code is not the contract.
func rowEntries(t *testing.T, file, region string) map[int]string {
	t.Helper()
	out := map[int]string{}
	lines := strings.Split(region, "\n")
	first := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "|") })
	if first < 0 {
		return out
	}
	for _, line := range lines[first:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		if m := tableRowCode.FindStringSubmatch(line); m != nil {
			addEntry(t, file, out, m[1], line)
		}
	}
	return out
}

func addEntry(t *testing.T, file string, entries map[int]string, code, entry string) {
	t.Helper()
	n, err := strconv.Atoi(code)
	if err != nil {
		t.Fatalf("%s: exit code %q is not an integer: %v", file, code, err)
	}
	if _, dup := entries[n]; dup {
		t.Errorf("%s writes exit code %d in two entries; a reader cannot tell which one is the contract", file, n)
	}
	entries[n] = entry
}

// docRegion slices [start, stop) out of text by literal anchor. A moved anchor
// is a t.Fatal rather than a silent whole-file match: a region that grew to the
// whole document would find every integer somewhere and report success about
// nothing.
func docRegion(t *testing.T, text, start, stop string) string {
	t.Helper()
	i := strings.Index(text, start)
	if i < 0 {
		t.Fatalf("the anchor %q is gone — the heading was renamed or the section moved. Point "+
			"this test at the new anchor; do not delete the assertion", start)
	}
	rest := text[i+len(start):]
	j := strings.Index(rest, stop)
	if j < 0 {
		t.Fatalf("the closing anchor %q never appears after %q, so the region runs to the end of "+
			"the file and would match integers belonging to some other section", stop, start)
	}
	region := rest[:j]
	if strings.TrimSpace(region) == "" {
		t.Fatalf("the region between %q and %q is empty", start, stop)
	}
	return region
}

// statusBranch and jqCodeBranch find the shell and jq consumers of the
// contract. Measured against the tree: together they find every hand-typed
// branch under .github/workflows and scripts, and nothing else.
var (
	statusBranch = regexp.MustCompile(`"\$status"\s*-(?:eq|ne)\s*([0-9]+)`)
	jqCodeBranch = regexp.MustCompile(`\.error\.code\s*==\s*([0-9]+)`)
)

// consumer is one machine branch, with the integer FORMATTED from the constant
// rather than typed. This is the shape internal/hook/hook.go already uses for
// the hooks it generates (`fmt.Sprintf(..., int(core.CodeLint))`), which is
// why those consumers are the copies in the tree that cannot drift.
// Everything below is the same idea applied to the copies glyph does not write.
type consumer struct {
	file string
	code core.Code
	form func(core.Code) string
}

func shEq(c core.Code) string { return fmt.Sprintf(`"$status" -eq %d`, int(c)) }
func shNe(c core.Code) string { return fmt.Sprintf(`"$status" -ne %d`, int(c)) }
func jqEq(c core.Code) string { return fmt.Sprintf(`.error.code == %d`, int(c)) }

// TestOutOfGoConsumersBranchOnTheContractIntegers has two halves, and needs
// both. The rows prove each known consumer still branches on the integer its
// constant carries TODAY. The inventory proves no consumer appeared that
// nobody derived from a constant — without it the rows would pass forever
// while a new hand-typed branch rots beside them.
func TestOutOfGoConsumersBranchOnTheContractIntegers(t *testing.T) {
	lint := filepath.Join(".github", "workflows", "lint.yml")
	release := filepath.Join(".github", "workflows", "release.yml")
	releaseNotes := filepath.FromSlash(releaseNotesAction)
	prVerdict := filepath.Join(".github", "workflows", "pr-verdict.yml")
	smoke := filepath.Join("scripts", "smoke.sh")
	preflight := filepath.Join("scripts", "fleet-preflight.sh")

	rows := []consumer{
		// fleet-preflight decides what counts as an ANSWER from a probe, and
		// therefore which repos it reports as changing. A stale integer there
		// does not fail loudly — it silently reclassifies a verdict as a broken
		// probe, or the reverse, in the one instrument a tag is cut on.
		{preflight, core.CodeOK, shEq},
		{preflight, core.CodeNoRelease, shEq},
		{preflight, core.CodeLint, shEq},
		{lint, core.CodeOK, shEq},
		{lint, core.CodeLint, jqEq},
		// The push arm swallows the gate code — and only the gate code — after
		// annotating: default-branch history is immutable, so a red verdict
		// there never turns green again. A stale integer here fails open in
		// one direction (violations start redding a check nobody can fix) and
		// closed in the other (a rerunnable infra failure gets absorbed).
		{lint, core.CodeLint, shEq},
		{release, core.CodeNoRelease, shEq},
		{release, core.CodeOK, shNe},
		{releaseNotes, core.CodeNoRelease, shEq},
		// The tag-time notes action ships the gate code as it ships 1: the
		// walk below the wedge escape's own tag reaches the commit it was cut
		// to clear, on every re-run (t-bcdz). A stale integer here kills that
		// tag's release job again, behind a tag that already exists — in
		// goreleaser.yml and in every fleet repository that runs the action.
		{releaseNotes, core.CodeLint, shEq},
		{releaseNotes, core.CodeOK, shNe},
		{prVerdict, core.CodeOK, shNe},
		{smoke, core.CodeUsage, shNe},
		{smoke, core.CodeLint, shNe},
		{smoke, core.CodeNoRelease, shNe},
	}

	// POSITIVE CONTROLS for the two matchers, on real lines from the tree. A
	// regex that stopped matching would make the inventory below find zero
	// sites and agree with every row by finding nothing to disagree with.
	if got := statusBranch.FindStringSubmatch(`          if [ "$status" -eq 1 ]; then`); got == nil || got[1] != "1" {
		t.Fatalf("statusBranch no longer extracts the integer from a real shell branch (got %v)", got)
	}
	if got := jqCodeBranch.FindStringSubmatch(`jq -e '.error.code == 3' "$RUNNER_TEMP/glyph-lint.json"`); got == nil || got[1] != "3" {
		t.Fatalf("jqCodeBranch no longer extracts the integer from a real jq branch (got %v)", got)
	}

	claimed := map[string]bool{}
	for _, r := range rows {
		body := repoFile(t, r.file)
		want := r.form(r.code)
		if !strings.Contains(body, want) {
			t.Errorf("%s does not contain %q. Either the branch moved to a different integer — "+
				"and it is now testing for an exit glyph does not emit there — or the assertion "+
				"is stale. Both are the drift this test exists for", r.file, want)
		}
		claimed[fmt.Sprintf("%s:%d", r.file, int(r.code))] = true
	}

	// INVENTORY. Walk every shell-bearing file and check what it really
	// branches on.
	valid := map[int]string{}
	for _, c := range contractFromSource(t) {
		valid[c.Val] = c.Name
	}

	var files []string
	for _, name := range workflowFiles(t) {
		files = append(files, filepath.Join(".github", "workflows", name))
	}
	actions, err := filepath.Glob(filepath.Join("..", "..", ".github", "actions", "*", "action.yml"))
	if err != nil || len(actions) == 0 {
		t.Fatalf("listing the composite actions: %v (%d found)", err, len(actions))
	}
	for _, a := range actions {
		files = append(files, filepath.Join(".github", "actions", filepath.Base(filepath.Dir(a)), "action.yml"))
	}
	scripts := filepath.Join("..", "..", "scripts")
	entries, err := os.ReadDir(scripts)
	if err != nil {
		t.Fatalf("reading %s: %v", scripts, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sh") {
			files = append(files, filepath.Join("scripts", e.Name()))
		}
	}

	sites := 0
	for _, f := range files {
		body := repoFile(t, f)
		for _, re := range []*regexp.Regexp{statusBranch, jqCodeBranch} {
			for _, m := range re.FindAllStringSubmatch(body, -1) {
				sites++
				n, err := strconv.Atoi(m[1])
				if err != nil {
					continue
				}
				if _, ok := valid[n]; !ok {
					t.Errorf("%s branches on exit %d, which internal/core/errors.go does not "+
						"declare: the consumer is waiting for a status the binary never "+
						"returns, so its arm is dead code that reads as a live check", f, n)
					continue
				}
				if !claimed[fmt.Sprintf("%s:%d", f, n)] {
					t.Errorf("%s branches on exit %d and no row above claims it. A new "+
						"hand-typed branch is exactly what this test inventories: add a "+
						"consumer row so the integer is FORMATTED from core.%s and can never "+
						"drift from the constant", f, n, valid[n])
				}
			}
		}
	}

	// NON-EMPTINESS, in the shape envelope_test.go uses: zero means the branches
	// were deleted or the scan broke, and both deserve a red test rather than a
	// silent pass.
	if sites == 0 {
		t.Error("the scan found no exit-code branches at all under .github/workflows or " +
			"scripts. Either the fleet's machine consumers are gone, or this test is now " +
			"asserting nothing — do not 'fix' it by deleting the assertion")
	}

	// COVERAGE ACCOUNTING: every code is branched on somewhere, or is listed
	// here with the reason it is not. A seventh code fails until someone
	// writes its reason down.
	notBranched := map[core.Code]string{
		core.CodeAPI:         "no consumer compares 4: lint.yml hard-fails only on 3 and treats every other non-zero as infrastructure, so 4 is handled by NOT matching",
		core.CodeInterrupted: "130 is emitted silently with no error envelope, so there is nothing for a consumer to branch on",
	}
	branched := map[core.Code]bool{}
	for _, r := range rows {
		branched[r.code] = true
	}
	var unaccounted []string
	for name, c := range contractConstants {
		if branched[c] {
			continue
		}
		if _, ok := notBranched[c]; !ok {
			unaccounted = append(unaccounted, fmt.Sprintf("%s (%d)", name, int(c)))
		}
	}
	sort.Strings(unaccounted)
	if len(unaccounted) > 0 {
		t.Errorf("no consumer row branches on %s and notBranched gives no reason. Either a "+
			"machine consumer stopped checking a code it used to check, or a new code arrived "+
			"and nothing outside Go knows about it", strings.Join(unaccounted, ", "))
	}
}

// goRunInvocation matches an executable `go run` invocation. The exit status
// of `go run` is cmd/go's, not the compiled binary's (`go help run` says so in
// as many words): glyph's 2, 3 and 4 all arrive as 1. In a step that branches
// on the contract integers, that launders every hard failure into the one
// value the soft no-release arm absorbs.
var goRunInvocation = regexp.MustCompile(`\bgo run\b`)

// TestReleaseNotesRunTheBuiltBinaryNotGoRun keeps `go run` out of
// goreleaser.yml's and the release-notes action's executable bodies. Measured
// with this tree's binary: `notes --since-tag=below:notatag` exits 2 directly
// and 1 through `go run` (stderr tail: "exit status 2"); a dead-credential API
// walk exits 4 directly and 1 through `go run`. The action's arm below the
// invocation reads 1 as "nothing release-worthy" and publishes a placeholder
// body — so under `go run` a wedged walk or a dark API ships a green release
// that claims there was nothing to say. scripts/check.sh's `go run` sites are
// out of scope on purpose: that script only pass/fails, it never branches an
// arm on 1.
func TestReleaseNotesRunTheBuiltBinaryNotGoRun(t *testing.T) {
	workflow := code(repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml")))
	action := code(repoFile(t, filepath.FromSlash(releaseNotesAction)))

	// Positive control: the matcher must still recognise the retired
	// invocation, or the absence below is asserting nothing.
	retired := `go run ./cmd/glyph notes --since-tag="below:$GITHUB_REF_NAME" > "$RUNNER_TEMP/release-notes.md" || status=$?`
	if !goRunInvocation.MatchString(retired) {
		t.Fatal("canary: goRunInvocation no longer matches the retired invocation it exists to keep out")
	}

	for name, body := range map[string]string{"goreleaser.yml": workflow, "the release-notes action": action} {
		if loc := goRunInvocation.FindString(body); loc != "" {
			t.Errorf("%s's executable body invokes %q: the exit status of `go run` is "+
				"cmd/go's, not glyph's — 2/3/4 all collapse into 1, the one value the no-release arm "+
				"absorbs, so a broken walk publishes a placeholder release body behind a real tag. "+
				"Build once and run the binary", name, loc)
		}
	}

	// The affirmative half: goreleaser.yml builds the binary and hands the
	// action its path, and the action runs what it was handed.
	for _, want := range []string{
		`go build -o "$RUNNER_TEMP/glyph" ./cmd/glyph`,
		"glyph: ${{ runner.temp }}/glyph",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("goreleaser.yml lost %q; the tagged tree must be built once and that binary "+
				"handed to the notes action, so glyph's own exit codes reach the arms there", want)
		}
	}
	if want := `"$GLYPH" notes --since-tag="below:$tag"`; !strings.Contains(action, want) {
		t.Errorf("the release-notes action lost %q; it must run the binary it was handed", want)
	}
}

// refusalClause is the wording that separates "something broke" from "glyph
// declines to answer". Both exit 4, and a copy that mentions only the first
// teaches the next author that the second needs a new integer — in a frozen
// machine API, that is a breaking change every fleet repo would have to learn.
// It wants the refusal as a clause — "refusal to", "refuses" — because the
// pointer the copies carry, "CodeAPI's doc lists the refusals", holds the bare
// word, and a copy that kept the pointer and lost the clause would pass a
// match on it.
var refusalClause = regexp.MustCompile(`(?i)refusal to|refuses|could not read|could not reach`)

// TestExitCodeFourIsNotOnlyAnIOFailure guards the MEANING of 4 in the five
// homes it is written down in, across four files. It is a wording lock, not a
// semantics check, and says so: nothing mechanical stops a future core.APIf
// call site from carrying a new meaning. What it does stop is the documented
// meaning falling behind the code again.
func TestExitCodeFourIsNotOnlyAnIOFailure(t *testing.T) {
	// THREE-SIDED CONTROL. The matcher must bite on a real corrected sentence,
	// and must NOT bite on the bare I/O sentence it exists to reject, nor on
	// the pointer alone — otherwise it accepts a copy that says only I/O.
	if !refusalClause.MatchString("a `release` walk that could not read its range") {
		t.Fatal("refusalClause no longer matches a sentence that correctly states the refusal; " +
			"every assertion below would demand wording this regex cannot recognise")
	}
	if refusalClause.MatchString("GitHub API / git / network / IO failure") {
		t.Fatal("refusalClause matches the I/O-only sentence, so it would accept exactly the " +
			"copy it exists to reject")
	}
	if refusalClause.MatchString("a failure to reach git, the API or the filesystem (CodeAPI's doc lists the refusals)") {
		t.Fatal("refusalClause matches the pointer alone, so a copy that keeps the pointer and " +
			"drops its refusal clause would pass")
	}

	const why = "exit 4 stopped meaning only I/O the day cmd_release.go began returning it for " +
		"an incomplete walk (ratified t-pysg) and for the published floor — deliberate refusals " +
		"with nothing broken underneath. State that here, or the next author reads 4 as " +
		"\"retry later\" and reaches for a new integer"

	// Home one: the constant's own prose, read from the AST so this cannot be
	// satisfied by a comment somewhere else in the file.
	var found bool
	for _, c := range contractFromSource(t) {
		if c.Name != "CodeAPI" {
			continue
		}
		found = true
		if !refusalClause.MatchString(oneLine(c.Doc)) {
			t.Errorf("internal/core/errors.go: CodeAPI's own documentation does not mention the "+
				"refusal. %s.\nIt currently says: %q", why, strings.TrimSpace(c.Doc))
		}
	}
	if !found {
		t.Fatal("internal/core/errors.go declares no CodeAPI constant — the AST walk or the " +
			"contract itself has changed shape")
	}

	// Home two: APIf's doc comment, the constructor every call site reads.
	if body := repoFile(t, filepath.Join("internal", "core", "errors.go")); !refusalClause.MatchString(constructorDoc(t, body)) {
		t.Errorf("internal/core/errors.go: APIf's doc comment does not mention the refusal. %s", why)
	}

	// Homes three to five: the prose copies. Each must say it in the sentence
	// that defines 4, not merely somewhere in the file.
	for _, d := range exitCodeDocs {
		entry, ok := d.entries(t, d.file, docRegion(t, repoFile(t, d.file), d.start, d.stop))[int(core.CodeAPI)]
		if !ok {
			t.Errorf("%s has no entry of its own for exit 4. %s", d.file, why)
			continue
		}
		if !refusalClause.MatchString(entry) {
			t.Errorf("%s documents exit 4 without the refusal. %s", d.file, why)
		}
	}
}

// homePointer is how a pointer copy names the home of exit 4's refusals:
// CodeAPI's doc, with or without the backtick a Markdown copy closes the
// identifier with. Naming the constant is not enough — "builds a CodeAPI
// error" names it and points nowhere.
var homePointer = regexp.MustCompile("CodeAPI`?'s doc")

// TestExitCodeFourPointerCopiesNameCodeAPIsDoc holds the one-list rule for
// exit 4: CodeAPI's doc is the one list of the refusals, and APIf's doc,
// DESIGN §5's `4` entry and glossary §6's `4` row state the meaning in one
// sentence that names it. README's list is the user's and CodeAPI's doc is the home,
// so neither is asked to point anywhere. A sibling of
// TestExitCodeFourIsNotOnlyAnIOFailure rather than part of it: that test asks
// whether a copy states the refusal, this one whether a pointer copy leads to
// the list.
func TestExitCodeFourPointerCopiesNameCodeAPIsDoc(t *testing.T) {
	// TWO-SIDED CONTROL: the matcher accepts each pointer's real spelling and
	// rejects a mention of the constant that points nowhere.
	for _, spelling := range []string{"(CodeAPI's doc lists the refusals)", "(`core.CodeAPI`'s doc lists the refusals)", "(`CodeAPI`'s doc lists the refusals)"} {
		if !homePointer.MatchString(spelling) {
			t.Fatalf("homePointer does not match %q, a pointer this tree writes", spelling)
		}
	}
	if homePointer.MatchString("APIf builds a CodeAPI error") {
		t.Fatal("homePointer matches a mention of CodeAPI that names no doc, so it would accept a copy that points nowhere")
	}

	const rule = "CodeAPI's doc in internal/core/errors.go is the one list of the refusals that exit " +
		"with 4; this copy states the meaning in one sentence and must name CodeAPI's doc, so a reader " +
		"lands on the list and the copy has no reason to grow a list of its own (DESIGN §5's grew one " +
		"that named neither the published floor nor the walk cap)"

	if doc := constructorDoc(t, repoFile(t, filepath.Join("internal", "core", "errors.go"))); !homePointer.MatchString(doc) {
		t.Errorf("internal/core/errors.go: APIf's doc comment does not name CodeAPI's doc. %s", rule)
	}
	pointers := 0
	for _, d := range exitCodeDocs {
		if !d.pointer {
			continue
		}
		pointers++
		entry, ok := d.entries(t, d.file, docRegion(t, repoFile(t, d.file), d.start, d.stop))[int(core.CodeAPI)]
		if !ok {
			t.Errorf("%s has no entry of its own for exit 4. %s", d.file, rule)
			continue
		}
		if !homePointer.MatchString(entry) {
			t.Errorf("%s: exit 4's entry does not name CodeAPI's doc. %s", d.file, rule)
		}
	}
	if pointers == 0 {
		t.Fatal("exitCodeDocs marks no prose copy as a pointer, so the loop above checked nothing")
	}
}

// constructorDoc returns the comment block immediately above `func APIf`, its
// `//` markers stripped and its lines joined (oneLine), so a phrase the
// assertions look for matches however gofmt or an author wraps it.
func constructorDoc(t *testing.T, body string) string {
	t.Helper()
	before, _, found := strings.Cut(body, "func APIf(")
	if !found {
		t.Fatal("internal/core/errors.go no longer declares APIf; the constructor this test " +
			"reads has been renamed")
	}
	// The cut lands mid-line at `func`, so the split's last element is the
	// empty fragment before it; the comment block starts one line above.
	lines := strings.Split(before, "\n")
	first := len(lines) - 1
	for first > 0 && strings.HasPrefix(strings.TrimSpace(lines[first-1]), "//") {
		first--
	}
	if first == len(lines)-1 {
		t.Fatal("APIf has no doc comment at all — the meaning of exit 4 now has one fewer home")
	}
	doc := make([]string, 0, len(lines)-1-first)
	for _, line := range lines[first : len(lines)-1] {
		doc = append(doc, strings.TrimPrefix(strings.TrimSpace(line), "//"))
	}
	return oneLine(strings.Join(doc, " "))
}

// oneLine collapses every run of whitespace, line breaks included, to one
// space: prose is matched as a reader reads it, not as it happens to wrap.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
