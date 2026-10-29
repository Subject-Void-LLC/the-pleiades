// Phase 131's R3 and R5 as rules a machine holds every built-in method to.
//
// R3, results are readable: whatever a method records, facts included, is
// a stat a register reads, so every name a method records must be one its
// Doc.Returns declares. An undeclared stat is invisible to `pleiades doc`,
// to an agent deciding what to register, and to the run journal, which
// keeps only declared names (journal_registry_test.go).
//
// R5, every Returns entry says when it is returned: "always", "when the
// device reports it", "when config is gathered". A blank condition leaves a
// reader to guess whether a missing key means "absent" or "not asked".
package archtest

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// engineStats are the names pkg/sdk's own writers record and the engine
// reads (a diff, an undo, a check's prediction mark), which no method
// declares because every method may record them.
var engineStats = []string{sdk.StatDiff, sdk.StatInverse, sdk.StatPredicted}

// funcPackage is the import path of the package that defines fn, read from
// its runtime name ("<path>.invoke", "<path>.init.func1", "<path>.(*T).M").
func funcPackage(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.IsNil() {
		return ""
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return ""
	}
	name := f.Name()
	slash := strings.LastIndex(name, "/")
	dot := strings.Index(name[slash+1:], ".")
	if dot < 0 {
		return name
	}
	return name[:slash+1+dot]
}

// funcFile is the base name of the file that defines fn, or "".
func funcFile(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.IsNil() {
		return ""
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return ""
	}
	file, _ := f.FileLine(v.Pointer())
	return filepath.Base(file)
}

// packageDecl is what one Collection package's methods declare.
type packageDecl struct {
	// methods are the methods whose Invoke or Check the package defines.
	methods []string
	// names are the Returns names any of them declares.
	names map[string]bool
	// byFile maps a file holding a method's Invoke or Check to the Returns
	// names the methods defined there declare.
	byFile map[string]map[string]bool
	// fileMethods maps the same files to those methods.
	fileMethods map[string][]string
}

// declaredByPackage maps each Collection package to what its methods
// declare, whole and file by file.
func declaredByPackage(t *testing.T) map[string]*packageDecl {
	t.Helper()
	out := map[string]*packageDecl{}
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		for _, fn := range []collection.Method{desc.Invoke, desc.Check} {
			pkg := funcPackage(fn)
			if pkg == "" {
				continue
			}
			d := out[pkg]
			if d == nil {
				d = &packageDecl{names: map[string]bool{}, byFile: map[string]map[string]bool{}, fileMethods: map[string][]string{}}
				out[pkg] = d
			}
			if !slices.Contains(d.methods, cfg.Name) {
				d.methods = append(d.methods, cfg.Name)
			}
			file := funcFile(fn)
			if d.byFile[file] == nil {
				d.byFile[file] = map[string]bool{}
			}
			if !slices.Contains(d.fileMethods[file], cfg.Name) {
				d.fileMethods[file] = append(d.fileMethods[file], cfg.Name)
			}
			for _, r := range desc.Manifest.Doc.Returns {
				d.names[r.Name] = true
				d.byFile[file][r.Name] = true
			}
		}
	}
	return out
}

// TestEveryRecordedKeyIsDeclared holds R3: each stat or fact name a
// Collection package's code records is declared in Doc.Returns by a method
// that package implements.
//
// The unit is the file where it can be and the package where it cannot. A
// key recorded in the file that holds a method's Invoke or Check must be
// declared by a method defined in that file; a key recorded in a shared
// helper file belongs to whichever of the package's methods call it, and
// the scan reads source rather than a call graph, so there any of the
// package's methods declaring it is enough. The finer per-call half is each
// package's own tests.
func TestEveryRecordedKeyIsDeclared(t *testing.T) {
	root := repoRoot(t)
	scope := newConstScope(t, root, modulePath)
	packages := declaredByPackage(t)
	if len(packages) == 0 {
		t.Fatal("no implemented method resolved to a package, so this test proved nothing")
	}

	var scanned, keys, dynamic int
	for pkg, decl := range packages {
		if !strings.HasPrefix(pkg, modulePath+"/internal/catalog/") {
			t.Logf("%v: Invoke or Check lives in %s, outside the catalog, and is not scanned", decl.methods, pkg)
			continue
		}
		dir := filepath.Join(root, strings.TrimPrefix(pkg, modulePath+"/"))
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: methods %v resolve to a package with no directory", pkg, decl.methods)
			continue
		}
		scan := scope.scanPackage(dir)
		scanned++
		dynamic += len(scan.dynamicAt)
		for _, at := range scan.dynamicAt {
			t.Logf("key named at run time: %s", strings.TrimPrefix(at, root+string(filepath.Separator)))
		}
		var missing []string
		for key, files := range scan.keys {
			keys++
			if slices.Contains(engineStats, key) {
				continue
			}
			for file := range files {
				owners, isMethodFile := decl.fileMethods[file]
				switch {
				case isMethodFile && !decl.byFile[file][key]:
					sort.Strings(owners)
					missing = append(missing, key+" in "+file+", which "+strings.Join(owners, ", ")+" (defined there) does not declare")
				case !isMethodFile && !decl.names[key]:
					missing = append(missing, key+" in "+file+", which no method of the package declares")
				}
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			t.Errorf("%s records %s in Doc.Returns", strings.TrimPrefix(pkg, modulePath+"/"), m)
		}
	}
	if keys == 0 {
		t.Fatal("the scan found no recorded key at all, so it is not reading the code")
	}

	// A package that records keys but defines no method's Invoke or Check
	// would pass above by never being looked at, so every catalog
	// directory that records anything must be one the registry reached.
	catalogRoot := filepath.Join(root, "internal", "catalog")
	err := filepath.WalkDir(catalogRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		pkg := modulePath + "/" + filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if _, ok := packages[pkg]; ok {
			return nil
		}
		if scan := scope.scanPackage(path); len(scan.keys) > 0 {
			t.Errorf("%s records %d key(s) but no registered method's Invoke or Check lives there, so none of them is held to a declaration", strings.TrimPrefix(pkg, modulePath+"/"), len(scan.keys))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", catalogRoot, err)
	}
	t.Logf("%d package(s), %d statically named key(s), %d record call(s) with a key named at run time", scanned, keys, dynamic)
}

// TestEveryReturnSaysWhenItIsReturned holds R5's mechanical half: no
// built-in method's Returns entry leaves its Returned condition blank.
func TestEveryReturnSaysWhenItIsReturned(t *testing.T) {
	var checked int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		for _, r := range desc.Manifest.Doc.Returns {
			checked++
			if strings.TrimSpace(r.Returned) == "" {
				t.Errorf("%s: Returns entry %q does not say when it is returned", cfg.Name, r.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Returns entry was examined, so this test proved nothing")
	}
}
