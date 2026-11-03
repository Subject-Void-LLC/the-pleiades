// Tests that the Controller, which links no Collection, answers questions
// about built-in methods from the catalog it installs (FAILURE_PATTERNS 841),
// in this binary's own test binary, which links no Collection either.
package main

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestInstallBuiltinCatalog_TheControllerTrustsABuiltInsRecordedUndo: before
// the catalog, this binary knows no built-in method, and the rollback route
// refused every recorded undo as "not a built-in method this build runs";
// after main's installBuiltinCatalog it accepts file.permissions' declared
// undo, still refuses one it does not declare, and admission reads the
// method's capabilities.
func TestInstallBuiltinCatalog_TheControllerTrustsABuiltInsRecordedUndo(t *testing.T) {
	t.Cleanup(collection.CatalogForTest())
	if _, ok := collection.Lookup("file.permissions"); ok {
		t.Fatal("this binary registers file.permissions, so it would not test the catalog")
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\nhosts: h1\ntasks:\n  - name: chmod\n    file.permissions:\n      path: /etc/motd\n      mode: \"0644\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	recorded := rollback.Step{Node: "tasks[0]", Emitter: "file.permissions", FQCN: "file.permissions",
		Params: map[string]any{"path": "/etc/motd", "mode": "0600"}, Source: rollback.SourceRecorded}
	if err := rollback.Verify(dag, recorded); err == nil {
		t.Fatal("a recorded undo was trusted with no catalog, so this proves nothing")
	}

	if err := installBuiltinCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Verify(dag, recorded); err != nil {
		t.Errorf("the recorded undo of a built-in method was refused: %v", err)
	}
	undeclared := recorded
	undeclared.FQCN = "file.remove"
	if err := rollback.Verify(dag, undeclared); err == nil {
		t.Error("an undo file.permissions does not declare was trusted")
	}
	if m, ok := collection.ManifestOf("file.permissions"); !ok || len(m.SupportedTransports) == 0 {
		t.Errorf("admission reads no transports for file.permissions: %+v, %v", m.SupportedTransports, ok)
	}
	if err := installBuiltinCatalog(); err == nil {
		t.Error("a second install was accepted")
	}
}
