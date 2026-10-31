// Tests of extracting OpenAPI operations, adding up a ledger and holding it
// to its floor.
package coverageledger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/coverageledger"
)

// TestExtractOpenAPI lists every method of every path, ordered by path and
// then method, skipping a path item's parameters, for Swagger 2 and OpenAPI 3
// alike, and refuses a document with no paths or a broken one.
func TestExtractOpenAPI(t *testing.T) {
	doc := `{"openapi": "3.0.0", "paths": {
	  "/b": {"parameters": [{"name": "q"}], "post": {"summary": " make b "}, "get": {"summary": "read b"}},
	  "/a": {"delete": {}, "head": {}, "trace": {}, "options": {}, "patch": {}, "put": {}}
	}}`
	ops, err := coverageledger.ExtractOpenAPI([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, op := range ops {
		ids = append(ids, op.ID)
	}
	want := "PUT /a,DELETE /a,OPTIONS /a,HEAD /a,PATCH /a,TRACE /a,GET /b,POST /b"
	if got := strings.Join(ids, ","); got != want {
		t.Errorf("operations %s, want %s", got, want)
	}
	if ops[7].Summary != "make b" {
		t.Errorf("summary %q is not trimmed", ops[7].Summary)
	}
	for _, bad := range []string{`{"swagger": "2.0", "paths": {}}`, `{`, `{"paths": {"/a": {"get": 1}}}`} {
		if _, err := coverageledger.ExtractOpenAPI([]byte(bad)); err == nil {
			t.Errorf("%s was read", bad)
		}
	}
}

// TestCompute_CountsAgainstWhatIsInScope: an excluded operation counts
// neither way, an operation is live only when every method implementing it
// is, and a registered method the ledger never names still has a row.
func TestCompute_CountsAgainstWhatIsInScope(t *testing.T) {
	mapping := `ops:
  add: {methods: [x.tool.add]}
  drop: {methods: [x.tool.drop, x.tool.add]}
  gc: {excluded: "no runbook wants it"}
proof:
  x.tool.add:
    linux: {status: live, test: a.TestA}
  x.tool.drop:
    linux: {status: fixture, test: a.TestB}
`
	ledgers, err := coverageledger.LoadAll(writeLedger(t, t.TempDir(), "x.tool", contractJSON, mapping))
	if err != nil {
		t.Fatal(err)
	}
	r := ledgers[0].Compute([]string{"x.tool.add", "x.tool.drop", "x.tool.unnamed"})
	if r.Total != 4 || r.Mapped != 2 || r.Excluded != 1 || r.Unmapped != 1 || r.Live != 1 {
		t.Errorf("counts %+v", r)
	}
	// 2 of 3 in scope mapped, 1 of 3 live.
	if r.MappedPercent != 66.7 || r.LivePercent != 33.3 {
		t.Errorf("percentages %.1f %.1f, want 66.7 33.3", r.MappedPercent, r.LivePercent)
	}
	if len(r.Methods) != 3 || r.Methods[2].FQCN != "x.tool.unnamed" || len(r.Methods[0].Ops) != 2 {
		t.Errorf("methods %+v", r.Methods)
	}
	if r.Ops[1].Live || !r.Ops[0].Live || r.Ops[3].Reason == "" || r.Ops[2].Status != coverageledger.OpUnmapped {
		t.Errorf("operation rows %+v", r.Ops)
	}

	empty := coverageledger.Ledger{Contract: coverageledger.Contract{Collection: "x.none"}}.Compute(nil)
	if empty.MappedPercent != 0 || empty.LivePercent != 0 {
		t.Errorf("an empty ledger reads %.1f %.1f", empty.MappedPercent, empty.LivePercent)
	}
}

// TestFloors_ADropFailsAndNoFloorIsReported: reading under a floor by more
// than the rounding tolerance is a drop on either percentage, within it is
// not, and a collection with no floor is reported as unfloored.
func TestFloors_ADropFailsAndNoFloorIsReported(t *testing.T) {
	root := t.TempDir()
	body := `{"_comment": "x", "floors": {"x.tool": {"mapped": 50, "live": 20}}}`
	if err := os.WriteFile(filepath.Join(root, coverageledger.FloorFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := coverageledger.LoadFloors(root)
	if err != nil {
		t.Fatal(err)
	}
	if drops, floored := f.Drops(coverageledger.Report{Collection: "x.tool", MappedPercent: 49.96, LivePercent: 20}); !floored || len(drops) != 0 {
		t.Errorf("within the tolerance: %v %v", drops, floored)
	}
	drops, _ := f.Drops(coverageledger.Report{Collection: "x.tool", MappedPercent: 49.9, LivePercent: 19})
	if len(drops) != 2 || !strings.Contains(drops[0], "mapped 49.9% is under its floor 50.0%") {
		t.Errorf("drops %v", drops)
	}
	if _, floored := f.Drops(coverageledger.Report{Collection: "y.tool"}); floored {
		t.Error("a collection with no floor reads as floored")
	}

	if err := os.WriteFile(filepath.Join(root, coverageledger.FloorFileName), []byte(`{"flors": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := coverageledger.LoadFloors(root); err == nil {
		t.Error("a misspelled floor file was read")
	}
	if _, err := coverageledger.LoadFloors(t.TempDir()); err == nil {
		t.Error("a missing floor file was read")
	}
}

// TestMethodsIn keeps a collection's own methods, in order, and no method of
// a collection whose name merely starts the same way.
func TestMethodsIn(t *testing.T) {
	got := coverageledger.MethodsIn("pkg.apt", []string{"pkg.apt.remove", "pkg.aptitude.install", "pkg.install", "pkg.apt.install"})
	if strings.Join(got, ",") != "pkg.apt.install,pkg.apt.remove" {
		t.Errorf("got %v", got)
	}
}
