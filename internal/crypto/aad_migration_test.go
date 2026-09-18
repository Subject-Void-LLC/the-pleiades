package crypto_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// Phase 78c's Release Gate: migrating Device.properties from the unbound
// envelope to the bound one, on a database that holds both at once.
//
// internal/crypto/envelope_bound.go recorded this as a known residual in
// Phase 22 and said why it was deferred: it needs a rotation pass over live
// encrypted data, which is its own piece of work with its own failure
// modes. This file is where those failure modes are held down.
//
// The gate is written to be falsifiable in BOTH directions, because the
// property being added is a negative one and a test that only shows the
// attack failing afterwards cannot distinguish "the binding works" from
// "the relocation was set up wrong". So the same relocation is attempted
// twice against the same rows: once before the migration, where it must
// SUCCEED, and once after, where it must fail. Without the first half the
// second proves nothing.

const (
	aadKey    = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	aadSecret = "a-real-device-password"
)

// legacyDeviceFixture builds a database holding two devices whose
// properties are in the pre-Phase-78c UNBOUND form, and one in the bound
// form, which is what a real deployment looks like partway through an
// upgrade.
func legacyDeviceFixture(t *testing.T) (string, *crypto.EnvelopeService) {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "aad_migration_test.db")

	svc, err := crypto.NewEnvelopeService([]byte(aadKey), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}

	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))

	for _, name := range []string{"legacy-a", "legacy-b", "modern"} {
		client.Device.Create().
			SetName(name).SetType("linux_server").SetDeviceID(name).
			SetProperties(map[string]interface{}{"password": aadSecret + "-" + name}).
			SaveX(ctx)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Rewrite the two legacy rows the way Phase 22 would have written them:
	// an UNBOUND envelope, and no secret_binding at all. Done at the SQL
	// layer because no code path in this package can produce that shape
	// any more, which is itself the point.
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	for _, name := range []string{"legacy-a", "legacy-b"} {
		plaintext, err := json.Marshal(map[string]interface{}{"password": aadSecret + "-" + name})
		if err != nil {
			t.Fatalf("marshalling the legacy plaintext: %v", err)
		}
		unbound, err := svc.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		stored, err := json.Marshal(map[string]string{crypto.EncryptedKeyMarker: unbound})
		if err != nil {
			t.Fatalf("marshalling the legacy row: %v", err)
		}
		if _, err := rawDB.ExecContext(ctx,
			"UPDATE devices SET properties = ?, secret_binding = '' WHERE name = ?",
			string(stored), name); err != nil {
			t.Fatalf("writing the legacy row for %s: %v", name, err)
		}
	}

	return dbPath, svc
}

// openDevices returns a client with the hooks installed.
func openDevices(t *testing.T, dbPath string, svc *crypto.EnvelopeService) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))
	return client
}

// deviceProperties reads a device's raw stored properties.
func deviceProperties(t *testing.T, dbPath, name string) string {
	t.Helper()
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	var raw string
	if err := rawDB.QueryRowContext(context.Background(),
		"SELECT properties FROM devices WHERE name = ?", name).Scan(&raw); err != nil {
		t.Fatalf("reading properties for %s: %v", name, err)
	}
	return raw
}

// relocate copies one device's stored ciphertext onto another, which is the
// attack the bound envelope exists to defeat: a database writer who can
// change a column without being able to read it.
func relocate(t *testing.T, dbPath, from, to string) {
	t.Helper()
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	if _, err := rawDB.ExecContext(context.Background(),
		"UPDATE devices SET properties = (SELECT properties FROM devices WHERE name = ?) WHERE name = ?",
		from, to); err != nil {
		t.Fatalf("relocating %s onto %s: %v", from, to, err)
	}
}

// propertiesOf reads a device's decrypted properties through the real
// interceptor.
func propertiesOf(t *testing.T, client *ent.Client, name string) map[string]interface{} {
	t.Helper()
	rows, err := client.Device.Query().All(context.Background())
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	for _, row := range rows {
		if row.Name == name {
			return row.Properties
		}
	}
	t.Fatalf("no device named %s", name)
	return nil
}

