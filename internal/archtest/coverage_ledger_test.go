// Package archtest: the collection coverage ledger is held to the registry,
// to its vendored contracts, to the tests it names and to its floors.
package archtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
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
//   - no collection reads under its floor in coverage-floor-collections.json,
//     every ledger has a floor, and every floor has its ledger.
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
	var collections []string
	for _, l := range ledgers {
		collections = append(collections, l.Contract.Collection)
		for _, p := range l.Problems() {
			t.Error(p)
		}
		methods := collectionMethods(l.Contract.Collection)
		if len(methods) == 0 {
			// Every check below would then examine nothing.
			t.Errorf("%s: no catalog method is in this collection, so the ledger is held to nothing", l.Contract.Collection)
		}
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
			t.Errorf("%s has no floor in %s: add one at its numbers (mapped %.1f%%, live %.1f%%)", report.Collection, coverageledger.FloorFileName, report.MappedPercent, report.LivePercent)
		}
	}
	for _, orphan := range floors.Orphans(collections) {
		t.Errorf("%s has a floor in %s and no ledger under %s: a ledger deleted or renamed takes its floor with it only by editing that file", orphan, coverageledger.FloorFileName, coverageledger.CollectionsDir)
	}
	checkPinnedRevisions(t, root)
}

// checkPinnedRevisions holds the vendored containerlab API contract to the
// image the live tests run: both name one upstream commit, so bumping one
// without the other fails here rather than leaving the ledger describing an
// API the tests no longer ask.
func checkPinnedRevisions(t *testing.T, root string) {
	t.Helper()
	notice, err := os.ReadFile(filepath.Join(root, coverageledger.CollectionsDir, "virt.clab", "source", "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	pin, err := os.ReadFile(filepath.Join(root, "internal", "testsupport", "clabapi.go"))
	if err != nil {
		t.Fatal(err)
	}
	commit := regexp.MustCompile(`commit ([0-9a-f]{40})`).FindSubmatch(notice)
	if commit == nil {
		t.Fatal("virt.clab's NOTICE names no upstream commit")
	}
	if !bytes.Contains(pin, commit[1]) {
		t.Errorf("virt.clab's swagger.json is from commit %s, and internal/testsupport.ClabAPIImage does not name it: refresh both together", commit[1])
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
		desc, ok := collection.Lookup(fqcn)
		switch {
		case !ok:
			t.Errorf("%s: the ledger names %s, which is not registered", c, fqcn)
		case desc.Manifest.Status != collection.StatusImplemented:
			// A declared method implements nothing: mapping an operation to it
			// would count that operation as covered.
			t.Errorf("%s: the ledger names %s, which is %s, not implemented", c, fqcn, desc.Manifest.Status)
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
	doc, err := l.ReadSource()
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
			dir, name, ok := coverageledger.SplitTest(p.Test)
			if !ok {
				continue // Problems names it
			}
			// SplitTest's check is of the text alone: a committed symbolic
			// link named dir would read and list a directory outside the
			// module (vuln-corpus C4, as ReadSource's was).
			if !insideRoot(root, dir) {
				t.Errorf("%s on %s names %s, whose directory resolves outside the repository", fqcn, os, p.Test)
				continue
			}
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

// testFuncExists reports whether a _test.go file in dir that some supported
// platform builds declares func name(t *testing.T). A file no platform builds
// (//go:build ignore) holds a test that never runs, so it does not count.
func testFuncExists(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		// A test file that is a symbolic link is neither parsed nor counted:
		// its directory being inside the repository says nothing of where
		// the link points.
		if info, err := os.Lstat(f); err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		if !someOSBuilds(parsed) {
			continue
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

// insideRoot reports whether dir, relative to root, resolves, symbolic links
// followed, to a path inside root.
func insideRoot(root, dir string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, dir))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, real)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// proofOSes are the platforms a proof may name, by GOOS.
var proofOSes = []string{"linux", "freebsd", "darwin", "windows"}

// someOSBuilds reports whether file's //go:build line, if it has one, holds
// for at least one of proofOSes, with the repository's integration tag on.
func someOSBuilds(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			for _, goos := range proofOSes {
				if expr.Eval(func(tag string) bool {
					return tag == goos || tag == "integration" || (tag == "unix" && goos != "windows") || strings.HasPrefix(tag, "go1.")
				}) {
					return true
				}
			}
			return false
		}
	}
	return true
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

// TestCoverageLedgerChecks_CanFail: the checks TestCoverageLedgers relies on
// go red on what they exist to catch: a test that is missing, or only in a
// file no platform builds, and a package whose build never reaches the
// method's.
func TestCoverageLedgerChecks_CanFail(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"real_test.go":    "package x\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n",
		"ignored_test.go": "//go:build ignore\n\npackage x\n\nimport \"testing\"\n\nfunc TestIgnored(t *testing.T) {}\n",
		"linux_test.go":   "//go:build linux\n\npackage x\n\nimport \"testing\"\n\nfunc TestLinuxOnly(t *testing.T) {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]bool{"TestReal": true, "TestLinuxOnly": true, "TestIgnored": false, "TestMissing": false} {
		if got := testFuncExists(t, dir, name); got != want {
			t.Errorf("testFuncExists(%s) = %v, want %v", name, got, want)
		}
	}
	// A directory that is a symbolic link out of the root does not count as
	// inside it, though its text is local.
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !insideRoot(root, "in") {
		t.Error("insideRoot refused a real directory inside the root")
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err == nil {
		if insideRoot(root, "out") {
			t.Error("insideRoot accepted a symbolic link out of the root")
		}
		// A test file linked from outside is not read, though its directory
		// is inside.
		if err := os.WriteFile(filepath.Join(outside, "elsewhere_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc TestElsewhere(t *testing.T) {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "elsewhere_test.go"), filepath.Join(dir, "linked_test.go")); err != nil {
			t.Fatal(err)
		}
		if testFuncExists(t, dir, "TestElsewhere") {
			t.Error("testFuncExists read a test file linked from outside the directory")
		}
	} else {
		t.Logf("symbolic links unavailable here, so the link case is not checked: %v", err)
	}

	reach := newReachCache(t, repoRoot(t))
	const clab = "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/clab"
	if !reach.reaches("internal/catalog/virt/clab", clab) {
		t.Error("the clab package's own build does not reach it")
	}
	if reach.reaches("internal/redact", clab) {
		t.Error("internal/redact's build reaches the clab collection, so reaches proves nothing")
	}
}
