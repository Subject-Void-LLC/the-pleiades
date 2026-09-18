// A fixture seeding one row into every encrypted column, shared by the rotation
// and census tests.
package crypto_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// A fixture that seeds one row into every column this package encrypts,
// through the real hooks, on a real on-disk SQLite file. Rotation and the
// census both need "a database where every encrypted column holds
// something", and building it once keeps the two proving the same thing.

// everyColumnSecret is the plaintext each seeded row carries, distinct per
// column so a row that decrypted into the wrong one would show.
var everyColumnSecret = map[string]string{
	"credentials":                 "credential-secret-value",
	"devices":                     "device-secret-value",
	"saved launch configurations": "survey-secret-value",
	"mesh signing keys":           "SAMESHSEEDFORTESTINGONLYNOTAREALKEY",
}

// everyColumnIDs holds the id of each seeded row.
type everyColumnIDs struct {
	credential, device, launchConfig, meshKey int
}

// installEveryHook wires all four hook and interceptor pairs, the same set
// cmd/controller's installCryptoHooks registers.
func installEveryHook(client *ent.Client, svc *crypto.EnvelopeService) {
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	client.MeshSigningKey.Use(crypto.MeshSigningKeySeedHook(svc))
	client.MeshSigningKey.Intercept(crypto.MeshSigningKeySeedInterceptor(svc))
}

// openEveryColumn opens dbPath with every hook installed under svc.
func openEveryColumn(t *testing.T, dbPath string, svc *crypto.EnvelopeService) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	installEveryHook(client, svc)
	return client
}

// seedEveryColumn writes one row into each encrypted column under svc and
// returns the database path and the rows' ids.
func seedEveryColumn(t *testing.T, svc *crypto.EnvelopeService) (string, everyColumnIDs) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "every_column.db")

	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	installEveryHook(client, svc)

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().
		SetName("Custom API").SetKind("cloud").SetNamespace("custom_api").
		SetOrganization(org).SaveX(ctx)
	inv := client.Inventory.Create().SetName("all").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("some-runbook").
		SetOrganization(org).SetInventory(inv).
		SaveX(ctx)

	var ids everyColumnIDs
	ids.credential = client.Credential.Create().
		SetName("api").SetOrganization(org).SetCredentialType(ct).
		SetInputs(map[string]string{"api_token": everyColumnSecret["credentials"]}).
		SaveX(ctx).ID
	ids.device = client.Device.Create().
		SetName("router-1").SetType("cisco_router").
		SetProperties(map[string]any{"enable_secret": everyColumnSecret["devices"]}).
		SaveX(ctx).ID
	ids.launchConfig = client.SavedLaunchConfig.Create().
		SetName("nightly").SetTemplate(tmpl).
		SetAnswers(map[string]any{"survey_password": everyColumnSecret["saved launch configurations"]}).
		SaveX(ctx).ID
	ids.meshKey = client.MeshSigningKey.Create().
		SetKeyID("key-1").SetAccountSubject("ACCOUNT").SetPublicKey("APUBLICKEY").
		SetSeed(everyColumnSecret["mesh signing keys"]).
		SaveX(ctx).ID
	return dbPath, ids
}

// readEveryColumn reads each seeded row back through client and reports,
// per table, whether it decrypted to the plaintext it was seeded with.
func readEveryColumn(t *testing.T, client *ent.Client, ids everyColumnIDs) map[string]bool {
	t.Helper()
	ctx := context.Background()
	ok := map[string]bool{}

	cred, err := client.Credential.Get(ctx, ids.credential)
	if err != nil {
		t.Fatalf("Credential.Get() error = %v", err)
	}
	ok["credentials"] = cred.Inputs["api_token"] == everyColumnSecret["credentials"]

	dev, err := client.Device.Get(ctx, ids.device)
	if err != nil {
		t.Fatalf("Device.Get() error = %v", err)
	}
	ok["devices"] = dev.Properties["enable_secret"] == everyColumnSecret["devices"]

	cfg, err := client.SavedLaunchConfig.Get(ctx, ids.launchConfig)
	if err != nil {
		t.Fatalf("SavedLaunchConfig.Get() error = %v", err)
	}
	ok["saved launch configurations"] = cfg.Answers["survey_password"] == everyColumnSecret["saved launch configurations"]

	key, err := client.MeshSigningKey.Get(ctx, ids.meshKey)
	if err != nil {
		t.Fatalf("MeshSigningKey.Get() error = %v", err)
	}
	ok["mesh signing keys"] = key.Seed == everyColumnSecret["mesh signing keys"]

	return ok
}
