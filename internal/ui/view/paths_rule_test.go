// The rule paths.go states, held for every source file of internal/ui/view
// and internal/ui/web and their subpackages: no path is built from a record
// id anywhere but paths.go.
package view_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ruleRoots are the trees the rule covers, relative to this package.
var ruleRoots = []string{".", "../web"}

// TestNothingElseBuildsAPathFromAnID: outside paths.go, no file escapes a
// path segment (net/url's PathEscape or JoinPath, under any import name),
// joins a path (path.Join or filepath.Join) from an id, formats one
// (fmt.Sprintf and its kin with a format holding a /) from an id, or
// concatenates one (a string holding a / added to an id). A link built any
// of those ways skips Addressable, and a ".." id is quietly resolved to
// another page.
//
// An id is recognised by its name: an identifier or field named id or row,
// or ending in ID or Id, alone or inside a call. That is a convention
// rather than a type, so a value renamed out of it escapes the rule; every
// id in these packages follows it today.
func TestNothingElseBuildsAPathFromAnID(t *testing.T) {
	for _, root := range ruleRoots {
		var seen int
		err := filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || file == "paths.go" {
				return err
			}
			seen++
			for _, problem := range pathRuleProblems(t, file) {
				t.Errorf("%s: build the link with view.RecordPath or view.RowPath", problem)
			}
			return nil
		})
		if err != nil || seen == 0 {
			t.Fatalf("walking %s: %v (%d files)", root, err, seen)
		}
	}
}

// pathRuleProblems parses one file and names each place it builds a path
// from an id.
func pathRuleProblems(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Each import's local name, so an aliased net/url is still net/url.
	imported := map[string]string{}
	for _, spec := range parsed.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imported[name] = p
	}
	pkgFunc := func(e ast.Expr) (string, string) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", ""
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			return imported[x.Name], sel.Sel.Name
		}
		return "", sel.Sel.Name
	}
	var problems []string
	inChain := map[*ast.BinaryExpr]bool{}
	note := func(n ast.Node, what string) {
		problems = append(problems, fset.Position(n.Pos()).String()+": "+what)
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			switch pkg, name := pkgFunc(x); {
			case pkg == "net/url" && name == "PathEscape":
				note(x, "net/url's PathEscape")
			case name == "JoinPath":
				note(x, "JoinPath")
			}
		case *ast.CallExpr:
			pkg, name := pkgFunc(x.Fun)
			switch {
			case (pkg == "path" || pkg == "path/filepath") && name == "Join" && anyIDLike(x.Args):
				note(x, pkg+".Join with an id")
			case pkg == "fmt" && formatters[name]:
				if formatsAPath(x.Args) {
					note(x, "fmt."+name+" formatting a path from an id")
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD && !inChain[x] {
				// The whole chain is judged once, at its top; its operands
				// are still walked, so a call inside it is seen.
				operands := flattenAdd(x, inChain)
				if anyLiteralWithSlash(operands) && anyIDLike(operands) {
					note(x, "a path concatenated from an id")
				}
			}
		}
		return true
	})
	return problems
}

// formatsAPath reports whether a fmt call's arguments are a format literal
// holding a / and, after it, an id.
func formatsAPath(args []ast.Expr) bool {
	for i, a := range args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "/") {
			return anyIDLike(args[i+1:])
		}
	}
	return false
}

// formatters are the fmt functions that format a string from a format.
var formatters = map[string]bool{"Sprintf": true, "Fprintf": true, "Printf": true, "Appendf": true}

// flattenAdd lists the operands of a chain of + expressions, marking each
// inner + of the chain in inChain so it is not judged again on its own.
func flattenAdd(e ast.Expr, inChain map[*ast.BinaryExpr]bool) []ast.Expr {
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
		inChain[b] = true
		return append(flattenAdd(b.X, inChain), flattenAdd(b.Y, inChain)...)
	}
	if p, ok := e.(*ast.ParenExpr); ok {
		return flattenAdd(p.X, inChain)
	}
	return []ast.Expr{e}
}

// anyLiteralWithSlash reports whether one operand is a string literal
// holding a /.
func anyLiteralWithSlash(es []ast.Expr) bool {
	for _, e := range es {
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "/") {
			return true
		}
	}
	return false
}

// anyIDLike reports whether one expression reads as a record id.
func anyIDLike(es []ast.Expr) bool {
	for _, e := range es {
		if idLike(e) {
			return true
		}
	}
	return false
}

// idLike reports whether e reads as a record id: an identifier or field
// named id or row, or ending in ID or Id, or a call or conversion taking
// one.
func idLike(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return idName(x.Name)
	case *ast.SelectorExpr:
		return idName(x.Sel.Name)
	case *ast.CallExpr:
		return anyIDLike(x.Args)
	case *ast.ParenExpr:
		return idLike(x.X)
	case *ast.BinaryExpr:
		return idLike(x.X) || idLike(x.Y)
	}
	return false
}

// idName reports whether a name is an id's by this package's convention.
func idName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "id" || lower == "row" || lower == "rowid" || strings.HasSuffix(name, "ID") || strings.HasSuffix(name, "Id")
}
