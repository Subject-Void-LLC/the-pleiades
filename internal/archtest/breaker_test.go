// Architecture rules for pkg/breaker: no circuit the platform owns is
// reachable from outside the code that dials, and no second breaker exists.
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

// breakerPkg is the import path of the platform's one circuit breaker.
const breakerPkg = modulePath + "/pkg/breaker"

// breakerTypeName matches a type name that reads as a circuit breaker of
// its own, which is the shape a second, private copy would take.
var breakerTypeName = regexp.MustCompile(`(?i)breaker|circuit`)

// TestNoPlatformCircuitIsReachable keeps pkg/breaker's one promise: it is
// exported because pkg/remoteexec may not import internal/, never so that
// something outside the code that dials can open or close a circuit the
// platform owns.
//
// Phase 72 refused to move the breaker into pkg/ at all for exactly that
// reason, and Phase 75 moved it anyway because WinRM needed the same one.
// What makes that safe is that every Breaker the platform builds is an
// unexported field of the thing that dials (remoteexec.Runner, the WinRM
// transport). This test is what keeps it that way: outside pkg/breaker,
// no production file may hold a Breaker in a package-level variable, in
// an exported struct field, or behind an exported function, method or
// type, and no production file may declare a breaker-shaped type of its
// own. A Collection that builds a Breaker for itself is still allowed,
// because it can only ever open its own circuits.
func TestNoPlatformCircuitIsReachable(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") || filepath.Dir(rel) == filepath.Join("pkg", "breaker") {
				return
			}
			for _, finding := range reachableBreakers(file) {
				t.Errorf("%s:%d %s. Keep every Breaker the platform builds in an unexported field of the "+
					"code that dials, and reuse pkg/breaker rather than writing a second one",
					rel, fset.Position(finding.pos).Line, finding.what)
			}
		})
	}
}

// TestReachableBreakers_Control proves the scan above can see what it is
// looking for, so an empty result from it means something. Each snippet
// is parsed exactly as a real file is.
func TestReachableBreakers_Control(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"an unexported field is the allowed shape", `package p
import "` + breakerPkg + `"
type Runner struct{ breaker *breaker.Breaker }
func New() *Runner { return &Runner{breaker: breaker.New(5, 0)} }`, 0},
		{"a package-level breaker", `package p
import "` + breakerPkg + `"
var shared = breaker.New(5, 0)`, 1},
		{"a package-level variable typed as one", `package p
import "` + breakerPkg + `"
var shared *breaker.Breaker`, 1},
		{"an exported field", `package p
import "` + breakerPkg + `"
type Runner struct{ Breaker *breaker.Breaker }`, 1},
		{"an exported accessor", `package p
import "` + breakerPkg + `"
type Runner struct{ b *breaker.Breaker }
func (r *Runner) Circuit() *breaker.Breaker { return r.b }`, 1},
		{"an exported alias", `package p
import "` + breakerPkg + `"
type Handle = breaker.Breaker`, 1},
		{"an aliased import is still seen", `package p
import cb "` + breakerPkg + `"
var shared = cb.New(5, 0)`, 1},
		{"the sentinel alone is fine", `package p
import "` + breakerPkg + `"
var errOpen = breaker.ErrOpen`, 0},
		{"a second breaker of its own", `package p
type circuitBreaker struct{}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			if got := len(reachableBreakers(file)); got != tc.want {
				t.Fatalf("findings = %d, want %d", got, tc.want)
			}
		})
	}
}

// breakerFinding is one place a file exposes a Breaker or defines its own.
type breakerFinding struct {
	pos  token.Pos
	what string
}

// reachableBreakers returns every place in file that holds a Breaker
// somewhere other than an unexported field, or declares a breaker-shaped
// type of its own.
func reachableBreakers(file *ast.File) []breakerFinding {
	var found []breakerFinding
	name := breakerImportName(file)

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if d.Tok == token.VAR && (mentionsBreaker(s.Type, name) || anyMentionsBreaker(s.Values, name)) {
						found = append(found, breakerFinding{s.Pos(), "holds a Breaker in a package-level variable"})
					}
				case *ast.TypeSpec:
					found = append(found, typeSpecBreakers(s, name)...)
				}
			}
		case *ast.FuncDecl:
			if d.Name.IsExported() && d.Type.Results != nil && mentionsBreaker(d.Type.Results, name) {
				found = append(found, breakerFinding{d.Pos(), "returns a Breaker from exported " + d.Name.Name})
			}
		}
	}
	return found
}

// typeSpecBreakers checks one type declaration: a struct may hold a
// Breaker only in an unexported field, any other exported type may not
// name one at all, and no type may be a breaker of its own.
func typeSpecBreakers(s *ast.TypeSpec, name string) []breakerFinding {
	var found []breakerFinding
	if breakerTypeName.MatchString(s.Name.Name) {
		found = append(found, breakerFinding{s.Pos(), "declares its own breaker type " + s.Name.Name})
	}
	st, isStruct := s.Type.(*ast.StructType)
	if !isStruct {
		if s.Name.IsExported() && mentionsBreaker(s.Type, name) {
			found = append(found, breakerFinding{s.Pos(), "exposes a Breaker through exported type " + s.Name.Name})
		}
		return found
	}
	for _, field := range st.Fields.List {
		if !mentionsBreaker(field.Type, name) {
			continue
		}
		// An embedded field is promoted, so it counts as exported.
		exported := len(field.Names) == 0
		for _, n := range field.Names {
			exported = exported || n.IsExported()
		}
		if exported {
			found = append(found, breakerFinding{field.Pos(), "holds a Breaker in an exported field of " + s.Name.Name})
		}
	}
	return found
}

// breakerImportName returns the name file uses for pkg/breaker, or the
// empty string when file does not import it.
func breakerImportName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != breakerPkg {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "breaker"
	}
	return ""
}

// anyMentionsBreaker reports whether any of nodes mentions a Breaker.
func anyMentionsBreaker(nodes []ast.Expr, name string) bool {
	for _, n := range nodes {
		if mentionsBreaker(n, name) {
			return true
		}
	}
	return false
}

// mentionsBreaker reports whether node names pkg/breaker's Breaker type
// or its constructor. The sentinel ErrOpen is deliberately not a
// mention: it carries no state.
func mentionsBreaker(node ast.Node, name string) bool {
	if node == nil || name == "" {
		return false
	}
	seen := false
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return !seen
		}
		if x, isIdent := sel.X.(*ast.Ident); isIdent && x.Name == name &&
			(sel.Sel.Name == "Breaker" || sel.Sel.Name == "New") {
			seen = true
		}
		return !seen
	})
	return seen
}
