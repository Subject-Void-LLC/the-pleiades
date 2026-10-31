// Package archtest: the collection coverage ledger is held to the registry,
// to its vendored contracts, to the tests it names and to its floors.
package archtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestCoverageLedgers holds every ledger under coverage/collections to what a
// reader of its generated page is told:
//
//   - the ledger breaks none of its own rules (coverageledger.Ledger.Problems);
//   - every FQCN it names is registered, every registered method of the
//     collection implements an operation or says why it implements none, and
//     every one has a proof entry, so a new method cannot arrive unaccounted;
//   - an OpenAPI contract's operations and digest are exactly what its
//     vendored file holds, so the list cannot drift from the file;
//   - every test a proof names exists, and its package's build, tests
//     included, reaches the package that registers the method;
//   - no collection reads under its floor in coverage-floor-collections.json.
//
// A proof is declared, not measured: this proves the named test could
// exercise the method, not that it ran or that it covers every operation.
func TestCoverageLedgers(t *testing.T) {
	root := repoRoot(t)
	ledgers, err := coverageledger.LoadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledgers) == 0 {
		t.Fatal("no ledger under coverage/collections, so this rule examined nothing")
	}
	floors, err := coverageledger.LoadFloors(root)
	if err != nil {
		t.Fatal(err)
	}
	reach := newReachCache(t, root)
	for _, l := range ledgers {
		for _, p := range l.Problems() {
			t.Error(p)
		}
		methods := collectionMethods(l.Contract.Collection)
		checkLedgerNames(t, l, methods)
		if l.Contract.Source.Kind == coverageledger.SourceOpenAPI {
			checkVendoredContract(t, l)
		}
		checkProofTests(t, root, l, reach)

		report := l.Compute(methods)
		drops, floored := floors.Drops(report)
		for _, d := range drops {
			t.Error(d)
		}
		if !floored {
			t.Logf("%s has no floor in %s yet (mapped %.1f%%, live %.1f%%)", report.Collection, coverageledger.FloorFileName, report.MappedPercent, report.LivePercent)
		}
	}
}

// collectionMethods returns every catalog FQCN in collection, in order.
func collectionMethods(collectionName string) []string {
	names := make([]string, 0, len(catalogdata.Collections))
	for _, cfg := range catalogdata.Collections {
		names = append(names, cfg.Name)
	}
	return coverageledger.MethodsIn(collectionName, names)
}

// checkLedgerNames holds the FQCNs l names to the registry and to methods.
func checkLedgerNames(t *testing.T, l coverageledger.Ledger, methods []string) {
	t.Helper()
	c := l.Contract.Collection
	named := map[string]bool{}
	for _, m := range l.Map.Ops {
		for _, fqcn := range m.Methods {
			named[fqcn] = true
		}
	}
	for fqcn := range l.Map.MethodsWithoutOp {
		named[fqcn] = true
	}
	for fqcn := range named {
		if _, ok := collection.Lookup(fqcn); !ok {
			t.Errorf("%s: the ledger names %s, which is not registered", c, fqcn)
		}
	}
	for fqcn := range l.Map.Proof {
		if _, ok := collection.Lookup(fqcn); !ok {
			t.Errorf("%s: proof for %s, which is not registered", c, fqcn)
		}
	}
	for _, fqcn := range methods {
		if !named[fqcn] {
			t.Errorf("%s: %s implements no operation in map.yaml and is not in methods_without_op", c, fqcn)
		}
		if _, ok := l.Map.Proof[fqcn]; !ok {
			t.Errorf("%s: %s has no proof entry (write status: none for an OS it is not proven on)", c, fqcn)
		}
	}
}

// checkVendoredContract holds an OpenAPI contract to its vendored file.
func checkVendoredContract(t *testing.T, l coverageledger.Ledger) {
	t.Helper()
	c := l.Contract
	doc, err := os.ReadFile(filepath.Join(l.Dir, c.Source.File)) // #nosec G304 -- a file the repository's own ledger names
	if err != nil {
		t.Errorf("%s: %v", c.Collection, err)
		return
	}
	sum := sha256.Sum256(doc)
	if got := hex.EncodeToString(sum[:]); got != c.Source.SHA256 {
		t.Errorf("%s: %s has sha256 %s, contract.json says %s: run go run ./tools/genledger", c.Collection, c.Source.File, got, c.Source.SHA256)
	}
	ops, err := coverageledger.ExtractOpenAPI(doc)
	if err != nil {
		t.Errorf("%s: %v", c.Collection, err)
		return
	}
	if !reflect.DeepEqual(ops, c.Ops) {
		t.Errorf("%s: contract.json's %d operations are not the %d %s defines: run go run ./tools/genledger", c.Collection, len(c.Ops), len(ops), c.Source.File)
	}
}

// checkProofTests holds every test a proof in l names to the source tree.
func checkProofTests(t *testing.T, root string, l coverageledger.Ledger, reach *reachCache) {
	t.Helper()
	for fqcn, byOS := range l.Map.Proof {
		desc, registered := collection.Lookup(fqcn)
		for os, p := range byOS {
			if p.Test == "" {
				continue
			}
			dir, name := coverageledger.SplitTest(p.Test)
			if !testFuncExists(t, filepath.Join(root, dir), name) {
				t.Errorf("%s on %s names %s, and no %s(t *testing.T) is in %s", fqcn, os, p.Test, name, dir)
				continue
			}
			if !registered || desc.Invoke == nil {
				continue
			}
			pkg := funcPackage(desc.Invoke)
			if !reach.reaches(dir, pkg) {
				t.Errorf("%s on %s names %s, whose package's build never reaches %s, where the method is registered", fqcn, os, p.Test, pkg)
			}
		}
	}
}

// testFuncExists reports whether a _test.go file in dir declares func name(t
// *testing.T).
func testFuncExists(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == name && fn.Type.Params.NumFields() == 1 {
				return true
			}
		}
	}
	return false
}

// reachCache answers, once per package directory, which packages its build
// with its tests depends on.
type reachCache struct {
	t    *testing.T
	root string
	deps map[string]map[string]bool
}

// newReachCache returns an empty cache over root.
func newReachCache(t *testing.T, root string) *reachCache {
	return &reachCache{t: t, root: root, deps: map[string]map[string]bool{}}
}

// reaches reports whether the package in dir, built with its tests, depends
// on pkg (or is it).
func (r *reachCache) reaches(dir, pkg string) bool {
	if r.deps[dir] == nil {
		cmd := exec.Command("go", "list", "-e", "-deps", "-test", "-f", "{{.ImportPath}}", "./"+dir)
		cmd.Dir = r.root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			r.t.Fatalf("go list -deps -test ./%s: %v\n%s", dir, err, stderr.String())
		}
		set := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			// A test variant lists as "pkg [pkg.test]"; the path is the
			// first field.
			if fields := strings.Fields(line); len(fields) > 0 {
				set[fields[0]] = true
			}
		}
		r.deps[dir] = set
	}
	return r.deps[dir][pkg]
}
