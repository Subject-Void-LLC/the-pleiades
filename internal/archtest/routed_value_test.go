// Package archtest: a value chi routed is read only through api.PathParam,
// in internal/api/pathparam.go, and a package chi routes never reads the
// decoded request path.
//
// chi v5 records each URL parameter still escaped whenever a request has a
// RawPath, so a handler reading chi.URLParam acted on the literal "sw%2F1"
// for a request naming the device "sw/1" (FAILURE_PATTERNS 1013), and
// r.URL.Path, decoded, has one segment more for every escaped slash: a
// guard that split it would judge a different record from the handler.
// This test is written against the rule, not the incident's one spelling
// (LESSONS 114, FAILURE_PATTERNS 1017): every way to reach chi's recorded
// values is listed and held to its own row in the control below.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// chiImportPath is the router whose recorded values this test guards.
const chiImportPath = "github.com/go-chi/chi/v5"

// pathParamFile is the one file allowed to read what chi recorded.
var pathParamFile = filepath.Join("internal", "api", "pathparam.go")

// chiValueNames are the names through which chi's recorded values are
// reached: the two package functions, the method on *chi.Context, the
// field holding the values, and the context key the route context hangs
// on. Matched as a selector under any receiver, so an aliased import, a
// method call on a route context and a method value are all caught.
var chiValueNames = map[string]bool{
	"URLParam":        true,
	"URLParamFromCtx": true,
	"URLParams":       true,
	"RouteCtxKey":     true,
}

// routedRead is one place a file reads what the router recorded, and why
// it is refused.
type routedRead struct {
	pos    token.Pos
	reason string
}

// importsChi reports whether file imports chi, and under which name ("" for
// the default name).
func importsChi(file *ast.File) (bool, string) {
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil || p != chiImportPath {
			continue
		}
		if spec.Name != nil {
			return true, spec.Name.Name
		}
		return true, ""
	}
	return false, ""
}

// routedValueReads returns where file reads a routed value or the request
// path other than through pathparam.go. chiRouted says whether file's
// package serves chi routes (some file of it imports chi), which is when a
// request's PathValue, URL.Path and URL.RawPath are chi's as well.
func routedValueReads(file *ast.File, chiRouted bool) []routedRead {
	var reads []routedRead
	if ok, name := importsChi(file); ok && name == "." {
		reads = append(reads, routedRead{file.Package, "dot-imports chi, which hides which of its names are read"})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		switch {
		case chiValueNames[name]:
			reads = append(reads, routedRead{sel.Pos(), "reads chi's recorded value (" + name + ") rather than api.PathParam"})
		case chiRouted && name == "PathValue":
			reads = append(reads, routedRead{sel.Pos(), "reads PathValue, which chi sets still escaped, rather than api.PathParam"})
		case chiRouted && (name == "Path" || name == "RawPath") && isURLField(sel.X):
			reads = append(reads, routedRead{sel.Pos(), "reads URL." + name + "; read the path as sent (URL.EscapedPath) or a value through api.PathParam"})
		}
		return true
	})
	return reads
}

// isURLField reports whether x is a selector ending in .URL, the shape of
// r.URL in r.URL.Path.
func isURLField(x ast.Expr) bool {
	sel, ok := x.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "URL"
}

// TestOnlyPathParamReadsARoutedValue walks every non-test Go file and
// refuses each routedValueReads finding outside pathparam.go.
func TestOnlyPathParamReadsARoutedValue(t *testing.T) {
	root := repoRoot(t)

	type parsed struct {
		rel  string
		file *ast.File
		fset *token.FileSet
	}
	byDir := map[string][]parsed{}
	routed := map[string]bool{}
	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			rel, _ := filepath.Rel(root, path)
			d := filepath.Dir(rel)
			byDir[d] = append(byDir[d], parsed{rel: rel, file: file, fset: fset})
			if ok, _ := importsChi(file); ok {
				routed[d] = true
			}
		})
	}

	if !routed[filepath.Join("internal", "api")] || !routed[filepath.Join("internal", "ui", "web")] {
		t.Fatalf("found chi in none of internal/api and internal/ui/web (routed = %v); this walk is not reading the tree it guards", routed)
	}
	for d, files := range byDir {
		for _, f := range files {
			if f.rel == pathParamFile {
				continue
			}
			for _, read := range routedValueReads(f.file, routed[d]) {
				t.Errorf("%s: %s; a value chi routed is read only through api.PathParam (internal/api/pathparam.go)", f.fset.Position(read.pos), read.reason)
			}
		}
	}
}

// TestRoutedValueReadsFindsEveryWay is the check's negative control: each
// spelling is found, the reads chi routing does not touch are not, and the
// path reads are refused only in a package chi routes.
func TestRoutedValueReadsFindsEveryWay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		chiRouted bool
		want      int
	}{
		{"package function", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = chi.URLParam(r, "id") }`, false, 1},
		{"aliased import", `package x
import router "github.com/go-chi/chi/v5"
func f(r R) { _ = router.URLParam(r, "id") }`, false, 1},
		{"from a context", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = chi.URLParamFromCtx(r.Context(), "id") }`, false, 1},
		{"method on a route context", `package x
func f(rctx C) { _ = rctx.URLParam("id") }`, false, 1},
		{"method value", `package x
import "github.com/go-chi/chi/v5"
var read = chi.URLParam`, false, 1},
		{"the values field", `package x
func f(rctx C) { _ = rctx.URLParams.Values[0] }`, false, 1},
		{"the context key", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = r.Context().Value(chi.RouteCtxKey) }`, false, 1},
		{"dot import", `package x
import . "github.com/go-chi/chi/v5"
func f(r R) { _ = URLParam(r, "id") }`, false, 1},
		{"PathValue where chi routes", `package x
func f(r R) { _ = r.PathValue("id") }`, true, 1},
		{"PathValue where chi does not route", `package x
func f(r R) { _ = r.PathValue("id") }`, false, 0},
		{"decoded path where chi routes", `package x
func f(r R) { _ = r.URL.Path }`, true, 1},
		{"raw path where chi routes", `package x
func f(r R) { _ = r.URL.RawPath }`, true, 1},
		{"decoded path where chi does not route", `package x
func f(r R) { _ = r.URL.Path }`, false, 0},
		{"what chi routing does not touch", `package x
import "github.com/go-chi/chi/v5"
func f(r R, u U) {
	_ = chi.RouteContext(r.Context()).RoutePattern()
	_ = chi.NewRouter()
	_ = r.URL.EscapedPath()
	_ = r.URL.Query()
	_ = u.Path
}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "x.go", tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(routedValueReads(file, tc.chiRouted)); got != tc.want {
				t.Fatalf("found %d reads, want %d", got, tc.want)
			}
		})
	}
}
