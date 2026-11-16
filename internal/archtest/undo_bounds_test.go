// Package archtest: the bounds a Collection copies from the run
// journal, held to the journal's own.
//
// A Collection may not import internal/engine, so net.netconf.config and
// net.gnmi.config each keep a copy of the journal's bounds on an undo value
// (internal/engine's maxRecordedText and maxRecordedItems), refusing an undo
// past them before it is recorded. A copy that drifts from the journal either
// refuses undos the journal would keep, or records undos the journal then
// withholds, under a manifest saying they are exact. This test reads each
// copy from its package's source and holds it to what the journal keeps.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// undoBound is one copy of a journal bound: the package directory holding it,
// the constant's name, and which bound it copies.
type undoBound struct {
	dir, name string
	// items says it copies the bound on a list's length rather than on one
	// text's.
	items bool
}

// undoBounds are every copy of the journal's bounds.
var undoBounds = []undoBound{
	{dir: "internal/catalog/net/netconf", name: "maxUndoBytes"},
	{dir: "internal/catalog/net/gnmi", name: "maxUndoPathBytes"},
	{dir: "internal/catalog/net/gnmi", name: "maxUndoDeletes", items: true},
}

// constValue is the integer value of the constant named name declared in the
// non-test Go files of dir, and whether one was found.
func constValue(t *testing.T, dir, name string) (int, bool) {
	t.Helper()
	value, found := 0, false
	walkGoFiles(t, dir, func(path string, file *ast.File, _ *token.FileSet) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, id := range spec.Names {
				if id.Name != name || i >= len(spec.Values) {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					t.Fatalf("%s: %s is not an integer literal, so this test cannot read it", path, name)
				}
				v, err := strconv.Atoi(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				value, found = v, true
			}
			return true
		})
	})
	return value, found
}

// TestUndoBoundsMatchTheJournals: each copy is exactly the bound the journal
// keeps to, read from the journal's own behavior: a text of the copy's length
// is kept and one byte more is not, and a list of the copy's length is kept
// and one item more is not.
func TestUndoBoundsMatchTheJournals(t *testing.T) {
	root := repoRoot(t)
	for _, b := range undoBounds {
		n, ok := constValue(t, filepath.Join(root, b.dir), b.name)
		if !ok {
			t.Errorf("%s declares no constant %s: update this test with the copy's new name", b.dir, b.name)
			continue
		}
		if b.items {
			list := func(k int) []string {
				out := make([]string, k)
				for i := range out {
					out[i] = "x"
				}
				return out
			}
			_, keeps := engine.RecordableTexts(list(n))
			_, keepsMore := engine.RecordableTexts(list(n + 1))
			if !keeps || keepsMore {
				t.Errorf("%s.%s is %d, but the journal keeps a list of %d: %v, of %d: %v", b.dir, b.name, n, n, keeps, n+1, keepsMore)
			}
			continue
		}
		if !engine.RecordableText(strings.Repeat("x", n)) || engine.RecordableText(strings.Repeat("x", n+1)) {
			t.Errorf("%s.%s is %d, which is not the length of text the journal keeps at most", b.dir, b.name, n)
		}
	}
}
