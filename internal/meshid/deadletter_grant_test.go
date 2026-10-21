// Package meshid_test: the Controller's dead-letter grants, derived from
// the subjects it consumes rather than listed beside them.
package meshid_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// consumedSubjects maps each topology function a bus subscription names
// to the subject it returns. A subscription naming a function missing here
// fails TestControllerGrantDeadLettersEveryConsumedSubject, so whoever adds
// a consumer decides its dead-letter grant then, rather than finding out on
// a mesh where the dead letter is silently denied.
var consumedSubjects = map[string]string{
	"JobRequestedSubject": topology.JobRequestedSubject(),
	"JournalSubjectAll":   topology.JournalSubjectAll(),
	"ResultSubjectAll":    topology.ResultSubjectAll(),
}

// TestControllerGrantDeadLettersEveryConsumedSubject finds every
// event.Bus.Subscribe call in the code that ships (a three-argument
// Subscribe whose subject is a call into internal/topology), and requires
// ControllerGrant to permit publishing that subject's dead letter. Every
// bus subscriber is the Controller's: the Runner pulls its work on its own
// loop and subscribes to nothing through the bus.
//
// It exists because the result consumer shipped without that grant while
// the two consumers beside it had theirs. event.HandleDeliveryFailure
// returns before msg.Term when the dead-letter publish is denied, so a
// result the Controller failed to record MaxDeliver times was neither
// dead-lettered nor terminated, and its job stayed running.
func TestControllerGrantDeadLettersEveryConsumedSubject(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".claude" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if name, ok := topologySubscription(n); ok {
				rel, _ := filepath.Rel(root, fset.Position(n.Pos()).Filename)
				found[name] = append(found[name], filepath.ToSlash(rel))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("found no bus subscription at all; the scan is broken, not the grant")
	}

	grant := meshid.ControllerGrant("controller-1")
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		subject, known := consumedSubjects[name]
		if !known {
			t.Errorf("%v subscribe to topology.%s(), which consumedSubjects does not list: add it, and give ControllerGrant its dead-letter subject", found[name], name)
			continue
		}
		if dlq := topology.DeadLetterSubject(subject); !permits(grant.Pub, dlq) {
			t.Errorf("ControllerGrant does not permit %q, the dead letter of topology.%s() which %v consume", dlq, name, found[name])
		}
	}
}

// topologySubscription reports the internal/topology function n subscribes
// to, when n is a call X.Subscribe(ctx, topology.F(), handler).
func topologySubscription(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 3 {
		return "", false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "Subscribe" {
		return "", false
	}
	subject, ok := call.Args[1].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	fn, ok := subject.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != "topology" {
		return "", false
	}
	return fn.Sel.Name, true
}
