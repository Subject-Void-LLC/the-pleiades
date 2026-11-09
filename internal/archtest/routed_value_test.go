// Package archtest: a value chi routed is read only through api.PathParam
// (internal/api/pathparam.go), and a package chi routes reads a request's
// path only at the sites allowedReads names, each with its reason.
//
// chi v5 records each URL parameter still escaped whenever a request has a
// RawPath, so a handler reading chi.URLParam acted on the literal "sw%2F1"
// for a request naming the device "sw/1" (FAILURE_PATTERNS 1013), and the
// decoded r.URL.Path has one segment more for every escaped slash, so a
// guard that split it would judge a different record from the handler.
//
// What is held, and how:
//
//   - In every non-test Go file of the module, by syntax: a selector named
//     URLParam, URLParamFromCtx, URLParams or RouteCtxKey, under any receiver
//     or import name, and a dot import of chi. Only pathparam.go may.
//   - In every package under internal/api, internal/ui and cmd/controller
//     (routedRoots), by type (go/types over compiled export data): a
//     *url.URL's Path or RawPath field or its EscapedPath or RequestURI
//     method, an *http.Request's RequestURI field or PathValue method, and
//     a *chi.Context's RoutePath field, however the value reached the
//     selector (r.URL.Path, u := r.URL; u.Path, a helper taking a
//     *url.URL). Each must be a site allowedReads names.
//
// What is not held: reflection, a URL turned into text first (String,
// Redacted, fmt), and the path of a request a package outside routedRoots
// serves. This test is written against the rule, not the incident's one
// spelling (LESSONS 114, FAILURE_PATTERNS 1017), and each spelling has its
// own row in the controls below.
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

// pathParamFile is the one file allowed to read what chi recorded by name.
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

// allowedRead is one site a routed package may read a request's path at:
// the file, the function (Recv.Name for a method), what it reads, and why
// that read judges no record other than the one the handler acts on.
type allowedRead struct {
	file, fn, spelling, reason string
}

// allowedReads are those sites. A read anywhere else in a routed package
// fails the test, and an entry no read matches fails it too, so the list is
// what the code does.
var allowedReads = []allowedRead{
	{"internal/api/pathparam.go", "routedValue", "URL.RawPath",
		"the accessor itself: decodes a value only when chi read it from a RawPath"},
	{"internal/api/pathparam.go", "routedPath", "URL.RawPath",
		"the path chi matched, for the 405 handler's route lookup"},
	{"internal/api/pathparam.go", "routedPath", "URL.Path",
		"the path chi matched when the request has no RawPath"},
	{"internal/api/middleware.go", "requestPath", "URL.EscapedPath",
		"the request log and trace record the path as sent; nothing is looked up"},
	{"internal/ui/web/dotsegments.go", "Handler.refuseDotSegments", "URL.EscapedPath",
		"refuses a dot or empty segment before routing, judging each segment as chi splits it"},
	{"internal/ui/web/preferences.go", "Handler.currentURL", "URL.EscapedPath",
		"the return address an appearance form carries; constrain checks it on the way back"},
	{"internal/ui/web/preferences.go", "Handler.constrain", "URL.EscapedPath",
		"reduces a caller's return address to a path under the UI prefix, its checks reading it decoded"},
	{"internal/ui/static/static.go", "Handler", "URL.Path",
		"an embedded asset's name after StripPrefix; a map lookup, before sign-in, of no record"},
	{"cmd/controller/startuphandler.go", "startupHandler.ServeHTTP", "URL.Path",
		"answers the health probes before the router exists; there is no record to judge"},
}

// routedRead is one place a file reads what the router recorded by name,
// and why it is refused.
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

// routedValueReads returns where file reads chi's recorded values by name:
// a selector chiValueNames holds, or a dot import of chi.
func routedValueReads(file *ast.File) []routedRead {
	var reads []routedRead
	if ok, name := importsChi(file); ok && name == "." {
		reads = append(reads, routedRead{file.Package, "dot-imports chi, which hides which of its names are read"})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && chiValueNames[sel.Sel.Name] {
			reads = append(reads, routedRead{sel.Pos(), "reads chi's recorded value (" + sel.Sel.Name + ") rather than api.PathParam"})
		}
		return true
	})
	return reads
}

// TestOnlyPathParamReadsARoutedValue holds both halves of the rule the
// package comment states.
func TestOnlyPathParamReadsARoutedValue(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, _ := filepath.Rel(root, path)
			if strings.HasSuffix(path, "_test.go") || rel == pathParamFile {
				return
			}
			for _, read := range routedValueReads(file) {
				t.Errorf("%s: %s; a value chi routed is read only through api.PathParam (internal/api/pathparam.go)", fset.Position(read.pos), read.reason)
			}
		})
	}

	used := make([]bool, len(allowedReads))
	reads, fsets := routedTypedReads(t, root)
	for file, list := range reads {
		for _, read := range list {
			i := allowedIndex(file, read)
			if i < 0 {
				t.Errorf("%s: %s reads %s, which a package chi routes reads only at a site allowedReads names: read a value through api.PathParam, or add the site there with why it judges the record the handler acts on", fsets[file].Position(read.pos), read.fn, read.spelling)
				continue
			}
			used[i] = true
		}
	}
	for i, a := range allowedReads {
		if !used[i] {
			t.Errorf("allowedReads names %s %s %s, which nothing reads any more: remove the entry", a.file, a.fn, a.spelling)
		}
	}
}

