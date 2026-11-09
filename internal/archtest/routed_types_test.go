// Package archtest: the type-checked half of the routed-value rule. A package
// chi routes is read with go/types, so a read of a request's path is found by
// what its receiver is (*url.URL, *http.Request, *chi.Context), however the
// value got there: r.URL.Path, u := r.URL; u.Path, or a helper taking a
// *url.URL.
package archtest

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// routedRoots are the module directories whose handlers chi routes. Every
// package at or under one of them is routed, whether or not it imports chi:
// chi v5.4.0's routeHTTP copies each value it recorded, still escaped, into
// r.SetPathValue, so r.PathValue reads escaped text in any package a chi
// router hands a request to.
var routedRoots = []string{
	filepath.Join("internal", "api"),
	filepath.Join("internal", "ui"),
	filepath.Join("cmd", "controller"),
}

// exportPackage is the part of `go list -export -json` a type check needs.
type exportPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Error      *struct{ Err string }
}

// exportIndex is every package the routed ones depend on, by import path,
// with the compiled export data go/importer reads.
type exportIndex struct {
	byPath map[string]exportPackage
	order  []string
}

// loadExports runs `go list -export -deps` over the routed roots once per
// test binary. -export builds what is not in the build cache already, which
// is the same compilation `go vet` of these packages needs.
var loadExports = sync.OnceValues(func() (*exportIndex, error) {
	args := []string{"list", "-e", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Error"}
	for _, root := range routedRoots {
		args = append(args, "./"+filepath.ToSlash(root)+"/...")
	}
	// The negative control type-checks snippets importing these.
	args = append(args, "net/url", "net/http", chiImportPath)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRootPath()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, &listError{err: err, stderr: stderr.String()}
	}
	idx := &exportIndex{byPath: map[string]exportPackage{}}
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p exportPackage
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		idx.byPath[p.ImportPath] = p
		idx.order = append(idx.order, p.ImportPath)
	}
	return idx, nil
})

// listError is a failed `go list`, with what it printed.
type listError struct {
	err    error
	stderr string
}

// Error implements error.
func (e *listError) Error() string { return "go list -export: " + e.err.Error() + "\n" + e.stderr }

// repoRootPath is repoRoot without a testing.T, for the once-only loader.
func repoRootPath() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// typesImporter is a go/types importer over idx's export data.
func (idx *exportIndex) typesImporter(fset *token.FileSet) types.Importer {
	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		p, ok := idx.byPath[path]
		if !ok || p.Export == "" {
			return nil, &missingExport{path: path}
		}
		return os.Open(p.Export) // #nosec G304 -- an export file go list named
	})
}

// missingExport is an import idx holds no export data for.
type missingExport struct{ path string }

// Error implements error.
func (e *missingExport) Error() string { return "no export data for " + e.path }

// checkFiles type-checks files as package path and returns what go/types
// recorded about them.
func (idx *exportIndex) checkFiles(fset *token.FileSet, path string, files []*ast.File) (*types.Info, error) {
	info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}}
	conf := types.Config{Importer: idx.typesImporter(fset)}
	if _, err := conf.Check(path, fset, files, info); err != nil {
		return nil, err
	}
	return info, nil
}

// typedRead is one read of a request's path found by type, and the
// function it sits in.
type typedRead struct {
	pos      token.Pos
	fn       string
	spelling string
}

// typedReads returns every read in file of a request's path or of a value chi
// recorded, by the type of what is read: a *url.URL's Path or RawPath field
// or EscapedPath or RequestURI method, an *http.Request's RequestURI field or
// PathValue method, and a *chi.Context's RoutePath field. Each is named with
// the function it is in, for the allowlist.
func typedReads(file *ast.File, info *types.Info) []typedRead {
	var reads []typedRead
	visit := func(fn string, root ast.Node) {
		ast.Inspect(root, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			selection := info.Selections[sel]
			if selection == nil {
				return true
			}
			if spelling := readSpelling(selection.Recv(), sel.Sel.Name); spelling != "" {
				reads = append(reads, typedRead{pos: sel.Pos(), fn: fn, spelling: spelling})
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			visit(funcName(fd), fd)
			continue
		}
		visit("", decl)
	}
	return reads
}

// readSpelling names a selection of member on a value of type recv when it
// is one typedReads holds, and is "" otherwise.
func readSpelling(recv types.Type, member string) string {
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	switch pkg, name := named.Obj().Pkg().Path(), named.Obj().Name(); {
	case pkg == "net/url" && name == "URL":
		switch member {
		case "Path", "RawPath", "EscapedPath", "RequestURI":
			return "URL." + member
		}
	case pkg == "net/http" && name == "Request":
		switch member {
		case "RequestURI", "PathValue":
			return "Request." + member
		}
	case pkg == chiImportPath && name == "Context":
		if member == "RoutePath" {
			return "chi.Context.RoutePath"
		}
	}
	return ""
}

// funcName is a function declaration's name as the allowlist writes it:
// Recv.Name for a method, Name otherwise.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// isRouted reports whether rel, a directory relative to the module root, is
// at or under a routed root.
func isRouted(rel string) bool {
	for _, root := range routedRoots {
		if rel == root || strings.HasPrefix(rel, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// routedTypedReads loads and type-checks every routed package's non-test
// files and returns each typed read with its file, relative to root. A
// routed package that does not load fails the test, since skipping it would
// leave a hole in the rule.
func routedTypedReads(t *testing.T, root string) (map[string][]typedRead, map[string]*token.FileSet) {
	t.Helper()
	idx, err := loadExports()
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string][]typedRead{}
	fsets := map[string]*token.FileSet{}
	checked := 0
	for _, path := range idx.order {
		p := idx.byPath[path]
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil || strings.HasPrefix(rel, "..") || !isRouted(rel) {
			continue
		}
		if p.Error != nil {
			t.Fatalf("routed package %s does not load: %s", path, p.Error.Err)
		}
		fset := token.NewFileSet()
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		info, err := idx.checkFiles(fset, path, files)
		if err != nil {
			t.Fatalf("type-checking %s: %v", path, err)
		}
		checked++
		for i, f := range files {
			fileRel, _ := filepath.Rel(root, filepath.Join(p.Dir, p.GoFiles[i]))
			reads[fileRel] = append(reads[fileRel], typedReads(f, info)...)
			fsets[fileRel] = fset
		}
	}
	if checked < 10 {
		t.Fatalf("type-checked %d routed packages, want internal/api, internal/ui/... and cmd/controller: the loader is not reading the tree it guards", checked)
	}
	return reads, fsets
}
