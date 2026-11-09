// Tests that no command splits its arguments with a hand-kept list of its
// boolean flags. The splitter reads which flags are boolean from the
// command's own FlagSet, so a list kept beside the FlagSet is a second
// copy that nothing holds equal to the first: a bool flag missing from it
// makes the token after that flag be swallowed as its value.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestNoCommandPassesAHandKeptBoolFlagList reads every non-test Go file in
// this package and fails on a call that passes a command's arguments (args,
// or a slice of it) together with a map[string]bool: a composite literal
// with entries, or a variable holding one. That is the shape every call to
// the splitter had while each command named its own boolean flags.
func TestNoCommandPassesAHandKeptBoolFlagList(t *testing.T) {
	fset, parsed := parsePackage(t)
	// Every name declared anywhere in the package as a non-empty
	// map[string]bool literal, a package-level var or a local one, since a
	// list declared in one file was passed from others (run.go's, which
	// rollback and adhoc shared).
	lists := map[string]bool{}
	for _, file := range parsed {
		for name := range boolListNames(file) {
			lists[name] = true
		}
	}
	var found []string
	for _, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !passesArgs(call) {
				return true
			}
			for _, arg := range call.Args {
				if isBoolList(arg) {
					found = append(found, fset.Position(arg.Pos()).String())
				}
				if id, ok := arg.(*ast.Ident); ok && lists[id.Name] {
					found = append(found, fset.Position(arg.Pos()).String()+" ("+id.Name+")")
				}
			}
			return true
		})
	}
	for _, at := range found {
		t.Errorf("%s passes a hand-kept bool flag list beside the command's arguments; let the splitter read the FlagSet", at)
	}
}

// parsePackage parses every non-test Go file in this package.
func parsePackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed = append(parsed, file)
	}
	return fset, parsed
}

// TestEverySplittingCommandIsInTheTable fails on a function that calls
// parseArgs, parseCheckedArgs or parseOneArg and that is not a command in
// splitCommands nor called, directly or through other functions, by one,
// so a new command is held to TestEveryCommandSplitsItsOwnBoolFlags the
// day it is written.
func TestEverySplittingCommandIsInTheTable(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range splitCommands {
		// The name is the package path, a dot, then the function's name.
		name := runtime.FuncForPC(reflect.ValueOf(c.call).Pointer()).Name()
		covered[name[strings.LastIndex(name, ".")+1:]] = true
	}
	fset, parsed := parsePackage(t)
	// callers maps each function in the package to the functions that call
	// it by name, so a helper that splits for commands (reviewFlags splits
	// for ai review show, approve and deny) counts as covered when a
	// command in the table reaches it.
	callers := map[string][]string{}
	type splitCall struct {
		fn, callee string
		at         token.Pos
	}
	var splits []splitCall
	for _, file := range parsed {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			// The splitter's own functions call one another.
			if !ok || fn.Body == nil || fn.Name.Name == "parseArgs" || fn.Name.Name == "parseOneArg" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				callers[id.Name] = append(callers[id.Name], fn.Name.Name)
				if id.Name == "parseArgs" || id.Name == "parseOneArg" || id.Name == "parseCheckedArgs" {
					splits = append(splits, splitCall{fn: fn.Name.Name, callee: id.Name, at: call.Pos()})
				}
				return true
			})
		}
	}
	var reached func(name string, seen map[string]bool) bool
	reached = func(name string, seen map[string]bool) bool {
		if covered[name] {
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for _, caller := range callers[name] {
			if reached(caller, seen) {
				return true
			}
		}
		return false
	}
	for _, s := range splits {
		if !reached(s.fn, map[string]bool{}) {
			t.Errorf("%s: %s calls %s and no command in splitCommands reaches it", fset.Position(s.at), s.fn, s.callee)
		}
	}
}

// passesArgs reports whether call passes the identifier args, or a slice
// of it such as args[1:], as one of its arguments.
func passesArgs(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if s, ok := arg.(*ast.SliceExpr); ok {
			arg = s.X
		}
		if id, ok := arg.(*ast.Ident); ok && id.Name == "args" {
			return true
		}
	}
	return false
}

// isBoolList reports whether expr is a map[string]bool composite literal
// with at least one entry.
func isBoolList(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || len(lit.Elts) == 0 {
		return false
	}
	m, ok := lit.Type.(*ast.MapType)
	if !ok {
		return false
	}
	key, kok := m.Key.(*ast.Ident)
	value, vok := m.Value.(*ast.Ident)
	return kok && vok && key.Name == "string" && value.Name == "bool"
}

// boolListNames returns every name file binds to a non-empty
// map[string]bool literal, by var declaration or by :=.
func boolListNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.ValueSpec:
			for i, value := range d.Values {
				if i < len(d.Names) && isBoolList(value) {
					names[d.Names[i].Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, value := range d.Rhs {
				if i >= len(d.Lhs) || !isBoolList(value) {
					continue
				}
				if id, ok := d.Lhs[i].(*ast.Ident); ok {
					names[id.Name] = true
				}
			}
		}
		return true
	})
	return names
}