// allowedIndex is the allowedReads entry read in file matches, or -1.
func allowedIndex(file string, read typedRead) int {
	for i, a := range allowedReads {
		if filepath.FromSlash(a.file) == file && a.fn == read.fn && a.spelling == read.spelling {
			return i
		}
	}
	return -1
}

// TestRoutedValueReadsFindsEveryWay is the syntax half's negative control:
// each spelling is found, and the names chi routing does not touch are not.
func TestRoutedValueReadsFindsEveryWay(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"package function", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = chi.URLParam(r, "id") }`, 1},
		{"aliased import", `package x
import router "github.com/go-chi/chi/v5"
func f(r R) { _ = router.URLParam(r, "id") }`, 1},
		{"from a context", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = chi.URLParamFromCtx(r.Context(), "id") }`, 1},
		{"method on a route context", `package x
func f(rctx C) { _ = rctx.URLParam("id") }`, 1},
		{"method value", `package x
import "github.com/go-chi/chi/v5"
var read = chi.URLParam`, 1},
		{"the values field", `package x
func f(rctx C) { _ = rctx.URLParams.Values[0] }`, 1},
		{"the context key", `package x
import "github.com/go-chi/chi/v5"
func f(r R) { _ = r.Context().Value(chi.RouteCtxKey) }`, 1},
		{"dot import", `package x
import . "github.com/go-chi/chi/v5"
func f(r R) { _ = URLParam(r, "id") }`, 1},
		{"what chi routing does not touch", `package x
import "github.com/go-chi/chi/v5"
func f(r R) {
	_ = chi.RouteContext(r.Context()).RoutePattern()
	_ = chi.NewRouter()
}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "x.go", tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(routedValueReads(file)); got != tc.want {
				t.Fatalf("found %d reads, want %d", got, tc.want)
			}
		})
	}
}

// TestTypedReadsFindsEveryWay is the type half's negative control: each
// spelling is found by what its receiver is, wherever the value came from,
// and a field or method of the same name on another type is not.
func TestTypedReadsFindsEveryWay(t *testing.T) {
	idx, err := loadExports()
	if err != nil {
		t.Fatal(err)
	}
	const header = `package x
import (
	"net/http"
	"net/url"
	"strings"
	"github.com/go-chi/chi/v5"
)
var _, _, _, _ = strings.Split, chi.NewRouter, url.Parse, http.MethodGet
`
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"the request's decoded path", `func f(r *http.Request) { _ = r.URL.Path }`, []string{"URL.Path"}},
		{"through a local", `func f(r *http.Request) { u := r.URL; _ = u.RawPath }`, []string{"URL.RawPath"}},
		{"in a helper taking a *url.URL", `func f(u *url.URL) string { return u.Path }`, []string{"URL.Path"}},
		{"a url.URL value", `func f(u url.URL) string { return u.Path }`, []string{"URL.Path"}},
		{"splitting the escaped path", `func f(r *http.Request) { _ = strings.Split(r.URL.EscapedPath(), "/") }`, []string{"URL.EscapedPath"}},
		{"the URL's request URI", `func f(r *http.Request) { _ = r.URL.RequestURI() }`, []string{"URL.RequestURI"}},
		{"the request's request URI", `func f(r *http.Request) { _ = r.RequestURI }`, []string{"Request.RequestURI"}},
		{"PathValue, which chi sets still escaped", `func f(r *http.Request) { _ = r.PathValue("id") }`, []string{"Request.PathValue"}},
		{"chi's route path", `func f(r *http.Request) { _ = chi.RouteContext(r.Context()).RoutePath }`, []string{"chi.Context.RoutePath"}},
		{"a method value of one", `func f(r *http.Request) { _ = r.URL.EscapedPath }`, []string{"URL.EscapedPath"}},
		{"another type's Path", `type file struct{ Path string }
func f(x file) string { return x.Path }`, nil},
		{"the URL's query", `func f(r *http.Request) { _ = r.URL.Query(); _ = r.URL.Host }`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "x.go", header+tc.body, 0)
			if err != nil {
				t.Fatal(err)
			}
			info, err := idx.checkFiles(fset, "x", []*ast.File{file})
			if err != nil {
				t.Fatalf("type-checking the control: %v", err)
			}
			var got []string
			for _, r := range typedReads(file, info) {
				got = append(got, r.spelling)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("found %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsRoutedCoversEachRoot pins the routed set: each root and what is
// under it, and nothing beside it.
func TestIsRoutedCoversEachRoot(t *testing.T) {
	for dir, want := range map[string]bool{
		filepath.Join("internal", "api"):                        true,
		filepath.Join("internal", "api", "wellknown"):           true,
		filepath.Join("internal", "ui", "static"):               true,
		filepath.Join("internal", "ui", "resources", "devices"): true,
		filepath.Join("cmd", "controller"):                      true,
		filepath.Join("internal", "apispec"):                    false,
		filepath.Join("internal", "uifake"):                     false,
		filepath.Join("cmd", "pleiades"):                        false,
	} {
		if got := isRouted(dir); got != want {
			t.Errorf("isRouted(%s) = %v, want %v", dir, got, want)
		}
	}
}
