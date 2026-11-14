// Tests for SeededLogins: which tasks of a compiled runbook the Controller
// refuses because they would seed a login.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerSeedingParam registers name as a method that seeds the login its
// "device" parameter names, the parameter required when required is set.
func registerSeedingParam(t *testing.T, name string, required bool) {
	t.Helper()
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			SeedsLogin:    "device",
			Doc:           collection.Doc{Params: []collection.Param{{Name: "device", Type: "string", Required: required}}},
		},
		Invoke: noopMethod,
	}); err != nil {
		t.Fatal(err)
	}
}

// noopMethod is a method body that does nothing.
func noopMethod(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
	return collection.Result{}, nil
}

// TestSeededLogins_NamesEveryTaskThatWouldSeed builds a runbook with a
// required seeding parameter set and left out, an optional one set, empty
// and left out, a conditional seeding task, a rollback step that seeds, and
// a method that seeds nothing. Every task that would seed on a Runner is
// named, by node, method and device; the rest are not.
func TestSeededLogins_NamesEveryTaskThatWouldSeed(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	registerSeedingParam(t, "seedtest.required", true)
	registerSeedingParam(t, "seedtest.optional", false)
	plain := registerReadOnly(t, "seedtest_plain", nil, noopMethod)

	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: seeds
tasks:
  - name: required, set
    seedtest.required:
      device: lab-web
  - name: required, left out
    seedtest.required: {}
  - name: optional, set
    seedtest.optional:
      device: "{{ vars.box }}"
  - name: optional, empty
    seedtest.optional:
      device: ""
  - name: optional, left out
    seedtest.optional: {}
  - name: conditional
    seedtest.required:
      device: lab-db
    when: ["1 == 2"]
  - name: plain, with a seeding undo
    ` + plain + `: {}
    rollback:
      - name: undo
        seedtest.optional:
          device: lab-undo
`))
	if err != nil {
		t.Fatal(err)
	}
	got := engine.SeededLogins(dag)
	want := []string{
		`task "tasks[0]" runs seedtest.required, which seeds the login of device "lab-web"`,
		`task "tasks[1]" runs seedtest.required, which seeds the login of device (not set)`,
		`task "tasks[2]" runs seedtest.optional, which seeds the login of device "{{ vars.box }}"`,
		`task "tasks[5]" runs seedtest.required, which seeds the login of device "lab-db"`,
		`task "tasks[6].rollback[0]" runs seedtest.optional, which seeds the login of device "lab-undo"`,
	}
	if len(got) != len(want) {
		t.Fatalf("SeededLogins = %q, want %d lines", got, len(want))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("line %d = %q, want it to start %q", i, got[i], want[i])
		}
	}
	if engine.SeededLogins(nil) != nil {
		t.Error("SeededLogins(nil) is not nil")
	}
}

// TestSeededLogins_ReadsTheInstalledCatalog: a Controller registers no
// Collection, so the seeding method is known to it only through the module
// catalog it installs; the task is still named.
func TestSeededLogins_ReadsTheInstalledCatalog(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte(`{"seedtest.catalog_only":{"status":"implemented","seedsLogin":"login","doc":{"params":[{"name":"login","type":"string","required":true}]}}}`)); err != nil {
		t.Fatal(err)
	}
	// The builder refuses a method it does not know, so the DAG is built
	// by hand, as a compiled one would be.
	dag := &engine.DAG{Nodes: map[string]*engine.Task{
		"tasks[0]": {FQCN: "seedtest.catalog_only", Params: map[string]any{"login": "lab-web"}},
	}}
	if got := engine.SeededLogins(dag); len(got) != 1 || !strings.Contains(got[0], `device "lab-web"`) {
		t.Errorf("SeededLogins = %q, want the catalog-only method's task named", got)
	}
}

// TestNearMatches_CaseFirstThenTypos: a name differing only in case comes
// first, then names at most two edits away and under half its length (web02
// is two of four letters, so not), nearest first; a short name matches
// nothing two edits away, and the name itself is never its own match.
func TestNearMatches_CaseFirstThenTypos(t *testing.T) {
	known := []string{"web01", "web02", "WEB1", "db01", "web1", "x"}
	if got := engine.NearMatches("web1", known, 3); strings.Join(got, ",") != "WEB1,web01" {
		t.Errorf("NearMatches(web1) = %v", got)
	}
	if got := engine.NearMatches("ab", []string{"xy", "ab"}, 3); len(got) != 0 {
		t.Errorf("NearMatches(ab) = %v, want none", got)
	}
}
