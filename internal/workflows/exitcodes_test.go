package workflows

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// The exit-code contract is a machine API with ONE implementation
// (internal/core/errors.go) and many hand-typed copies the compiler never
// sees: three full tables in prose, a subset table, and eighteen shell/jq
// branches across .github/workflows and scripts. A Go test that renumbered a
// code would go red everywhere at once; a doc that spells the old integer, or
// a `jq -e '.error.code == 3'` left pointing at a code glyph stopped emitting,
// goes green forever. These three tests are what stands between the two.
//
// They are deliberately not one test. The first asks "is every code written
// down", the second "does every machine consumer branch on an integer the
// binary really emits", the third "does the prose still say what 4 MEANS" —
// three different ways for the same contract to rot, three different failure
// messages.

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

	type home struct {
		file        string
		start, stop string
		// render turns a code into the token that region spells it with.
		render func(int) string
		// canary is a string the region really contains, produced by render:
		// proof the renderer still matches this file's notation before the
		// loop below can pass by matching nothing.
		canary int
	}
	backtick := func(n int) string { return "`" + strconv.Itoa(n) + "`" }
	cell := func(n int) string { return "| " + strconv.Itoa(n) + " |" }

	homes := []home{
		{file: "README.md", start: "## Exit codes", stop: "\n## ", render: backtick, canary: 0},
		{file: filepath.Join("docs", "DESIGN.md"), start: "**Exit-code contract**", stop: "\n\n**Stream contract:**", render: backtick, canary: 0},
		{file: filepath.Join("docs", "glossary.md"), start: "## 6. Exit codes and streams", stop: "\n## ", render: cell, canary: 0},
	}

	for _, h := range homes {
		t.Run(h.file, func(t *testing.T) {
			region := docRegion(t, repoFile(t, h.file), h.start, h.stop)

			// POSITIVE CONTROL, the distgate_test.go shape: prove the renderer
			// produces something this region genuinely contains before trusting
			// its verdict on the rest. A render that stopped matching this
			// file's notation would otherwise report every code as missing —
			// or, if the notation loosened, report every code as present.
			if want := h.render(h.canary); !strings.Contains(region, want) {
				t.Fatalf("the renderer for %s produced %q, which the region does not contain: "+
					"this file has changed how it spells an exit code, so the assertions below "+
					"are about a notation nothing uses", h.file, want)
			}

			for _, c := range codes {
				if want := h.render(c.Val); !strings.Contains(region, want) {
					t.Errorf("%s does not spell %s (%d) as %q. The contract has %d codes and this "+
						"copy documents fewer; a caller reading this file learns an exit it will "+
						"never handle correctly", h.file, c.Name, c.Val, want, len(codes))
				}
			}
		})
	}
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
// the commit-msg hook it generates (`fmt.Sprintf(..., int(core.CodeLint))`),
// which is why that consumer is the one copy in the tree that cannot drift.
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
	goreleaser := filepath.Join(".github", "workflows", "goreleaser.yml")
	prVerdict := filepath.Join(".github", "workflows", "pr-verdict.yml")
	build := filepath.Join(".github", "workflows", "build.yml")
	check := filepath.Join("scripts", "check.sh")
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
		{goreleaser, core.CodeNoRelease, shEq},
		{goreleaser, core.CodeOK, shNe},
		{prVerdict, core.CodeOK, shNe},
		{build, core.CodeUsage, shNe},
		{build, core.CodeLint, shNe},
		{build, core.CodeNoRelease, shNe},
		{check, core.CodeUsage, shNe},
		{check, core.CodeLint, shNe},
		{check, core.CodeNoRelease, shNe},
	}

	// POSITIVE CONTROLS for the two matchers, on real lines from the tree. A
	// regex that stopped matching would make the inventory below find zero
	// sites and agree with every row by finding nothing to disagree with.
	if got := statusBranch.FindStringSubmatch(`          if [ "$status" -eq 1 ]; then`); got == nil || got[1] != "1" {
		t.Fatalf("statusBranch no longer extracts the integer from a real shell branch (got %v)", got)
	}
	if got := jqCodeBranch.FindStringSubmatch(`jq -e '.error.code == 3' /tmp/glyph-lint.json`); got == nil || got[1] != "3" {
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

	// NON-EMPTINESS, in the shape envelope_test.go uses. Measured floor is 18
	// sites; zero means the branches were deleted or the scan broke, and both
	// deserve a red test rather than a silent pass.
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
// goreleaser.yml's executable body. Measured with this tree's binary: `notes
// --since-tag=below:notatag` exits 2 directly and 1 through `go run` (stderr
// tail: "exit status 2"); a dead-credential API walk exits 4 directly and 1
// through `go run`. The arm below the invocation reads 1 as "nothing
// release-worthy" and publishes a placeholder body — so under `go run` a
// wedged walk or a dark API ships a green release that claims there was
// nothing to say. scripts/check.sh's `go run` sites are out of scope on
// purpose: that script only pass/fails, it never branches an arm on 1.
func TestReleaseNotesRunTheBuiltBinaryNotGoRun(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml")))

	// Positive control: the matcher must still recognise the retired
	// invocation, or the absence below is asserting nothing.
	retired := `go run ./cmd/glyph notes --since-tag="below:$GITHUB_REF_NAME" > "$RUNNER_TEMP/release-notes.md" || status=$?`
	if !goRunInvocation.MatchString(retired) {
		t.Fatal("canary: goRunInvocation no longer matches the retired invocation it exists to keep out")
	}

	if loc := goRunInvocation.FindString(body); loc != "" {
		t.Errorf("goreleaser.yml's executable body invokes %q: the exit status of `go run` is "+
			"cmd/go's, not glyph's — 2/3/4 all collapse into 1, the one value the no-release arm "+
			"absorbs, so a broken walk publishes a placeholder release body behind a real tag. "+
			"Build once and run the binary", loc)
	}

	// The affirmative half: the binary is built, and the invocation runs it
	// from where it was built to.
	for _, want := range []string{
		`go build -o "$RUNNER_TEMP/glyph" ./cmd/glyph`,
		`"$RUNNER_TEMP/glyph" notes`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goreleaser.yml's notes step lost %q; the step must build the tagged tree "+
				"once and invoke that binary, so glyph's own exit codes reach the branch below it", want)
		}
	}
}

