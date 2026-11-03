// Phase 41b: a built-in method that emits a fact declares it.
package archtest

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

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// emittingFiles returns every non-test Go file under internal/catalog that
// calls a method named EmitFact, by absolute path.
func emittingFiles(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join(repoRoot(t), "internal", "catalog")
	out := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "EmitFact" {
					out[path] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEveryFactEmitterDeclaresItsFacts: a built-in method whose Invoke is
// defined in a file that calls EmitFact declares its facts
// (Manifest.Facts), since the engine stores nothing a method did not
// declare and an undeclared emitter would lose every fact silently. The
// engine's own refusal is the real gate; this catches the omission at the
// commit rather than in a run's log. A method calling EmitFact only from a
// helper in another file is not attributed here, which is why the control
// below counts the emitters it found.
func TestEveryFactEmitterDeclaresItsFacts(t *testing.T) {
	emitting := emittingFiles(t)
	if len(emitting) == 0 {
		t.Fatal("no file under internal/catalog calls EmitFact, so this test proved nothing")
	}
	found := 0
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Invoke == nil {
			continue
		}
		pc := reflect.ValueOf(desc.Invoke).Pointer()
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		file, _ := fn.FileLine(pc)
		if !emitting[filepath.Clean(file)] {
			continue
		}
		found++
		if len(desc.Manifest.Facts) == 0 {
			t.Errorf("%s emits facts (%s) and declares none (Manifest.Facts)", cfg.Name, file)
		}
	}
	// facts.gather, net.ios.facts and the four net.catalyst fact methods.
	if found < 6 {
		t.Errorf("found %d emitting methods, want at least 6: the attribution by Invoke's file stopped working", found)
	}
}
