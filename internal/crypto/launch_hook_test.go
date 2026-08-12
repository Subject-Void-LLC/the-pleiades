package crypto_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers envelope encryption of SavedLaunchConfig.answers.
//
// Survey answers need it because a survey is the one path by which a
// launching operator supplies a value the template author did not write,
// and AWX's own survey types include `password`. A saved configuration
// therefore holds credentials: a vault token, a sudo password, an API key
// somebody typed into a launch form and asked the platform to remember.
//
// The assertions read the raw bytes back through a second, plain
// database/sql handle rather than through ent, because ent's own read path
// is the thing that decrypts. Asserting through it would prove only that a
// round trip round-trips, which is true of no encryption at all.

// testVaultToken is the credential this file follows through storage. It is
// distinctive enough that a substring search for it in raw bytes is a real
// test rather than a coincidence.
const testVaultToken = "hvs.CAESIJ-not-a-real-vault-token-abcdef123456"

func mustLaunchEnvelopeService(t *testing.T) *crypto.EnvelopeService {
	t.Helper()
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	return svc
}

// launchFixture opens a real on-disk database with the hook and
// interceptor registered, and seeds the template a configuration hangs off.
func launchFixture(t *testing.T) (*ent.Client, string, int) {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "launch_hook_test.db")
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	svc := mustLaunchEnvelopeService(t)
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("patch").SetKind("runbook").SetDefinition("patch-edge").
		SetOrganization(org).SetInventory(inv).SaveX(ctx)

	return client, dbPath, tmpl.ID
}

