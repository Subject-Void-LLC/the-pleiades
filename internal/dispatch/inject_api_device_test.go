// Tests that a template's machine credential never reaches a device reached
// only through an API (Phase 117b, S1).
package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// TestMachineCredentialNeverReachesAnHTTPAPIDevice: a template binds the
// switches' machine credential, and its inventory holds a generic_http
// device. Before S1's fix the device's payload carried that login, which
// http.request sends as basic or bearer auth to whatever answers at the
// device's base URL, and it displaced the device's own stored credential.
// Now the device gets its own credential and never the template's.
func TestMachineCredentialNeverReachesAnHTTPAPIDevice(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()

	props := map[string]pkginventory.PropertyValue{
		generic.BaseURLProperty:  "https://api.example.com/v2",
		generic.HTTPAuthProperty: "bearer",
	}
	found := pkginventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	found.Binding = generic.Binding(generic.TypeHTTP, pkginventory.NewProperties(props))
	props[pkginventory.DiscoveredProperty] = found.Property()
	device, err := generic.NewHTTP(record.Record{
		ID: "api-id", Name: "api1", Type: generic.TypeHTTP, State: pkginventory.StateActive,
		Properties: props,
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.yaml"), []byte("id: api\ntasks:\n  - name: read\n    http.request:\n      url: /interfaces\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}

	credDir := t.TempDir()
	key, err := credential.ResolveMasterKey(credDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(credDir, key, "api1", credential.Credential{Username: "api-user", Password: "the-apis-own-token"}); err != nil {
		t.Fatal(err)
	}

	resolver := &fakeResolver{creds: []credtype.Credential{machineCredential(3, "switches", "netops", "the-switches-password")}}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, credential.NewLazyFileStore(credDir),
		dispatch.WithCredentials(resolver, testInjector(t)))

	job := &dispatch.Job{RunbookID: "api", GroupName: "routers", Actor: "user@example.com", CredentialIDs: []int{3}}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		JobID string `json:"job_id"`
	}{JobID: job.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.HandleJobRequested(event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}); err != nil {
		t.Fatal(err)
	}
	if bus.count() == 0 {
		t.Fatal("the HTTP API device was not dispatched")
	}

	payload := dispatchedPayload(t, bus)
	for k, v := range payload.Secrets {
		if v == "the-switches-password" || v == "netops" {
			t.Errorf("the template's machine credential reached the HTTP API device under %q", k)
		}
	}
	if got := payload.Secrets[credential.SecretPassword]; got != "the-apis-own-token" {
		t.Errorf("Secrets[password] = %q, want the device's own stored credential", got)
	}
}