// refusalClause is the wording that separates "something broke" from "glyph
// declines to answer". Both exit 4, and a copy that mentions only the first
// teaches the next author that the second needs a new integer — in a frozen
// machine API, that is a breaking change every fleet repo would have to learn.
var refusalClause = regexp.MustCompile(`(?i)refus|could not read|could not reach`)

// TestExitCodeFourIsNotOnlyAnIOFailure guards the MEANING of 4 in the four
// places it is written down. It is a wording lock, not a semantics check, and
// says so: nothing mechanical stops a future core.APIf call site from carrying
// a fifth meaning. What it does stop is the documented meaning falling behind
// the code again — which is how it started, with README.md corrected and the
// source file itself still claiming 4 was only I/O.
func TestExitCodeFourIsNotOnlyAnIOFailure(t *testing.T) {
	// TWO-SIDED CONTROL. The matcher must bite on a real corrected sentence,
	// and must NOT bite on the bare I/O sentence it exists to reject —
	// otherwise it is a matcher that accepts everything.
	if !refusalClause.MatchString("a `release` walk that could not read its range") {
		t.Fatal("refusalClause no longer matches a sentence that correctly states the refusal; " +
			"every assertion below would demand wording this regex cannot recognise")
	}
	if refusalClause.MatchString("GitHub API / git / network / IO failure") {
		t.Fatal("refusalClause matches the I/O-only sentence, so it would accept exactly the " +
			"copy it exists to reject")
	}

	const why = "exit 4 stopped meaning only I/O the day cmd_release.go began returning it for " +
		"an incomplete walk (ratified t-pysg) and for checkPublishedFloor — deliberate refusals " +
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
		if !refusalClause.MatchString(c.Doc) {
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
	for _, h := range []struct{ file, start, stop string }{
		{"README.md", "## Exit codes", "\n## "},
		{filepath.Join("docs", "DESIGN.md"), "**Exit-code contract**", "\n\n**Stream contract:**"},
		{filepath.Join("docs", "glossary.md"), "## 6. Exit codes and streams", "\n## "},
	} {
		region := docRegion(t, repoFile(t, h.file), h.start, h.stop)
		if !refusalClause.MatchString(region) {
			t.Errorf("%s documents exit 4 without the refusal. %s", h.file, why)
		}
	}
}

// constructorDoc returns the comment block immediately above `func APIf`.
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
	var doc []string
	for j := len(lines) - 2; j >= 0; j-- {
		line := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(line, "//") {
			break
		}
		doc = append(doc, line)
	}
	if len(doc) == 0 {
		t.Fatal("APIf has no doc comment at all — the meaning of exit 4 now has one fewer home")
	}
	return strings.Join(doc, "\n")
}
