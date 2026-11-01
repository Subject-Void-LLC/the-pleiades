// Tests of reading, checking and adding up a coverage ledger, on ledgers
// written to a temporary directory.
package coverageledger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
)

// digest is a well-formed sha256 for a fixture contract.
const digest = "81ef3ecc951051010a4ec6941d6b02f862f84e3a4e98e064871fd38475a3ba20"

// contractJSON is a three-operation CLI contract for collection x.tool.
const contractJSON = `{"collection": "x.tool", "title": "a tool",
 "source": {"kind": "cli", "url": "https://example.test/tool.1", "version": "1.0", "sha256": "` + digest + `"},
 "ops": [{"id": "add"}, {"id": "drop"}, {"id": "dump"}, {"id": "gc"}]}`

// writeLedger writes one ledger directory named name under root's
// coverage/collections and returns root.
func writeLedger(t *testing.T, root, name, contract, mapping string) string {
	t.Helper()
	dir := filepath.Join(root, coverageledger.CollectionsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{"contract.json": contract, "map.yaml": mapping} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestLoadAll_ReadsEveryLedgerStrictly: ledgers come back by collection, an
// empty map.yaml is a ledger with nothing mapped, and an unknown key in either
// file or a directory named for another collection is refused.
func TestLoadAll_ReadsEveryLedgerStrictly(t *testing.T) {
	root := writeLedger(t, t.TempDir(), "x.tool", contractJSON, "")
	ledgers, err := coverageledger.LoadAll(root)
	if err != nil || len(ledgers) != 1 || len(ledgers[0].Contract.Ops) != 4 || ledgers[0].Map.Ops != nil {
		t.Fatalf("LoadAll: %v, %+v", err, ledgers)
	}

	for name, tc := range map[string]struct{ dir, contract, mapping, want string }{
		"a misspelled map key":      {"x.tool", contractJSON, "ops:\n  add:\n    exluded: no\n", "exluded"},
		"an unknown contract key":   {"x.tool", strings.Replace(contractJSON, `"title"`, `"titel"`, 1), "", "titel"},
		"a directory misnamed":      {"y.tool", contractJSON, "", "not its directory's name"},
		"a contract that is broken": {"x.tool", "{", "", "contract.json"},
	} {
		root := writeLedger(t, t.TempDir(), tc.dir, tc.contract, tc.mapping)
		if _, err := coverageledger.LoadAll(root); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
	if _, err := coverageledger.LoadAll(t.TempDir()); err == nil {
		t.Error("a repository with no ledger directory was read")
	}
}

// TestProblems_HoldsTheMapToTheContract: a correct ledger has no problems, and
// each way of breaking one is named.
func TestProblems_HoldsTheMapToTheContract(t *testing.T) {
	good := `ops:
  add: {methods: [x.tool.add]}
  drop: {excluded: "no runbook wants it"}
methods_without_op:
  x.tool.facts: "reads several commands at once"
proof:
  x.tool.add:
    linux: {status: live, test: internal/catalog/x/tool.TestAdd}
    darwin: {status: none}
`
	ledgers, err := coverageledger.LoadAll(writeLedger(t, t.TempDir(), "x.tool", contractJSON, good))
	if err != nil {
		t.Fatal(err)
	}
	if got := ledgers[0].Problems(); len(got) != 0 {
		t.Fatalf("a correct ledger has problems: %v", got)
	}

	for name, tc := range map[string]struct{ contract, mapping, want string }{
		"an operation not in the contract": {contractJSON, "ops:\n  zap: {methods: [x.tool.zap]}\n", `"zap", which the contract does not define`},
		"both methods and excluded":        {contractJSON, "ops:\n  add: {methods: [x.tool.add], excluded: why}\n", "exactly one of"},
		"neither":                          {contractJSON, "ops:\n  add: {}\n", "exactly one of"},
		"another collection's method":      {contractJSON, "ops:\n  add: {methods: [y.tool.add]}\n", "not in the collection"},
		"a method without op, no reason":   {contractJSON, "methods_without_op:\n  x.tool.facts: \"\"\n", "gives no reason"},
		"an unknown OS":                    {contractJSON, "proof:\n  x.tool.add:\n    plan9: {status: none}\n", `OS "plan9"`},
		"live with no test":                {contractJSON, "proof:\n  x.tool.add:\n    linux: {status: live}\n", "needs a test"},
		"none naming a test":               {contractJSON, "proof:\n  x.tool.add:\n    linux: {status: none, test: a.TestB}\n", "none but names a test"},
		"an unknown status":                {contractJSON, "proof:\n  x.tool.add:\n    linux: {status: maybe}\n", `status "maybe"`},
		"proof outside the collection":     {contractJSON, "proof:\n  y.tool.add:\n    linux: {status: none}\n", "not in the collection"},
		"a short digest":                   {strings.Replace(contractJSON, digest, "abc", 1), "", "sha256"},
		"an unknown kind":                  {strings.Replace(contractJSON, `"cli"`, `"wsdl"`, 1), "", `kind "wsdl"`},
		"openapi with no vendored file":    {strings.Replace(contractJSON, `"cli"`, `"openapi"`, 1), "", "names none"},
		"an operation listed twice":        {strings.Replace(contractJSON, `{"id": "drop"}`, `{"id": "add"}`, 1), "", "listed twice"},
	} {
		ledgers, err := coverageledger.LoadAll(writeLedger(t, t.TempDir(), "x.tool", tc.contract, tc.mapping))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Join(ledgers[0].Problems(), "\n"); !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems %q, want one saying %q", name, got, tc.want)
		}
	}
}

// TestSplitTest splits a proof's test into its package directory and name,
// and refuses, without panicking, a reference that is not one or whose
// directory leaves the module.
func TestSplitTest(t *testing.T) {
	dir, name, ok := coverageledger.SplitTest("internal/catalog/virt/clab.TestLive_ARealLabEndToEnd")
	if !ok || dir != "internal/catalog/virt/clab" || name != "TestLive_ARealLabEndToEnd" {
		t.Errorf("got %q %q %v", dir, name, ok)
	}
	for _, bad := range []string{"", "TestFoo", "a.", ".", ".TestA", "x.Foo", "a.Testfoo", "../../x.TestA", "/abs/dir.TestA", "a/../../b.TestA"} {
		if dir, name, ok := coverageledger.SplitTest(bad); ok {
			t.Errorf("SplitTest(%q) = %q %q, accepted", bad, dir, name)
		}
	}
}

// TestLoad_ReadsNeitherFileThroughALinkOut: contract.json and map.yaml are
// read inside the ledger's directory, so a symbolic link to a file outside it
// is refused, as ReadSource refuses one.
func TestLoad_ReadsNeitherFileThroughALinkOut(t *testing.T) {
	outside := t.TempDir()
	for _, file := range []string{"contract.json", "map.yaml"} {
		body := contractJSON
		if file == "map.yaml" {
			body = ""
		}
		if err := os.WriteFile(filepath.Join(outside, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, linked := range []string{"contract.json", "map.yaml"} {
		root := writeLedger(t, t.TempDir(), "x.tool", contractJSON, "")
		dir := filepath.Join(root, coverageledger.CollectionsDir, "x.tool")
		if err := os.Remove(filepath.Join(dir, linked)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, linked), filepath.Join(dir, linked)); err != nil {
			t.Skipf("symbolic links unavailable here: %v", err)
		}
		if _, err := coverageledger.Load(dir); err == nil {
			t.Errorf("a %s linked out of the directory was read", linked)
		}
	}
}

// TestReadSource_StaysInsideTheLedger: the vendored file is read, and neither
// a ".." in source.file nor a symbolic link pointing out of the directory
// reads anything outside it; Problems names the first as well.
func TestReadSource_StaysInsideTheLedger(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := func(file string) string {
		return strings.Replace(strings.Replace(contractJSON, `"cli"`, `"openapi"`, 1), `"sha256"`, `"file": "`+file+`", "sha256"`, 1)
	}
	root := writeLedger(t, t.TempDir(), "x.tool", contract("source/api.json"), "")
	dir := filepath.Join(root, coverageledger.CollectionsDir, "x.tool")
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source", "api.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := coverageledger.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := l.ReadSource(); err != nil || string(got) != "{}" {
		t.Fatalf("ReadSource: %q, %v", got, err)
	}

	l.Contract.Source.File = "../../../../" + filepath.Base(outside) + "/secret"
	if got, err := l.ReadSource(); err == nil {
		t.Errorf("a .. path was read: %q", got)
	}
	if p := strings.Join(l.Problems(), "\n"); !strings.Contains(p, "not a path inside") {
		t.Errorf("Problems did not name the .. path: %q", p)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "source", "link")); err != nil {
		t.Skipf("symbolic links unavailable here: %v", err)
	}
	l.Contract.Source.File = "source/link"
	if got, err := l.ReadSource(); err == nil {
		t.Errorf("a symbolic link out of the directory was read: %q", got)
	}
}
