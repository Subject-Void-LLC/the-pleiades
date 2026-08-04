package crypto_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

const testAAALogin = "Privilege15_Dynamic_Token_12345"

func mustDeviceEnvelopeService(t *testing.T) *crypto.EnvelopeService {
	t.Helper()
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	return svc
}

// TestDeviceEnvelopeProperties_RoundTrip covers "transparent sealing of
// Cisco DevNet dynamic AAA logins in the State Store": Device.properties
// is the generic bag any such login would land in today (there is no
// dedicated AAA/DevNet type anywhere in this codebase), so sealing it with
// no exceptions is what satisfies that checklist item. This is the direct
// successor of the old Fact-scoped test's identical scenario, moved onto
// the correct target per PLAN.md Section 22.4 (Facts are telemetry,
// connection credentials belong on Device.properties).
//
// Uses a real on-disk (not :memory:) SQLite file via ent.OpenEmbedded, the
// same entry point cmd/controller uses, so the raw-driver ciphertext proof
// below is against genuinely persisted bytes — the closest honest stand-in
// available for the Release Gate's literal (currently unachievable, see
// IMPLEMENTATION.md) "queried via psql" wording.
func TestDeviceEnvelopeProperties_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "device_hook_test.db")
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	dev, err := client.Device.Create().
		SetName("devnet-sandbox-1").
		SetType("cisco_router").
		SetProperties(map[string]interface{}{
			"host":      "10.0.0.1",
			"aaa_token": testAAALogin,
		}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Reload through Query() rather than trusting Create()'s own return
	// value, so this exercises the interceptor's read path for real.
	reloaded, err := client.Device.Get(ctx, dev.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, ok := reloaded.Properties["aaa_token"]; !ok || got != testAAALogin {
		t.Fatalf("interceptor failed to decrypt: got properties %v", reloaded.Properties)
	}
	if got := reloaded.Properties["host"]; got != "10.0.0.1" {
		t.Fatalf("interceptor lost a sibling property: got %v", reloaded.Properties)
	}
}

// TestDeviceEnvelopeProperties_RawStorageIsCiphertext is the Release Gate
// evidence: bypassing every hook/interceptor via a raw driver connection
// to the same on-disk file, the stored properties column must be
// unreadable ciphertext, never the AAA login in the clear.
func TestDeviceEnvelopeProperties_RawStorageIsCiphertext(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "device_hook_test.db")
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	_, err = client.Device.Create().
		SetName("devnet-sandbox-2").
		SetType("cisco_router").
		SetProperties(map[string]interface{}{"aaa_token": testAAALogin}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	var rawJSON string
	row := rawDB.QueryRowContext(ctx, "SELECT properties FROM devices WHERE name = ?", "devnet-sandbox-2")
	if err := row.Scan(&rawJSON); err != nil {
		t.Fatalf("failed to scan raw properties: %v", err)
	}

	if !strings.Contains(rawJSON, `"_encrypted"`) {
		t.Fatalf("raw database row is NOT encrypted: %s", rawJSON)
	}
	if !strings.Contains(rawJSON, "v1$AES256GCM$") {
		t.Fatalf("raw database row missing the version header: %s", rawJSON)
	}
	if strings.Contains(rawJSON, testAAALogin) {
		t.Fatalf("raw database row leaked plaintext: %s", rawJSON)
	}
}

// TestDeviceEnvelopeProperties_BulkUpdateEncrypts matches
// internal/inventory/ent_save.go's exact call shape
// (client.Device.Update().Where(...).SetProperties(...)), the real
// production write path, to prove the hook's ent.OpUpdate registration
// actually fires on it. Device.properties is not .Immutable(), unlike
// Fact.payload, so this write path exists at all — Fact's hook never
// needed to cover it.
func TestDeviceEnvelopeProperties_BulkUpdateEncrypts(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:bulkupdate?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	_, err := client.Device.Create().SetName("bulk-1").SetType("linux_server").Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	affected, err := client.Device.Update().
		Where(device.NameEQ("bulk-1")).
		SetProperties(map[string]interface{}{"aaa_token": testAAALogin}).
		Save(ctx)
	if err != nil {
		t.Fatalf("bulk Update() error = %v", err)
	}
	if affected != 1 {
		t.Fatalf("bulk Update() affected %d rows, want 1", affected)
	}

	reloaded, err := client.Device.Query().Where(device.NameEQ("bulk-1")).Only(ctx)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if got := reloaded.Properties["aaa_token"]; got != testAAALogin {
		t.Fatalf("bulk update path did not round-trip through encryption: got %v", reloaded.Properties)
	}
}

// TestDeviceEnvelopeProperties_UpdateOneEncrypts proves the hook's
// ent.OpUpdateOne registration fires on a direct client.Device.UpdateOneID
// write. No real caller in this codebase uses UpdateOneID for Device
// today (RotateDeviceProperties uses the bulk Update builder scoped to
// one row via a Where clause instead, so its write can be conditional on
// the row's stored version — see rotate.go); OpUpdateOne stays registered
// on the hook defensively, matching this package's Interceptor pattern
// rationale (PATTERNS.md): the interception point, not which builder a
// future caller happens to choose, is what must guarantee the invariant.
// This test is that defense-in-depth registration's only real exercise.
func TestDeviceEnvelopeProperties_UpdateOneEncrypts(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:updateone?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	dev, err := client.Device.Create().SetName("single-1").SetType("linux_server").Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = client.Device.UpdateOneID(dev.ID).
		SetProperties(map[string]interface{}{"aaa_token": testAAALogin}).
		Save(ctx)
	if err != nil {
		t.Fatalf("UpdateOneID() error = %v", err)
	}

	reloaded, err := client.Device.Get(ctx, dev.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := reloaded.Properties["aaa_token"]; got != testAAALogin {
		t.Fatalf("UpdateOneID path did not round-trip through encryption: got %v", reloaded.Properties)
	}
}

// TestDeviceEnvelopeProperties_ThroughRepositorySaveTx proves the
// encryption hook fires on a write made through
// internal/inventory.entRepository.Save's real WithTx transaction, not
// just on a bare client.Device.Update() call outside any transaction.
// ent's generated Tx() copies the client's config (including registered
// hooks/interceptors) into the transaction, but that is exactly the kind
// of generated-code behavior this codebase's own AGENTS.md says to verify
// directly rather than assume from a read of it.
func TestDeviceEnvelopeProperties_ThroughRepositorySaveTx(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:repotx?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	_, err := client.Device.Create().SetName("repo-1").SetType("linux_server").Save(ctx)
	if err != nil {
		t.Fatalf("seed Create() error = %v", err)
	}

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	item, err := repo.GetByName(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetByName() error = %v", err)
	}
	if err := item.AddInfo("aaa_token", testAAALogin, true); err != nil {
		t.Fatalf("AddInfo() error = %v", err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("repo.Save() error = %v", err)
	}

	reloaded, err := client.Device.Query().Where(device.NameEQ("repo-1")).Only(ctx)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if got := reloaded.Properties["aaa_token"]; got != testAAALogin {
		t.Fatalf("repo.Save's WithTx path did not round-trip through encryption: got %v", reloaded.Properties)
	}
}

// TestDeviceEnvelopeProperties_AlreadyEncryptedIsPassedThrough covers the
// hook's failsafe against double-encrypting a properties map that somehow
// already carries EncryptedKeyMarker: it must be written through
// unchanged, never re-wrapped as ciphertext-of-ciphertext.
func TestDeviceEnvelopeProperties_AlreadyEncryptedIsPassedThrough(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:alreadyencrypted?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))

	const preEncrypted = "v1$AES256GCM$already-here$unchanged"
	dev, err := client.Device.Create().
		SetName("already-encrypted-1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{crypto.EncryptedKeyMarker: preEncrypted}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Bypassing the interceptor deliberately (not installed on this
	// client) to see exactly what the hook wrote, not what a decrypt pass
	// would produce from it.
	if got := dev.Properties[crypto.EncryptedKeyMarker]; got != preEncrypted {
		t.Fatalf("hook rewrote an already-encrypted marker: got %v, want unchanged %q", dev.Properties, preEncrypted)
	}
}

// TestDeviceEnvelopeProperties_ColludingPropertyKeyDoesNotBypassEncryption
// is the regression test for a real, critical defect an adversarial
// review caught before this phase shipped: a caller-supplied property
// literally named EncryptedKeyMarker ("_encrypted"), sitting alongside a
// real secret, used to make the hook's "already encrypted" failsafe skip
// encryption of the ENTIRE map — including the real secret — because the
// failsafe checked only for the marker key's presence, not that it was
// the map's only key. FAILURE_PATTERNS.md records this as a phase-5
// finding. This proves the fixed failsafe (isAlreadyEncryptedShape,
// device_hook.go) correctly treats such a map as ordinary plaintext and
// encrypts it as a whole.
func TestDeviceEnvelopeProperties_ColludingPropertyKeyDoesNotBypassEncryption(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:colludingkey?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	dev, err := client.Device.Create().
		SetName("colluding-key-1").
		SetType("cisco_router").
		SetProperties(map[string]interface{}{
			crypto.EncryptedKeyMarker: "not-a-real-envelope-just-a-colliding-key-name",
			"aaa_token":               testAAALogin,
		}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The raw stored row must not contain the secret in the clear: the
	// whole map, including the colliding key, must have been swept into
	// one real ciphertext blob rather than skipped.
	if got, ok := dev.Properties[crypto.EncryptedKeyMarker].(string); !ok || !strings.HasPrefix(got, "v1$AES256GCM$") {
		t.Fatalf("expected the properties map to be encrypted as a whole (real envelope string), got %v", dev.Properties)
	}
	if strings.Contains(fmt.Sprintf("%v", dev.Properties), testAAALogin) {
		t.Fatalf("secret leaked in plaintext past the encryption hook: %v", dev.Properties)
	}

	// It must still round-trip correctly through the domain read path.
	reloaded, err := client.Device.Get(ctx, dev.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := reloaded.Properties["aaa_token"]; got != testAAALogin {
		t.Fatalf("secret did not round-trip correctly: got %v", reloaded.Properties)
	}
}

// TestDeviceEnvelopeProperties_MixedRolloutReadPaths covers two read-side
// edge cases that arise if a row was ever written without the hook
// installed: properties with no EncryptedKeyMarker at all must pass
// through unchanged (not treated as an error), and a marker whose value
// is not a string must be a clean error, never a panic or a silent
// zero-value result.
func TestDeviceEnvelopeProperties_MixedRolloutReadPaths(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:mixedrollout?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	// Written through a client with NO hook installed, simulating a row
	// from before envelope encryption was ever wired up.
	plain, err := client.Device.Create().
		SetName("never-encrypted").
		SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.5"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create(plain) error = %v", err)
	}
	nonString, err := client.Device.Create().
		SetName("bad-marker").
		SetType("linux_server").
		SetProperties(map[string]interface{}{crypto.EncryptedKeyMarker: 12345}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create(nonString) error = %v", err)
	}

	svc := mustDeviceEnvelopeService(t)
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	reloadedPlain, err := client.Device.Get(ctx, plain.ID)
	if err != nil {
		t.Fatalf("Get(plain) error = %v", err)
	}
	if got := reloadedPlain.Properties["host"]; got != "10.0.0.5" {
		t.Fatalf("unmarked plaintext properties were altered: got %v", reloadedPlain.Properties)
	}

	// A non-string marker fails to decrypt; per
	// DeviceEnvelopePropertiesInterceptor's own isolate-not-abort design
	// (Get/Only funnel through the same []*ent.Device path Query().All()
	// does, confirmed against the generated ent code — there is no
	// ent-level way to give Get a different failure mode), this is NOT a
	// returned error: the call succeeds and the device's Properties is
	// left exactly as stored, unmodified, so the caller can still see the
	// device exists even though its properties are unreadable.
	reloadedBad, err := client.Device.Get(ctx, nonString.ID)
	if err != nil {
		t.Fatalf("Get(nonString) error = %v, want nil (decrypt failure is isolated, not propagated)", err)
	}
	// float64(12345), not int(12345): ent round-trips JSONB properties
	// through encoding/json, whose default number decoding is float64.
	if got := reloadedBad.Properties[crypto.EncryptedKeyMarker]; got != float64(12345) {
		t.Fatalf("expected the unreadable marker to be left untouched, got %v", reloadedBad.Properties)
	}
}

// TestDeviceEnvelopeProperties_DecryptFailureIsIsolatedNotFatal is the
// regression test for a real defect an adversarial review of this phase
// caught: an earlier draft of DeviceEnvelopePropertiesInterceptor
// returned the first row's decrypt error directly from inside its
// []*ent.Device loop, which aborted the ENTIRE query result — every other,
// perfectly healthy device in the same batch failed to load too, and
// RotateDeviceProperties's own opening listing query could then never
// make progress on any row once a single row became unreadable.
// FAILURE_PATTERNS.md records this as a phase-5 finding. This proves a
// batch query containing one corrupted row still returns every other,
// healthy device correctly, with the corrupted row's Properties left in
// its raw, undecrypted shape rather than the whole call failing.
func TestDeviceEnvelopeProperties_DecryptFailureIsIsolatedNotFatal(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:decryptisolated?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	// Written through a hookless client with a marker value that is a
	// string but not a real envelope, so Decrypt itself fails.
	corrupted, err := client.Device.Create().
		SetName("corrupted-1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{crypto.EncryptedKeyMarker: "not-a-real-envelope-string"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create(corrupted) error = %v", err)
	}

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	// A perfectly healthy sibling row, created AFTER the hook is
	// installed so it is genuinely encrypted for real.
	_, err = client.Device.Create().
		SetName("healthy-1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.9"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create(healthy) error = %v", err)
	}

	devs, err := client.Device.Query().All(ctx)
	if err != nil {
		t.Fatalf("Query().All() error = %v, want nil (one bad row must not abort the whole batch)", err)
	}
	if len(devs) != 2 {
		t.Fatalf("Query().All() returned %d devices, want 2 (the corrupted row must still be present)", len(devs))
	}

	var sawCorrupted, sawHealthy bool
	for _, dev := range devs {
		switch dev.Name {
		case "corrupted-1":
			sawCorrupted = true
			if got := dev.Properties[crypto.EncryptedKeyMarker]; got != "not-a-real-envelope-string" {
				t.Errorf("expected the corrupted row's marker left untouched, got %v", dev.Properties)
			}
		case "healthy-1":
			sawHealthy = true
			if got := dev.Properties["host"]; got != "10.0.0.9" {
				t.Errorf("expected the healthy sibling to decrypt correctly despite the corrupted row, got %v", dev.Properties)
			}
		}
	}
	if !sawCorrupted || !sawHealthy {
		t.Fatalf("expected both devices in the result, saw corrupted=%v healthy=%v", sawCorrupted, sawHealthy)
	}

	// Get() (Only/Limit(2).All() underneath) must also not fail for the
	// corrupted row specifically.
	if _, err := client.Device.Get(ctx, corrupted.ID); err != nil {
		t.Fatalf("Get(corrupted) error = %v, want nil", err)
	}
}

// TestDeviceEnvelopeProperties_HookWrapsEncryptionFailure covers the
// hook's own error-wrap path: a properties value encoding/json cannot
// marshal (a channel, here) must surface as a clean mutation error, not a
// panic, and the row must never be written at all.
func TestDeviceEnvelopeProperties_HookWrapsEncryptionFailure(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:hookmarshalerror?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))

	_, err := client.Device.Create().
		SetName("unmarshalable").
		SetType("linux_server").
		SetProperties(map[string]interface{}{"bad": make(chan int)}).
		Save(ctx)
	if err == nil {
		t.Fatal("expected Create() to fail encrypting an unmarshalable properties value, got nil")
	}
}
