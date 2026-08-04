package markdown

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the exported-surface golden from the current source")

// TestExportedSurfaceGolden freezes this package's exported identifiers as a
// golden file. The surface IS the seal (t-3f4s): the ratified design keeps the
// escape order unforgeable by exporting the Line builder and NOTHING else, so
// re-exporting an escape pass — or adding any new exported name — is not a
// convenience, it is a new sink the order-seal no longer covers. This test is
// what makes that widening a visible, deliberate act instead of a drive-by:
// the golden diff is the contract change, `-update` rewrites it only after you
// have read it as one, and golden-gate then demands a `Golden-change: <reason>`
// trailer on every commit that carries it.
func TestExportedSurfaceGolden(t *testing.T) {
	got := exportedSurface(t)
	golden := filepath.Join("testdata", "exported-surface.golden.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (after a RATIFIED surface change, regenerate with `go test ./internal/markdown -update`)", err)
	}
	if got != string(want) {
		t.Fatalf("the exported surface drifted from its golden:\n--- golden\n%s--- source\n%s\nEvery name beyond the Line builder is a caller that can skip the escape order. Regenerate with -update only for a deliberate, stated widening — golden-gate will ask each commit for its Golden-change reason.", want, got)
	}
}

// exportedSurface renders one sorted line per exported identifier in the
// non-test sources of this directory: fully printed signatures for funcs and
// methods (a method counts only when its receiver type is exported too), and
// name-plus-kind for types, consts and vars. Unexported detail — struct fields,
// method bodies — is deliberately outside the string, so an internal refactor
// does not churn the golden.
func exportedSurface(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test sources found — the surface below would be vacuously empty")
	}
	var lines []string
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !ast.IsExported(d.Name.Name) {
					continue
				}
				if d.Recv != nil && !ast.IsExported(receiverType(d.Recv)) {
					continue
				}
				lines = append(lines, signature(t, fset, d))
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(s.Name.Name) {
							lines = append(lines, "type "+s.Name.Name+" "+typeKind(s.Type))
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if ast.IsExported(n.Name) {
								kw := "var"
								if d.Tok == token.CONST {
									kw = "const"
								}
								lines = append(lines, kw+" "+n.Name)
							}
						}
					}
				}
			}
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n") + "\n"
}

// signature prints d as source, body and doc dropped — the declaration line
// exactly as a reader of the package sees it.
func signature(t *testing.T, fset *token.FileSet, d *ast.FuncDecl) string {
	t.Helper()
	c := *d
	c.Body = nil
	c.Doc = nil
	var b strings.Builder
	if err := printer.Fprint(&b, fset, &c); err != nil {
		t.Fatalf("print %s: %v", d.Name.Name, err)
	}
	return b.String()
}

// receiverType names the receiver's type, star stripped.
func receiverType(recv *ast.FieldList) string {
	if len(recv.List) != 1 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// typeKind names what kind of type a spec declares, keeping the golden stable
// across changes to unexported internals.
func typeKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "(other)"
	}
}
