// Tests that the Controller, which links no Collection, answers questions
// about built-in methods from the catalog it installs (FAILURE_PATTERNS 841),
// in this binary's own test binary, which links no Collection either.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestInstallBuiltinCatalog_TheControllerTrustsABuiltInsRecordedUndo: this
// binary's init installed the catalog; without it the binary knows no
// built-in method, and the rollback route refused every recorded undo as
// "not a built-in method this build runs"; with it, it accepts
// file.permissions' declared undo, and a list value a pkg.apt.install undo
// records, still refuses one it does not declare, and admission reads the
// method's capabilities.
func TestInstallBuiltinCatalog_TheControllerTrustsABuiltInsRecordedUndo(t *testing.T) {
	// The package's init installed the catalog, as it does for the server:
	// this binary must know file.permissions before the test does anything.
	if _, ok := collection.BuiltinManifest("file.permissions"); !ok {
		t.Fatal("this binary's init installed no built-in catalog")
	}
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
	// A recorded undo with a list value (engine.ListParam asks the catalog
	// which parameters are lists): five built-in undos record one.
	apt, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\nhosts: h1\ntasks:\n  - name: install\n    pkg.apt.install:\n      name: nginx\n"))
	if err != nil {
		t.Fatal(err)
	}
	listed := rollback.Step{Node: "tasks[0]", Emitter: "pkg.apt.install", FQCN: "pkg.apt.remove",
		Params: map[string]any{"name": "nginx", "dependencies": []any{"libnginx", "nginx-common"}}, Source: rollback.SourceRecorded}
	if err := rollback.Verify(apt, listed); err != nil {
		t.Errorf("a recorded undo with a list value was refused: %v", err)
	}
	if m, ok := collection.ManifestOf("file.permissions"); !ok || len(m.SupportedTransports) == 0 {
		t.Errorf("admission reads no transports for file.permissions: %+v, %v", m.SupportedTransports, ok)
	}
	if err := installBuiltinCatalog(); err == nil {
		t.Error("a second install was accepted")
	}
}

// recordingStore is a facts.Store that keeps what it was asked to record.
type recordingStore struct {
	facts.Store
	got []facts.Entry
}

func (s *recordingStore) Record(_ context.Context, _ string, obs facts.Observation) error {
	s.got = append(s.got, obs.Entries...)
	return nil
}

// TestNewFactsSubscriber_HoldsBatchesToTheBuiltInDeclarations: the facts
// consumer main builds refuses a built-in fact a Runner claims under
// another kind, as the declarations it embeds say (FAILURE_PATTERNS 837).
func TestNewFactsSubscriber_HoldsBatchesToTheBuiltInDeclarations(t *testing.T) {
	store := &recordingStore{}
	sub, err := newFactsSubscriber(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	asVolatile := func(string) (facts.Spec, bool) { return facts.Spec{Kind: facts.KindVolatile}, true }
	entries, _, _ := facts.Prepare(map[string]any{"ansible_kernel": "6.1", "ansible_uptime_seconds": 5}, facts.Guard{Declared: asVolatile})
	data, err := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: data}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 1 || store.got[0].Key != "ansible_uptime_seconds" {
		t.Errorf("stored %+v, want only the uptime: the kernel is declared stable", store.got)
	}
}

// TestTheControllerBuildsARunbookAsTheRunnerWill: the runbook builder asks
// whether a handler's method acts on a device (engine.TaskNeedsDevice); in
// this binary, which registers no method, it reads the installed catalog, so
// a handler that would never run is refused here, as the Runner refuses it,
// rather than dispatched to fail on every device (review of 841).
func TestTheControllerBuildsARunbookAsTheRunnerWill(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	const runbook = "id: notify\nhosts: h1\ntasks:\n  - name: change\n    file.permissions:\n      path: /etc/motd\n      mode: \"0644\"\n    notify:\n      - summarize\nhandlers:\n  - name: summarize\n    net.pcap.summary:\n      src: capture.pcap\n"
	if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook)); err == nil || !strings.Contains(err.Error(), "acts on no device") {
		t.Fatalf("a handler acting on no device: %v, want it refused", err)
	}
}
