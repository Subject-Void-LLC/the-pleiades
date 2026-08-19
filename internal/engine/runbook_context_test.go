package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factReader is the engine-side view of a RunbookContext: everything a
// method emitted. It mirrors engine.FactCollector, redeclared here because
// the test needs to assert on a value the constructor returns as the
// narrower sdk.RunbookContext.
type factReader interface {
	Facts() map[string]interface{}
}

// TestRunbookContext_CollectsFactsAndStats proves both emission paths land
// in the same place. They share storage deliberately: both end up in
// ActionResult.Stats, and keeping two maps would only force the executor to
// merge them back.
func TestRunbookContext_CollectsFactsAndStats(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	if err := rc.EmitFact("os", "IOS-XE"); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}
	if err := rc.SetStat("device_count", 4); err != nil {
		t.Fatalf("SetStat: %v", err)
	}

	facts := rc.(factReader).Facts()
	if facts["os"] != "IOS-XE" {
		t.Errorf("facts[os] = %v, want IOS-XE", facts["os"])
	}
	if facts["device_count"] != 4 {
		t.Errorf("facts[device_count] = %v, want 4", facts["device_count"])
	}
}

// TestRunbookContext_RejectsEmptyKey proves an unusable key is an error
// rather than a silent no-op. A value stored under an empty key is
// something no later when_cel can reference, so accepting it would lose the
// method's output without telling anyone.
func TestRunbookContext_RejectsEmptyKey(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	for _, method := range []struct {
		name string
		call func() error
	}{
		{name: "EmitFact", call: func() error { return rc.EmitFact("", 1) }},
		{name: "SetStat", call: func() error { return rc.SetStat("", 1) }},
	} {
		t.Run(method.name, func(t *testing.T) {
			err := method.call()
			if err == nil {
				t.Fatal("expected an empty key to be rejected")
			}
			if !strings.Contains(err.Error(), "empty key") {
				t.Errorf("error = %q, want it to mention the empty key", err)
			}
		})
	}
}

// TestRunbookContext_SecretsAreCopied proves neither the caller's map nor
// the returned one can reach back into the context's own state. A method
// mutating what InjectSecrets handed it must not change what the next call
// sees.
func TestRunbookContext_SecretsAreCopied(t *testing.T) {
	source := map[string]string{"password": "hunter2"}
	rc := engine.NewRunbookContext(source)

	source["password"] = "changed-after-construction"
	if got := rc.InjectSecrets()["password"]; got != "hunter2" {
		t.Errorf("mutating the source map changed the context: got %q", got)
	}

	returned := rc.InjectSecrets()
	returned["password"] = "mutated-by-caller"
	if got := rc.InjectSecrets()["password"]; got != "hunter2" {
		t.Errorf("mutating the returned map changed the context: got %q", got)
	}
}

// TestRunbookContext_FactsSnapshotIsIndependent proves the same for facts:
// a caller holding an earlier snapshot must not see later writes, or the
// executor could read a map another goroutine is still filling.
func TestRunbookContext_FactsSnapshotIsIndependent(t *testing.T) {
	rc := engine.NewRunbookContext(nil)
	if err := rc.EmitFact("first", 1); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}

	snapshot := rc.(factReader).Facts()
	if err := rc.EmitFact("second", 2); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}

	if _, leaked := snapshot["second"]; leaked {
		t.Error("an earlier snapshot saw a later write")
	}
}

// TestRunbookContext_ConcurrentEmit proves the context is safe for a method
// that emits from more than one goroutine, which nothing in the SDK
// contract forbids. Run with -race, this is what catches an unguarded map.
func TestRunbookContext_ConcurrentEmit(t *testing.T) {
	rc := engine.NewRunbookContext(nil)

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			// Distinct keys per goroutine, so the final count is a real
			// assertion rather than a race on one key's last writer.
			_ = rc.EmitFact(string(rune('a'+n)), n)
		}(i)
	}
	wg.Wait()

	if got := len(rc.(factReader).Facts()); got != writers {
		t.Errorf("recorded %d facts, want %d", got, writers)
	}
}

// TestNewDeviceRunbookContext proves the resolve-nothing
// RunbookContextFunc produces a usable, empty context.
func TestNewDeviceRunbookContext(t *testing.T) {
	rc, err := engine.NewDeviceRunbookContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("NewDeviceRunbookContext: %v", err)
	}
	if rc == nil {
		t.Fatal("NewDeviceRunbookContext returned nil")
	}
	if len(rc.InjectSecrets()) != 0 {
		t.Error("expected no secrets on a context built for a device with none resolved")
	}
	if len(rc.(factReader).Facts()) != 0 {
		t.Error("expected a fresh context to carry no facts")
	}
}

