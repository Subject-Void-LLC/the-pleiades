// A Collection method writes a file where The Pleiades runs only through
// sdk.ProjectFileWriter, never with the os package, so a path a runbook
// names can never reach a file outside the project.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// localWriteCalls are the os functions that create, replace, move or delete
// a file or directory by a path. os.CreateTemp is not among them: it names
// its own file, in the system's temporary directory.
var localWriteCalls = map[string]bool{
	"WriteFile": true, "Create": true, "OpenFile": true, "Rename": true, "Remove": true, "RemoveAll": true,
	"Mkdir": true, "MkdirAll": true, "Symlink": true, "Link": true, "Chmod": true, "Chown": true, "Truncate": true,
}

// localWriteAllowed names each function, as "path:function", that calls
// one of localWriteCalls on a path no runbook chooses, with why.
var localWriteAllowed = map[string]string{
	"internal/catalog/container/image/load.go:spool":      "removes the temporary file os.CreateTemp made for the archive, a name it chose",
	"internal/catalog/container/image/load.go:closeSpool": "removes that same temporary file once the engine has read it",
}

// TestCatalogWritesLocalFilesOnlyThroughTheProjectWriter holds every
// Collection method under internal/catalog to writing where The Pleiades
// runs only through sdk.ProjectFileWriter, which refuses an absolute path,
// one out of the project, one through a symbolic link and .pleiades, and is
// absent on a Runner. virt.vbox.vm.screenshot wrote its dest with
// os.WriteFile wherever it named, so on a Runner a runbook could replace any
// file the Runner's account can write (FAILURE_PATTERNS 1237).
func TestCatalogWritesLocalFilesOnlyThroughTheProjectWriter(t *testing.T) {
	root := repoRoot(t)
	used := map[string]bool{}
	var scanned int
	walkGoFiles(t, filepath.Join(root, "internal", "catalog"), func(path string, file *ast.File, fset *token.FileSet) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		scanned++
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("resolving %s: %v", path, err)
		}
		rel = filepath.ToSlash(rel)
		for _, hit := range localWrites(file, fset) {
			key := rel + ":" + hit.fn
			if _, ok := localWriteAllowed[key]; ok {
				used[key] = true
				continue
			}
			t.Errorf("%s:%d: %s calls os.%s; a Collection method writes where The Pleiades runs only through sdk.ProjectFileWriter "+
				"(as virt.cml.capture.stop and virt.vbox.vm.screenshot do), or names the function in localWriteAllowed with why no runbook chooses the path",
				rel, hit.line, hit.fn, hit.call)
		}
	})
	for key := range localWriteAllowed {
		if !used[key] {
			t.Errorf("localWriteAllowed names %s, which no longer writes a local file: remove it", key)
		}
	}
	if scanned == 0 {
		t.Fatal("no Go file under internal/catalog was scanned, so this test proved nothing")
	}
}

// TestCatalogLocalWrites_Control: the scan finds the call the old
// virt.vbox.vm.screenshot made, under any name the os package is imported
// as, and passes os.CreateTemp.
func TestCatalogLocalWrites_Control(t *testing.T) {
	src := `package vm
import (
	"os"
	osx "os"
)
func writeLocal(path string, data []byte) error {
	part := path + ".pleiades-part"
	if err := os.WriteFile(part, data, 0o600); err != nil {
		return err
	}
	return osx.Rename(part, path)
}
func spool() { _, _ = os.CreateTemp("", "x-*") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "screenshot.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	hits := localWrites(file, fset)
	if len(hits) != 2 || hits[0].call != "WriteFile" || hits[1].call != "Rename" || hits[0].fn != "writeLocal" {
		t.Fatalf("found %+v, want os.WriteFile and os.Rename in writeLocal", hits)
	}
}

// localWrite is one call of localWriteCalls: the function it is in, the os
// function, and its line.
type localWrite struct {
	fn, call string
	line     int
}

// localWrites returns every call of localWriteCalls in file, through any
// name the os package is imported as.
func localWrites(file *ast.File, fset *token.FileSet) []localWrite {
	names := map[string]bool{}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "os" {
			continue
		}
		name := "os"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = true
	}
	if len(names) == 0 {
		return nil
	}
	var hits []localWrite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && names[pkg.Name] && localWriteCalls[sel.Sel.Name] {
				hits = append(hits, localWrite{fn: fn.Name.Name, call: sel.Sel.Name, line: fset.Position(call.Pos()).Line})
			}
			return true
		})
	}
	return hits
}
