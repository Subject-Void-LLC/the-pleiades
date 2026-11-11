// Tests that the Controller, which links no Collection, answers questions
// about built-in methods from the catalog it installs (FAILURE_PATTERNS 841),
// in this binary's own test binary, which links no Collection either.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
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
	entries, _, _ := facts.Prepare(map[string]any{"os.kernel.release": "6.1", "host.uptime_seconds": 5}, facts.Guard{Declared: asVolatile})
	data, err := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Method: "facts.gather"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: data}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 1 || store.got[0].Key != "host.uptime_seconds" {
		t.Errorf("stored %+v, want only the uptime: the kernel is declared stable", store.got)
	}
}

// TestNewFactsSubscriber_KeepsBuiltInFactsFromAMethodOutsideItsCatalog: a
// batch whose method is not in the catalog this binary embeds (a newer
// Runner's built-in) keeps every fact, a declared key and a reserved
// family's alike, and the consumer main builds notes the difference at info
// rather than dropping them (FP 1020): which methods are built-in is the
// Runner's release's to say. It is built here without the node resolver main
// adds; node_resolver_test.go holds the batch to its node's method.
func TestNewFactsSubscriber_KeepsBuiltInFactsFromAMethodOutsideItsCatalog(t *testing.T) {
	store := &recordingStore{}
	var logged bytes.Buffer
	sub, err := newFactsSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := facts.Prepare(map[string]any{"host.name": "web1", "Package.openssh": "9.6", "acme.widgets": 3}, facts.Guard{})
	data, err := json.Marshal(facts.Batch{JobID: "job-1", DeviceID: "dev-1", Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Method: "newer.gather"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Handle(event.Event{ID: "e1", Type: facts.EventType, Data: data}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 3 {
		t.Errorf("stored %+v, want all three facts", store.got)
	}
	if !strings.Contains(logged.String(), "level=INFO") || !strings.Contains(logged.String(), "facts=2") || strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("want one info note counting the two built-in facts and no warning:\n%s", logged.String())
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

// TestBuiltinCatalog_GivesEachGroupTheCatalogsDigest: a Controller links no
// Collection, so each first-party group's identity digest comes from the
// catalog InstallCatalog put in place (Phase 136b). Each must equal the digest
// of the same group taken straight from the embedded catalog's JSON, which
// internal/archtest holds equal to a Runner's registry: so a Controller and a
// Runner of one build agree on every group.
func TestBuiltinCatalog_GivesEachGroupTheCatalogsDigest(t *testing.T) {
	var catalog map[string]collection.Manifest
	if err := json.Unmarshal(wellknown.ModuleCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	byGroup := map[string]map[string]collection.Manifest{}
	for name, m := range catalog {
		g, ok := collection.FirstPartyGroupOf(name)
		if !ok {
			t.Fatalf("%s is in the catalog and in no first-party group", name)
		}
		if byGroup[g.Name] == nil {
			byGroup[g.Name] = map[string]collection.Manifest{}
		}
		byGroup[g.Name][name] = m
	}
	if len(byGroup) == 0 {
		t.Fatal("control: the embedded catalog holds no group")
	}
	for group, manifests := range byGroup {
		got, err := collection.GroupDigest(group)
		if err != nil {
			t.Errorf("group %s: %v", group, err)
			continue
		}
		if want := collection.ManifestSetDigest(manifests); got != want {
			t.Errorf("group %s: the installed catalog digests to %s and its JSON to %s", group, got, want)
		}
	}
}
