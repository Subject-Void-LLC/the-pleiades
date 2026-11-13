// Architecture rule for pkg/winrmexec's uncounted entry: only the waits may
// reach a host without going through its circuit breaker.
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

// winrmexecPkg is the import path of the one WinRM client.
const winrmexecPkg = modulePath + "/pkg/winrmexec"

// uncountedWinRMEntries are pkg/winrmexec's names that reach a host outside
// its endpoint circuit: a Waiter is neither counted nor refused, and
// WaitUntilReachable waits through one.
var uncountedWinRMEntries = map[string]bool{
	"NewWaiter":          true,
	"Waiter":             true,
	"WaitUntilReachable": true,
}

// uncountedWinRMCallers are the waits, by package directory and the
// top-level declaration holding the reference: each is a wait for a host
// to come back, whose failures are expected by the dozen.
var uncountedWinRMCallers = map[string]map[string]bool{
	// wait.connection's WinRM tries.
	"internal/catalog/wait": {"connectionProbe": true, "winrmTry": true},
	// exec.winrm.shell's reconnect after an expected disconnect.
	"internal/catalog/exec/winrm": {"handleExpectedDisconnect": true},
	// win.reboot's wait for the new boot time.
	"internal/catalog/win": {"reboot": true, "rebootWaitRun": true},
}

// TestOnlyWaitsUseTheUncountedWinRMEntry keeps the Waiter a wait's alone.
// It is a way round the endpoint's circuit breaker for whatever calls it: a
// Collection that used it for ordinary work would hammer a dead host its
// whole timeout on every task, which is what the breaker exists to stop.
// So outside pkg/winrmexec, no production file may name NewWaiter, Waiter
// or WaitUntilReachable except in a declaration uncountedWinRMCallers
// lists. A new wait is added there, by name, with its reason.
func TestOnlyWaitsUseTheUncountedWinRMEntry(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]bool{}
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") {
				return
			}
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			for _, use := range uncountedWinRMUses(file) {
				if uncountedWinRMCallers[pkgDir][use.decl] {
					seen[pkgDir+"."+use.decl] = true
					continue
				}
				t.Errorf("%s:%d names winrmexec.%s in %s, which is not one of the waits. It reaches the host "+
					"outside its circuit breaker; ordinary work goes through winrmexec.Execute, and a new wait is "+
					"listed in uncountedWinRMCallers with its reason", rel, fset.Position(use.pos).Line, use.entry, use.decl)
			}
		})
	}
	// Every listed wait still uses the entry, so the list cannot outlive the
	// code it allows and quietly allow something else of the same name.
	for dir, decls := range uncountedWinRMCallers {
		for decl := range decls {
			if !seen[dir+"."+decl] {
				t.Errorf("%s.%s is listed as a wait but names no uncounted WinRM entry; take it off the list", dir, decl)
			}
		}
	}
}

// TestUncountedWinRMUses_Control proves the scan sees what it looks for, so
// an empty result above means something. Each snippet is parsed as a real
// file is.
func TestUncountedWinRMUses_Control(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"a call to NewWaiter", `package p
import "` + winrmexecPkg + `"
func work() { _ = winrmexec.NewWaiter(winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Options{}) }`, []string{"work"}},
		{"the type in a signature", `package p
import "` + winrmexecPkg + `"
func (s *svc) use(w *winrmexec.Waiter) {}`, []string{"svc.use"}},
		{"a package-level variable", `package p
import "` + winrmexecPkg + `"
var wait = winrmexec.WaitUntilReachable`, []string{"wait"}},
		{"an aliased import is still seen", `package p
import w "` + winrmexecPkg + `"
func work() { _ = w.NewWaiter }`, []string{"work"}},
		{"the counted entry is not an uncounted one", `package p
import "` + winrmexecPkg + `"
func work() { _, _ = winrmexec.Execute(nil, winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Command{}, winrmexec.Options{}) }`, nil},
		{"another package's Waiter is not this one", `package p
import "example.com/other"
func work() { _ = other.NewWaiter() }`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			var got []string
			for _, use := range uncountedWinRMUses(file) {
				got = append(got, use.decl)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("uses in %v, want %v", got, tc.want)
			}
		})
	}
	// A listed wait passes and the same use anywhere else is reported.
	if !uncountedWinRMCallers["internal/catalog/wait"]["connectionProbe"] || uncountedWinRMCallers["internal/catalog/exec/winrm"]["Shell"] {
		t.Fatal("the allowlist does not read the way the rule assumes")
	}
}

// uncountedWinRMUse is one place a file names an uncounted entry.
type uncountedWinRMUse struct {
	pos   token.Pos
	entry string
	// decl is the top-level declaration holding it: a function's name, a
	// method's "Type.Method", or a package-level variable's name.
	decl string
}

// uncountedWinRMUses returns every place file names one of
// uncountedWinRMEntries through its import of pkg/winrmexec.
func uncountedWinRMUses(file *ast.File) []uncountedWinRMUse {
	name := importName(file, winrmexecPkg, "winrmexec")
	if name == "" {
		return nil
	}
	var uses []uncountedWinRMUse
	collect := func(node ast.Node, decl string) {
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, isIdent := sel.X.(*ast.Ident); isIdent && x.Name == name && uncountedWinRMEntries[sel.Sel.Name] {
				uses = append(uses, uncountedWinRMUse{pos: sel.Pos(), entry: sel.Sel.Name, decl: decl})
			}
			return true
		})
	}
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			collect(decl, funcDeclName(decl))
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if len(s.Names) > 0 {
						collect(s, s.Names[0].Name)
					}
				case *ast.TypeSpec:
					collect(s, s.Name.Name)
				}
			}
		}
	}
	return uses
}

// funcDeclName is a function's name, or "Type.Method" for a method.
func funcDeclName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// importName returns the name file uses for path (its alias, or fallback),
// or the empty string when file does not import it.
func importName(file *ast.File, path, fallback string) string {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return fallback
	}
	return ""
}