// TestReleaseGate_TheAADMigrationClosesTheRelocationHole is Phase 78c's
// gate, in three acts.
func TestReleaseGate_TheAADMigrationClosesTheRelocationHole(t *testing.T) {
	ctx := context.Background()
	dbPath, svc := legacyDeviceFixture(t)

	// Act one: a database holding both forms reads correctly. This is the
	// migration window's whole requirement, and getting it wrong would
	// make an upgrade lose every pre-existing device's connection details.
	client := openDevices(t, dbPath, svc)
	for _, name := range []string{"legacy-a", "legacy-b", "modern"} {
		got := propertiesOf(t, client, name)
		if want := aadSecret + "-" + name; got["password"] != want {
			t.Fatalf("%s reads back %v before migration, want %q", name, got, want)
		}
	}
	// And they really are in different forms, or act one proves nothing.
	if crypto.IsBoundEnvelope(unwrap(t, deviceProperties(t, dbPath, "legacy-a"))) {
		t.Fatal("the legacy fixture is not actually unbound")
	}
	if !crypto.IsBoundEnvelope(unwrap(t, deviceProperties(t, dbPath, "modern"))) {
		t.Fatal("the modern fixture is not actually bound")
	}

	// Act two: BEFORE migrating, the relocation attack succeeds. This is
	// the half that makes act three meaningful rather than vacuous.
	relocate(t, dbPath, "legacy-a", "legacy-b")
	if got := propertiesOf(t, client, "legacy-b"); got["password"] != aadSecret+"-legacy-a" {
		t.Fatalf("the relocation did not succeed before migration, so this gate cannot prove it fails after: %v", got)
	}

	// Put legacy-b back, so act three migrates a healthy database.
	dbPath2, svc2 := legacyDeviceFixture(t)
	client2 := openDevices(t, dbPath2, svc2)

	// Act three: migrate, then attempt the identical relocation.
	rotated, err := crypto.RotateDeviceProperties(ctx, client2, svc2)
	if err != nil {
		t.Fatalf("RotateDeviceProperties() error = %v", err)
	}
	if rotated.Rotated != 3 {
		t.Fatalf("RotateDeviceProperties() converted %d rows, want all three", rotated.Rotated)
	}

	// Every row is now bound, and every row still decrypts.
	for _, name := range []string{"legacy-a", "legacy-b", "modern"} {
		raw := deviceProperties(t, dbPath2, name)
		if !crypto.IsBoundEnvelope(unwrap(t, raw)) {
			t.Errorf("%s is still unbound after migration: %s", name, raw)
		}
		if got := propertiesOf(t, client2, name); got["password"] != aadSecret+"-"+name {
			t.Errorf("%s reads back %v after migration, want its own value", name, got)
		}
	}

	// The identical attack now fails, and the row is left sealed rather
	// than opened as somebody else's device.
	relocate(t, dbPath2, "legacy-a", "legacy-b")
	got := propertiesOf(t, client2, "legacy-b")
	if got["password"] == aadSecret+"-legacy-a" {
		t.Fatal("a relocated ciphertext still decrypts after migration: the binding is not doing anything")
	}
	if _, sealed := got[crypto.EncryptedKeyMarker]; !sealed {
		t.Errorf("the relocated row = %v, want it left sealed", got)
	}
}

// TestTheMigrationIsIdempotent covers the operator who runs the pass twice,
// which is the likely shape of a real rotation: run it, check the count,
// run it again to be sure.
func TestTheMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath, svc := legacyDeviceFixture(t)
	client := openDevices(t, dbPath, svc)

	first, err := crypto.RotateDeviceProperties(ctx, client, svc)
	if err != nil {
		t.Fatalf("first pass error = %v", err)
	}
	second, err := crypto.RotateDeviceProperties(ctx, client, svc)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if first.Rotated != 3 || second.Rotated != 3 {
		t.Fatalf("passes converted %d then %d rows, want three each: every decryptable row is re-encrypted unconditionally", first.Rotated, second.Rotated)
	}

	for _, name := range []string{"legacy-a", "legacy-b", "modern"} {
		if got := propertiesOf(t, client, name); got["password"] != aadSecret+"-"+name {
			t.Errorf("%s reads back %v after two passes, want its own value", name, got)
		}
	}
}

// unwrap pulls the envelope string out of a stored properties document.
func unwrap(t *testing.T, raw string) string {
	t.Helper()
	var stored map[string]string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("the stored properties are not a marker document: %s", raw)
	}
	value, ok := stored[crypto.EncryptedKeyMarker]
	if !ok {
		t.Fatalf("the stored properties carry no marker: %s", raw)
	}
	if !strings.Contains(value, "$") {
		t.Fatalf("the stored value is not an envelope: %s", value)
	}
	return value
}
