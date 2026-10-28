// Architecture rule for the far side's words (Phase 131, R1): no client
// puts a remote answer into an error by its first line. pkg/farside is the
// one helper that turns an answer into error text.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// farsideScanRoots are the trees whose code talks to a device, a controller
// or an API. tools/ is left out: its programs read this repository's own
// test output, never a far side.
var farsideScanRoots = []string{"pkg", "internal", "cmd"}

// firstLineHelperName matches a function named as a first-line helper,
// the shape every client this rule replaced had: firstLine, firstNonEmptyLine,
// firstLineOf, headLine, firstOf.
var firstLineHelperName = regexp.MustCompile(`(?i)^(first(non)?(empty|blank)?line(of)?|headline|firstof)$`)

// errorBuilders are the calls an error's text is made in.
var errorBuilders = map[string]map[string]bool{
	"fmt":    {"Errorf": true, "Sprintf": true},
	"errors": {"New": true},
}

// TestNoClientCutsTheFarSideToAFirstLine keeps R1: a refusal from a device,
// controller or API reaches the error with its own words, through
// pkg/farside (JSON error fields, an XML fault's text, else the whole answer
// on one line, bounded and quoted), never cut to its first line. CML's
// indented JSON made a first line say "{", a polkit refusal's first line is
// its banner, and an IOS refusal's first line is its caret. It refuses both
// shapes a cut took: a first-line helper function, and an error built from
// strings.Split or strings.SplitN's element 0 on a newline.
func TestNoClientCutsTheFarSideToAFirstLine(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range farsideScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") || filepath.Dir(rel) == filepath.Join("pkg", "farside") {
				return
			}
			for _, f := range firstLineCuts(file) {
				t.Errorf("%s:%d %s. Put a remote answer into an error with pkg/farside (Text, Masked, Output), "+
					"which reads its error fields and keeps every line, bounded and quoted", rel, fset.Position(f.pos).Line, f.what)
			}
		})
	}
}

// TestFirstLineCuts_Control proves the scan sees what it looks for, so its
// silence over the tree means something.
func TestFirstLineCuts_Control(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{"a first-line helper", `package p
func firstLine(s string) string { return s }`, 1},
		{"a first-non-empty-line method", `package p
type c struct{}
func (c) FirstNonEmptyLine(s string) string { return s }`, 1},
		{"a split's first element in Errorf", `package p
import ("fmt"; "strings")
func f(out string) error { return fmt.Errorf("refused: %s", strings.Split(out, "\n")[0]) }`, 1},
		{"a SplitN's first element in errors.New", `package p
import ("errors"; "strings")
func f(out string) error { return errors.New(strings.SplitN(out, "\n", 2)[0]) }`, 1},
		{"farside is the allowed shape", `package p
import ("fmt"; "github.com/Subject-Void-LLC/the-pleiades/pkg/farside")
func f(out string) error { return fmt.Errorf("refused: %s", farside.Text(out)) }`, 0},
		{"a split outside an error is parsing", `package p
import "strings"
func f(out string) string { return strings.Split(out, "\n")[0] }`, 0},
		{"a split on another separator", `package p
import ("fmt"; "strings")
func f(out string) error { return fmt.Errorf("%s", strings.Split(out, ",")[0]) }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			if got := len(firstLineCuts(file)); got != tc.want {
				t.Fatalf("findings = %d, want %d", got, tc.want)
			}
		})
	}
}

// cutFinding is one place a file cuts text to a first line.
type cutFinding struct {
	pos  token.Pos
	what string
}

// firstLineCuts returns every first-line helper file declares and every
// error it builds from a newline split's first element.
func firstLineCuts(file *ast.File) []cutFinding {
	var found []cutFinding
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && firstLineHelperName.MatchString(fn.Name.Name) {
			found = append(found, cutFinding{fn.Pos(), "declares the first-line helper " + fn.Name.Name})
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isErrorBuilder(call) {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(m ast.Node) bool {
				if idx, ok := m.(*ast.IndexExpr); ok && isNewlineSplitFirst(idx) {
					found = append(found, cutFinding{idx.Pos(), "builds an error from a newline split's first element"})
				}
				return true
			})
		}
		return true
	})
	return found
}

// isErrorBuilder reports whether call is one of errorBuilders.
func isErrorBuilder(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && errorBuilders[pkg.Name][sel.Sel.Name]
}

// isNewlineSplitFirst reports whether idx is strings.Split(x, "\n")[0] or
// strings.SplitN(x, "\n", n)[0].
func isNewlineSplitFirst(idx *ast.IndexExpr) bool {
	if lit, ok := idx.Index.(*ast.BasicLit); !ok || lit.Value != "0" {
		return false
	}
	call, ok := idx.X.(*ast.CallExpr)
	if !ok || len(call.Args) < 2 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Split" && sel.Sel.Name != "SplitN") {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
		return false
	}
	sep, ok := call.Args[1].(*ast.BasicLit)
	if !ok || sep.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(sep.Value)
	return err == nil && s == "\n"
}