// rawAnswers reads the answers column with a plain driver, bypassing every
// ent hook and interceptor.
func rawAnswers(t *testing.T, dbPath string, id int) string {
	t.Helper()

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("opening the raw database: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	var stored string
	if err := rawDB.QueryRowContext(context.Background(),
		"SELECT answers FROM saved_launch_configs WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatalf("reading the stored answers: %v", err)
	}
	return stored
}

func TestSavedLaunchConfigAnswers_NeverReachStorageInPlaintext(t *testing.T) {
	ctx := context.Background()
	client, dbPath, templateID := launchFixture(t)

	cfg := client.SavedLaunchConfig.Create().
		SetName("nightly").
		SetFields(map[string]any{"limit": "edge-01"}).
		SetAnswers(map[string]any{
			"vault_token":    testVaultToken,
			"target_version": "17.6",
		}).
		SetTemplateID(templateID).
		SaveX(ctx)

	stored := rawAnswers(t, dbPath, cfg.ID)

	if strings.Contains(stored, testVaultToken) {
		t.Fatalf("the vault token is in the database in plaintext: %s", stored)
	}
	// The whole map is encrypted rather than only the password entry, so
	// even the non-secret answer is not readable from the raw column. That
	// is deliberate: selective encryption would need the template's survey
	// to know which questions are passwords, which a mutation hook cannot
	// reach.
	if strings.Contains(stored, "17.6") {
		t.Errorf("a non-secret answer is stored in plaintext beside an encrypted one: %s", stored)
	}
	if !strings.Contains(stored, crypto.EncryptedKeyMarker) {
		t.Errorf("the stored answers carry no encryption marker: %s", stored)
	}

	// And the fields column is deliberately NOT encrypted: launch
	// overrides are a limit and a fork count, not credentials, and
	// encrypting them would cost queryability for nothing.
	var storedFields string
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("opening the raw database: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if err := rawDB.QueryRowContext(ctx,
		"SELECT fields FROM saved_launch_configs WHERE id = ?", cfg.ID).Scan(&storedFields); err != nil {
		t.Fatalf("reading the stored fields: %v", err)
	}
	if !strings.Contains(storedFields, "edge-01") {
		t.Errorf("launch overrides were encrypted, which costs queryability for no secret: %s", storedFields)
	}
}

func TestSavedLaunchConfigAnswers_RoundTripThroughEnt(t *testing.T) {
	ctx := context.Background()
	client, _, templateID := launchFixture(t)

	created := client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": testVaultToken}).
		SetTemplateID(templateID).
		SaveX(ctx)

	read := client.SavedLaunchConfig.GetX(ctx, created.ID)
	if read.Answers["vault_token"] != testVaultToken {
		t.Errorf("the answer did not survive the round trip: %#v", read.Answers)
	}

	// A listing decrypts too. Get and Only are implemented as All plus a
	// length check, so an interceptor that only handled one shape would
	// pass the assertion above and fail here.
	all := client.SavedLaunchConfig.Query().AllX(ctx)
	if len(all) != 1 || all[0].Answers["vault_token"] != testVaultToken {
		t.Errorf("a listing did not decrypt its answers: %#v", all)
	}
}

func TestSavedLaunchConfigAnswers_AnUpdateReEncrypts(t *testing.T) {
	ctx := context.Background()
	client, dbPath, templateID := launchFixture(t)

	created := client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": "first"}).
		SetTemplateID(templateID).
		SaveX(ctx)

	// The hook is registered for update as well as create, because answers
	// are not immutable: a saved configuration is a thing an operator
	// edits, and an update path that skipped the hook would write the new
	// credential in plaintext beside the old encrypted one.
	client.SavedLaunchConfig.UpdateOneID(created.ID).
		SetAnswers(map[string]any{"vault_token": testVaultToken}).
		ExecX(ctx)

	if stored := rawAnswers(t, dbPath, created.ID); strings.Contains(stored, testVaultToken) {
		t.Errorf("an updated answer was written in plaintext: %s", stored)
	}

	read := client.SavedLaunchConfig.GetX(ctx, created.ID)
	if read.Answers["vault_token"] != testVaultToken {
		t.Errorf("the updated answer did not survive the round trip: %#v", read.Answers)
	}
}

func TestSavedLaunchConfigAnswers_AnUnreadableRowDoesNotBreakTheRest(t *testing.T) {
	ctx := context.Background()
	client, dbPath, templateID := launchFixture(t)

	good := client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": testVaultToken}).
		SetTemplateID(templateID).
		SaveX(ctx)
	bad := client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": "also-secret"}).
		SetTemplateID(templateID).
		SaveX(ctx)

	// Corrupt one row's ciphertext behind ent's back, which is what a
	// key rotation gone wrong or a partially restored backup looks like.
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("opening the raw database: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if _, err := rawDB.ExecContext(ctx,
		`UPDATE saved_launch_configs SET answers = '{"_encrypted":"not-real-ciphertext"}' WHERE id = ?`,
		bad.ID); err != nil {
		t.Fatalf("corrupting a row: %v", err)
	}

	// The healthy row still reads. A decrypt failure that aborted the
	// query would make one unreadable configuration hide every readable
	// one in the same template, which is the defect an adversarial review
	// caught in the Device interceptor before it shipped.
	all := client.SavedLaunchConfig.Query().AllX(ctx)
	if len(all) != 2 {
		t.Fatalf("a corrupted row aborted the listing: got %d rows", len(all))
	}
	for _, cfg := range all {
		if cfg.ID == good.ID && cfg.Answers["vault_token"] != testVaultToken {
			t.Errorf("the healthy row did not decrypt: %#v", cfg.Answers)
		}
		if cfg.ID == bad.ID {
			// Left in its raw shape rather than silently presented as a
			// decrypted-looking value.
			if _, still := cfg.Answers[crypto.EncryptedKeyMarker]; !still {
				t.Errorf("the corrupted row was presented as decrypted: %#v", cfg.Answers)
			}
		}
	}
}

func TestSavedLaunchConfigAnswers_InterceptorPassesThroughWhatIsNotARowSet(t *testing.T) {
	ctx := context.Background()
	client, _, templateID := launchFixture(t)

	client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": testVaultToken}).
		SetTemplateID(templateID).
		SaveX(ctx)

	// An aggregate returns an int, not a row set. An interceptor that
	// assumed every result was []*ent.SavedLaunchConfig would break every
	// Count, Exist and GroupBy against this entity, and nothing about a
	// broken count looks like an encryption problem.
	if n := client.SavedLaunchConfig.Query().CountX(ctx); n != 1 {
		t.Errorf("Count through the interceptor returned %d, want 1", n)
	}
	if !client.SavedLaunchConfig.Query().ExistX(ctx) {
		t.Error("Exist through the interceptor reported nothing")
	}
}

func TestSavedLaunchConfigAnswers_AQueryFailureIsReportedNotSwallowed(t *testing.T) {
	ctx := context.Background()
	client, _, templateID := launchFixture(t)

	client.SavedLaunchConfig.Create().
		SetAnswers(map[string]any{"vault_token": testVaultToken}).
		SetTemplateID(templateID).
		SaveX(ctx)

	// A closed client is a real query failure, not a simulated one. The
	// interceptor must return it rather than reporting an empty result
	// set: "the database is gone" and "this template has no saved
	// configurations" are answers a caller acts on very differently.
	if err := client.Close(); err != nil {
		t.Fatalf("closing the client: %v", err)
	}
	if _, err := client.SavedLaunchConfig.Query().All(ctx); err == nil {
		t.Error("a query against a closed database reported success")
	}
}
