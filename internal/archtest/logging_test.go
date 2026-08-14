package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The structural enforcement of the ordering constraint on the logging
// seam, recorded in the specification before either mechanism existed:
// apply the masking ruleset through slog.HandlerOptions.ReplaceAttr, not
// inside a wrapping slog.Handler, and make sure EVERY terminal writer
// carries it including any stderr fallback.
//
// internal/redact proves the ruleset works and, in wrapper_control_test.go,
// proves the rejected design does not. Neither proves it is actually
// installed everywhere. This file does, by reading the composition roots'
// own source.
//
// It is source inspection rather than a runtime check on purpose. There is
// no runtime moment at which "every binary configured its logger" is
// observable: each binary configures its own, in its own main, and the one
// that forgot is the one nobody runs in the test suite. The failure this
// prevents is a fourth binary added later whose author did not know the
// rule, and the only thing that reaches that author in time is a build
// failure.

// maskerOptionsMethod is the constructor a handler's options must come
// from. redact.Masker.HandlerOptions returns options that already carry
// the ReplaceAttr function, so a caller cannot hold the ruleset and forget
// to install it.
const maskerOptionsMethod = "HandlerOptions"

// slogHandlerConstructors are the standard library handler constructors
// whose options argument this test inspects.
var slogHandlerConstructors = map[string]bool{
	"NewJSONHandler": true,
	"NewTextHandler": true,
}

// TestEverySlogHandlerCarriesTheMaskingRuleset asserts that every
// slog handler constructed in a cmd/ binary takes its options from
// redact.Masker.HandlerOptions.
//
// The specific failure it exists to prevent is the one this repository
// already had: cmd/controller passed a literal nil for its handler
// options, which is valid Go, produces a working logger, and means no
// masking at all. Nothing about a nil there looks wrong at a glance.
func TestEverySlogHandlerCarriesTheMaskingRuleset(t *testing.T) {
	offenders := make([]string, 0)

	forEachCommandFile(t, func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !slogHandlerConstructors[sel.Sel.Name] {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "slog" {
				return true
			}

			where := fset.Position(call.Pos()).String()

			// slog.NewJSONHandler(w, opts): the options are the second
			// argument.
			if len(call.Args) < 2 {
				offenders = append(offenders, where+": handler constructed with no options argument")
				return true
			}

			if !isMaskerHandlerOptions(call.Args[1]) {
				offenders = append(offenders, where+": handler options do not come from a redact Masker's "+maskerOptionsMethod)
			}
			return true
		})
	})

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"a slog handler was built without the shared secret-masking ruleset.\n"+
				"Build its options with redact.Shared().%s(level) instead. Passing nil is valid Go, produces a working "+
				"logger, and means no masking at all, which is why this is a build failure rather than a review comment.\n"+
				"The ruleset must reach the handler through ReplaceAttr rather than a wrapping slog.Handler: a wrapper "+
				"cannot see attributes added with Logger.With and sees the message only as an opaque string. See "+
				"internal/redact/wrapper_control_test.go, which demonstrates both failures against a real one.\n%v",
			maskerOptionsMethod, offenders)
	}
}

// TestEveryCommandUsingTheLogPackageMasksItsOutput is the corollary, and
// it covers the half a slog-only check would miss entirely.
//
// A log.Fatalf or log.Printf bypasses slog completely and still reaches an
// operator's terminal. cmd/runner alone calls it a dozen times, all on
// fatal paths, which is to say a masking control that skipped this would
// emit unmasked exactly when things are going wrong. PLAN.md Section 25
// calls this axis irreversible: anything already leaked is durable.
func TestEveryCommandUsingTheLogPackageMasksItsOutput(t *testing.T) {
	usesLog := make(map[string]bool)
	setsOutput := make(map[string]bool)

	forEachCommandFile(t, func(path string, fset *token.FileSet, file *ast.File) {
		dir := filepath.Dir(path)

		for _, imp := range file.Imports {
			if imp.Path.Value == `"log"` {
				usesLog[dir] = true
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SetOutput" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "log" {
				return true
			}
			if len(call.Args) == 1 && isMaskerWriter(call.Args[0]) {
				setsOutput[dir] = true
			}
			return true
		})
	})

	missing := make([]string, 0)
	for dir := range usesLog {
		if !setsOutput[dir] {
			missing = append(missing, dir)
		}
	}

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf(
			"a cmd/ binary imports the standard library log package without routing it through the masking ruleset.\n"+
				"Add log.SetOutput(redact.Shared().Writer(os.Stderr)) in main. A log.Fatalf bypasses slog entirely and "+
				"still reaches a terminal, so a binary masking only its structured output emits unmasked on exactly the "+
				"paths that matter most.\n%v",
			missing)
	}
}

// isMaskerHandlerOptions reports whether expr is a call to HandlerOptions
// on something.
//
// The check is deliberately structural rather than type-checked: it looks
// for a call to a method named HandlerOptions, which in this module only
// redact.Masker has. Loading full type information for every cmd/ package
// would make this test slower than everything else in the file combined,
// and the failure mode of the cheap version is a false pass on a
// deliberately-named decoy, which nobody writes by accident. The failure
// it is guarding against is somebody passing nil.
func isMaskerHandlerOptions(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == maskerOptionsMethod
}

// isMaskerWriter reports whether expr is a call to a method named Writer,
// which in this module only redact.Masker has. Same structural-rather-than-
// typed reasoning as isMaskerHandlerOptions.
func isMaskerWriter(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Writer"
}

// forEachCommandFile parses every non-test Go file under cmd/ and hands it
// to fn.
func forEachCommandFile(t *testing.T, fn func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()

	root := filepath.Join(moduleRoot(t), "cmd")
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		// ImportsOnly stops at the import block, which is enough for the
		// import scan but not for the call scan, so reparse in full. Doing
		// it in two passes keeps the common case cheap and this is a
		// handful of files.
		full, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		full.Imports = file.Imports

		fn(path, fset, full)
		return nil
	})
	if err != nil {
		t.Fatalf("walking cmd/: %v", err)
	}
}

// moduleRoot walks up from the working directory to the directory holding
// go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}