// namedDevice is the minimum inventory item NewCredentialRunbookContext
// needs: it looks a credential up by Name and touches nothing else.
type namedDevice struct {
	*inventorytest.Stub
	name string
}

func (d *namedDevice) Name() string { return d.name }

// newNamedDevice builds a namedDevice as an inventory.InventoryItem.
func newNamedDevice(name string) inventory.InventoryItem {
	return &namedDevice{Stub: &inventorytest.Stub{}, name: name}
}

// failingStore is a credential.Store whose Lookup always fails with
// something other than ErrNotFound, standing in for an unreadable file
// or a wrong master encryption key.
type failingStore struct{ err error }

func (s failingStore) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{}, s.err
}

// TestNewCredentialRunbookContext_ResolvesTheDevicesSecrets proves the
// Walk tier actually hands a Collection method the credential stored for
// the device it is acting on.
//
// This is the gap that made every credential-needing Collection method
// unusable from the CLI: the composition root passed a context func that
// resolved nothing, so net.ssh.ping failed with "no usable
// authentication method" against a device whose credential was on disk
// the whole time. The keys asserted below are the wire.Secret* ones,
// which is what the method reads back out.
func TestNewCredentialRunbookContext_ResolvesTheDevicesSecrets(t *testing.T) {
	store := credential.NewStaticStore(map[string]string{
		wire.SecretUsername: "admin",
		wire.SecretPassword: "hunter2",
	})

	rc, err := engine.NewCredentialRunbookContext(store)(context.Background(), newNamedDevice("core-1"))
	if err != nil {
		t.Fatalf("NewCredentialRunbookContext: %v", err)
	}

	secrets := rc.InjectSecrets()
	if got := secrets[wire.SecretUsername]; got != "admin" {
		t.Errorf("InjectSecrets()[%q] = %q, want %q", wire.SecretUsername, got, "admin")
	}
	if got := secrets[wire.SecretPassword]; got != "hunter2" {
		t.Errorf("InjectSecrets()[%q] = %q, want %q", wire.SecretPassword, got, "hunter2")
	}
}

// TestNewCredentialRunbookContext_MissingCredentialIsNotAFailure proves
// a device with nothing stored produces an empty secret set rather than
// an error.
//
// That distinction is the whole design. A device with no credential is
// ordinary, and the failure belongs at the point a method actually needs
// a secret, where the message can say so. Failing here instead would
// break a runbook whose tasks need no credential at all.
func TestNewCredentialRunbookContext_MissingCredentialIsNotAFailure(t *testing.T) {
	// An empty static store returns ErrNotFound for every name.
	rc, err := engine.NewCredentialRunbookContext(credential.NewStaticStore(nil))(context.Background(), newNamedDevice("core-1"))
	if err != nil {
		t.Fatalf("a device with no stored credential must not be an error, got: %v", err)
	}
	if got := rc.InjectSecrets(); len(got) != 0 {
		t.Errorf("InjectSecrets() = %v, want empty", got)
	}
}

// TestNewCredentialRunbookContext_RealLookupFailureIsReported proves a
// broken credential store is reported rather than degraded into an empty
// secret set.
//
// An unreadable file and an absent entry must not look alike: the first
// is a broken installation an operator has to fix, and reporting it as
// "no usable authentication method" three frames later would send them
// looking at the wrong thing.
func TestNewCredentialRunbookContext_RealLookupFailureIsReported(t *testing.T) {
	store := failingStore{err: errors.New("cipher: message authentication failed")}

	_, err := engine.NewCredentialRunbookContext(store)(context.Background(), newNamedDevice("core-1"))
	if err == nil {
		t.Fatal("expected an unreadable credential store to be reported")
	}
	if !strings.Contains(err.Error(), "core-1") {
		t.Errorf("error = %v, want it to name the device whose credential could not be resolved", err)
	}
	if !strings.Contains(err.Error(), "cipher") {
		t.Errorf("error = %v, want it to carry the underlying cause", err)
	}
}

// TestNewCredentialRunbookContext_NoStoreOrNoDevice covers the two
// degenerate inputs. Neither is an error: a composition root with no
// store configured, and a task with no target at all, both mean "no
// secrets," not "something went wrong."
func TestNewCredentialRunbookContext_NoStoreOrNoDevice(t *testing.T) {
	tests := []struct {
		name   string
		store  credential.Store
		device inventory.InventoryItem
	}{
		{name: "no store", store: nil, device: newNamedDevice("core-1")},
		{name: "no device", store: credential.NewStaticStore(map[string]string{wire.SecretUsername: "admin"}), device: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc, err := engine.NewCredentialRunbookContext(tc.store)(context.Background(), tc.device)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := rc.InjectSecrets(); len(got) != 0 {
				t.Errorf("InjectSecrets() = %v, want empty", got)
			}
		})
	}
}
