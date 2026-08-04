package crypto_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// TestRotateDeviceProperties is RotateDeviceProperties's real caller and
// proof, per this project's own "a port with no callers is not an
// implemented pattern" bar: a real on-disk SQLite file, seeded under an
// old key/version through one *ent.Client, rotated through a second
// *ent.Client configured with a new current key and the old key as its
// previous slot (mirroring a real operator moving the old
// MASTER_ENCRYPTION_KEY into MASTER_ENCRYPTION_KEY_PREVIOUS and setting a
// new current one), then verified both at the storage layer (raw rows now
// carry the new version tag) and the domain layer (every row still
// decrypts correctly).
//
// Rotation is modeled as two separate *ent.Client connections rather than
// swapping hooks on one live client: ent hooks/interceptors registered via
// Use/Intercept are additive, not replaceable, so re-registering a second
// service on the same client would run both against every mutation
// instead of switching keys. Two connections against the same on-disk
// file is also the more realistic shape of a real rotation, which happens
// across a config change and (at minimum) a reconnect, never inside one
// still-running client with mutable key state.
func TestRotateDeviceProperties(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rotate_test.db")

	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))

	svcOld, err := crypto.NewEnvelopeService(oldKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(old) error = %v", err)
	}

	clientOld, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	clientOld.Device.Use(crypto.DeviceEnvelopePropertiesHook(svcOld))
	clientOld.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svcOld))

	names := []string{"rotate-1", "rotate-2", "rotate-3"}
	for i, name := range names {
		_, err := clientOld.Device.Create().
			SetName(name).
			SetType("cisco_router").
			SetProperties(map[string]interface{}{"aaa_token": testAAALogin, "index": i}).
			Save(ctx)
		if err != nil {
			t.Fatalf("seed Create(%s) error = %v", name, err)
		}
	}
	// A device with no properties at all must be skipped cleanly, not
	// error the whole rotation.
	if _, err := clientOld.Device.Create().SetName("no-properties").SetType("linux_server").Save(ctx); err != nil {
		t.Fatalf("seed Create(no-properties) error = %v", err)
	}
	if err := clientOld.Close(); err != nil {
		t.Fatalf("failed to close old client: %v", err)
	}

	svcNew, err := crypto.NewEnvelopeService(newKey, "v2", oldKey, "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService(new) error = %v", err)
	}

	clientNew, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() (reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = clientNew.Close() })
	clientNew.Device.Use(crypto.DeviceEnvelopePropertiesHook(svcNew))
	clientNew.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svcNew))

	rotated, err := crypto.RotateDeviceProperties(ctx, clientNew, svcNew)
	if err != nil {
		t.Fatalf("RotateDeviceProperties() error = %v", err)
	}
	if rotated != len(names) {
		t.Fatalf("RotateDeviceProperties() rotated = %d, want %d (the no-properties row must be skipped)", rotated, len(names))
	}

	// Storage-layer proof: every rotated row's raw ciphertext now carries
	// the new version tag, not the old one.
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	for _, name := range names {
		var rawJSON string
		row := rawDB.QueryRowContext(ctx, "SELECT properties FROM devices WHERE name = ?", name)
		if err := row.Scan(&rawJSON); err != nil {
			t.Fatalf("failed to scan raw properties for %s: %v", name, err)
		}
		if !strings.Contains(rawJSON, "v2$AES256GCM$") {
			t.Errorf("device %s raw properties still on old version after rotation: %s", name, rawJSON)
		}
		if strings.Contains(rawJSON, testAAALogin) {
			t.Errorf("device %s raw properties leaked plaintext after rotation: %s", name, rawJSON)
		}
	}

	// Domain-layer proof: every row still decrypts correctly through the
	// new service.
	devs, err := clientNew.Device.Query().All(ctx)
	if err != nil {
		t.Fatalf("Query().All() error = %v", err)
	}
	byName := make(map[string]*ent.Device, len(devs))
	for _, dev := range devs {
		byName[dev.Name] = dev
	}
	for _, name := range names {
		dev, ok := byName[name]
		if !ok {
			t.Errorf("device %s not found after rotation", name)
			continue
		}
		if got := dev.Properties["aaa_token"]; got != testAAALogin {
			t.Errorf("device %s did not decrypt correctly after rotation: got %v", name, dev.Properties)
		}
	}
}

// TestRotateDeviceProperties_QueryError proves a failure listing devices
// (here, a closed client) is a returned error, not a panic.
func TestRotateDeviceProperties_QueryError(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:rotatequeryerror?mode=memory&cache=shared&_fk=1")

	svc := mustDeviceEnvelopeService(t)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := crypto.RotateDeviceProperties(ctx, client, svc); err == nil {
		t.Fatal("expected an error rotating against a closed client, got nil")
	}
}

// TestRotateDeviceProperties_SkipsUndecryptableRow proves a row left in
// its raw, still-encrypted shape by DeviceEnvelopePropertiesInterceptor
// (because no key svc knows can decrypt it) is skipped rather than
// causing RotateDeviceProperties to error or to attempt a pointless
// no-op write, while a healthy sibling row in the same pass still gets
// rotated normally.
func TestRotateDeviceProperties_SkipsUndecryptableRow(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:rotateundecryptable?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	// Written through a hookless client, so its marker value is not a
	// real envelope under any key.
	_, err := client.Device.Create().
		SetName("undecryptable-1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{crypto.EncryptedKeyMarker: "not-a-real-envelope"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("Create(undecryptable) error = %v", err)
	}

	svc := mustDeviceEnvelopeService(t)
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	if _, err := client.Device.Create().
		SetName("healthy-1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.9"}).
		Save(ctx); err != nil {
		t.Fatalf("Create(healthy) error = %v", err)
	}

	rotated, err := crypto.RotateDeviceProperties(ctx, client, svc)
	if err != nil {
		t.Fatalf("RotateDeviceProperties() error = %v, want nil (an undecryptable row must be skipped, not fatal)", err)
	}
	if rotated != 1 {
		t.Fatalf("RotateDeviceProperties() rotated = %d, want 1 (only the healthy row)", rotated)
	}

	devs, err := client.Device.Query().All(ctx)
	if err != nil {
		t.Fatalf("Query().All() error = %v", err)
	}
	for _, dev := range devs {
		if dev.Name == "undecryptable-1" {
			if got := dev.Properties[crypto.EncryptedKeyMarker]; got != "not-a-real-envelope" {
				t.Errorf("expected the undecryptable row left untouched, got %v", dev.Properties)
			}
		}
	}
}
