package crypto_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// Rotating and migrating survey answers, which matters more than the Device
// pass and for a reason worth stating where the test lives.
//
// A Device migrates itself: its properties are written by ordinary
// operation, so the hook converts a pre-Phase-78c row the first time
// anything touches it. Nothing in this platform ever updates a saved launch
// configuration's answers; internal/launch's store creates one and reads it
// back, and there is no edit path at all. So for this entity the rotation
// pass is not a sweep for stragglers, it is the ONLY way a row written
// before that phase ever becomes bound.

// legacyLaunchFixture seeds one saved configuration in the pre-Phase-78c
// unbound form and one in the bound form.
func legacyLaunchFixture(t *testing.T) (string, *crypto.EnvelopeService, []int) {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rotate_launch_test.db")

	svc, err := crypto.NewEnvelopeService([]byte(aadKey), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	inv := client.Inventory.Create().SetName("all").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("some-runbook").
		SetOrganization(org).SetInventory(inv).
		SaveX(ctx)

	ids := make([]int, 0, 2)
	for _, name := range []string{"legacy", "modern"} {
		row := client.SavedLaunchConfig.Create().
			SetName(name).
			SetTemplate(tmpl).
			SetAnswers(map[string]any{"survey_password": aadSecret + "-" + name}).
			SaveX(ctx)
		ids = append(ids, row.ID)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Rewrite the first row the way Phase 22 would have: unbound, and with
	// no binding column at all.
	plaintext, err := json.Marshal(map[string]any{"survey_password": aadSecret + "-legacy"})
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

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	if _, err := rawDB.ExecContext(ctx,
		"UPDATE saved_launch_configs SET answers = ?, secret_binding = '' WHERE id = ?",
		string(stored), ids[0]); err != nil {
		t.Fatalf("writing the legacy row: %v", err)
	}

	return dbPath, svc, ids
}

// openLaunchConfigs returns a client with the answer hooks installed.
func openLaunchConfigs(t *testing.T, dbPath string, svc *crypto.EnvelopeService) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))
	return client
}

// TestRotateSavedLaunchConfigAnswersMigratesTheUnboundRow is the pass's
// real caller and proof.
func TestRotateSavedLaunchConfigAnswersMigratesTheUnboundRow(t *testing.T) {
	ctx := context.Background()
	dbPath, svc, ids := legacyLaunchFixture(t)
	client := openLaunchConfigs(t, dbPath, svc)

	// Both forms read correctly before the pass, which is the migration
	// window's requirement.
	for i, name := range []string{"legacy", "modern"} {
		row, err := client.SavedLaunchConfig.Get(ctx, ids[i])
		if err != nil {
			t.Fatalf("Get(%d) error = %v", ids[i], err)
		}
		if want := aadSecret + "-" + name; row.Answers["survey_password"] != want {
			t.Fatalf("%s reads back %v before migration, want %q", name, row.Answers, want)
		}
	}

	rotated, err := crypto.RotateSavedLaunchConfigAnswers(ctx, client, svc)
	if err != nil {
		t.Fatalf("RotateSavedLaunchConfigAnswers() error = %v", err)
	}
	if rotated != 2 {
		t.Fatalf("RotateSavedLaunchConfigAnswers() converted %d rows, want both", rotated)
	}

	// Every row is bound now, and every answer survived.
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	for i, name := range []string{"legacy", "modern"} {
		var raw, binding string
		if err := rawDB.QueryRowContext(ctx,
			"SELECT answers, secret_binding FROM saved_launch_configs WHERE id = ?", ids[i]).
			Scan(&raw, &binding); err != nil {
			t.Fatalf("reading row %d: %v", ids[i], err)
		}
		if !crypto.IsBoundEnvelope(unwrap(t, raw)) {
			t.Errorf("%s is still unbound after the pass: %s", name, raw)
		}
		if binding == "" {
			t.Errorf("%s was migrated without being given a binding", name)
		}

		row, err := client.SavedLaunchConfig.Get(ctx, ids[i])
		if err != nil {
			t.Fatalf("Get(%d) error = %v", ids[i], err)
		}
		if want := aadSecret + "-" + name; row.Answers["survey_password"] != want {
			t.Errorf("%s reads back %v after migration, want %q", name, row.Answers, want)
		}
	}
}

// TestARelocatedAnswerDocumentIsRefusedAfterMigration is the negative
// control for the pass above. Survey answers are the one path by which a
// password reaches a stored row, so relocating one row's answers onto
// another is the attack that matters here.
func TestARelocatedAnswerDocumentIsRefusedAfterMigration(t *testing.T) {
	ctx := context.Background()
	dbPath, svc, ids := legacyLaunchFixture(t)
	client := openLaunchConfigs(t, dbPath, svc)

	if _, err := crypto.RotateSavedLaunchConfigAnswers(ctx, client, svc); err != nil {
		t.Fatalf("RotateSavedLaunchConfigAnswers() error = %v", err)
	}

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	if _, err := rawDB.ExecContext(ctx,
		"UPDATE saved_launch_configs SET answers = (SELECT answers FROM saved_launch_configs WHERE id = ?) WHERE id = ?",
		ids[0], ids[1]); err != nil {
		t.Fatalf("relocating the answers: %v", err)
	}

	row, err := client.SavedLaunchConfig.Get(ctx, ids[1])
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if row.Answers["survey_password"] == aadSecret+"-legacy" {
		t.Fatal("a relocated answer document decrypted: the binding is not doing anything")
	}
	if _, sealed := row.Answers[crypto.EncryptedKeyMarker]; !sealed {
		t.Errorf("the relocated row = %v, want it left sealed", row.Answers)
	}
}

// TestBulkAnswerUpdateIsRefused holds the refusal in place for the same
// reason the Device one is held: without it a future caller reaching for
// the bulk builder would write a ciphertext bound to nothing.
func TestBulkAnswerUpdateIsRefused(t *testing.T) {
	ctx := context.Background()
	dbPath, svc, _ := legacyLaunchFixture(t)
	client := openLaunchConfigs(t, dbPath, svc)

	_, err := client.SavedLaunchConfig.Update().
		SetAnswers(map[string]any{"survey_password": "anything"}).
		Save(ctx)
	if !errors.Is(err, crypto.ErrBulkLaunchConfigAnswers) {
		t.Fatalf("bulk Update() error = %v, want it refused", err)
	}
}

// TestRotationLeavesAConfigWithNoAnswersAlone covers the ordinary saved
// configuration that answers no survey, which is most of them.
func TestRotationLeavesAConfigWithNoAnswersAlone(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "no_answers.db")

	svc, err := crypto.NewEnvelopeService([]byte(aadKey), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	inv := client.Inventory.Create().SetName("all").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("some-runbook").
		SetOrganization(org).SetInventory(inv).
		SaveX(ctx)
	client.SavedLaunchConfig.Create().SetName("plain").SetTemplate(tmpl).SaveX(ctx)

	rotated, err := crypto.RotateSavedLaunchConfigAnswers(ctx, client, svc)
	if err != nil {
		t.Fatalf("RotateSavedLaunchConfigAnswers() error = %v", err)
	}
	if rotated != 0 {
		t.Errorf("converted %d rows, want none for a configuration with no answers", rotated)
	}
}

// TestLaunchRotationSkipsAnUnreadableRowRatherThanAborting mirrors the
// credential pass's own tolerance, and matters for the same reason: one
// unreadable row must not stop an operator migrating every other one, which
// during an incident is the difference between a rotation that completes
// and one that cannot be started.
func TestLaunchRotationSkipsAnUnreadableRowRatherThanAborting(t *testing.T) {
	ctx := context.Background()
	dbPath, svc, ids := legacyLaunchFixture(t)

	corrupt, err := json.Marshal(map[string]string{
		crypto.EncryptedKeyMarker: "v1$AES256GCM-AAD$bm90$cmVhbA==",
	})
	if err != nil {
		t.Fatalf("building the corrupt row: %v", err)
	}

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	if _, err := rawDB.ExecContext(ctx,
		"UPDATE saved_launch_configs SET answers = ? WHERE id = ?", string(corrupt), ids[0]); err != nil {
		t.Fatalf("corrupting the row: %v", err)
	}

	client := openLaunchConfigs(t, dbPath, svc)

	rotated, err := crypto.RotateSavedLaunchConfigAnswers(ctx, client, svc)
	if err != nil {
		t.Fatalf("RotateSavedLaunchConfigAnswers() error = %v, want the pass to complete", err)
	}
	if rotated != 1 {
		t.Fatalf("converted %d rows, want the one readable row", rotated)
	}

	// The unreadable row was left exactly as it was, rather than being
	// re-encrypted as though its sealed shape were plaintext.
	var raw string
	if err := rawDB.QueryRowContext(ctx,
		"SELECT answers FROM saved_launch_configs WHERE id = ?", ids[0]).Scan(&raw); err != nil {
		t.Fatalf("reading the corrupt row: %v", err)
	}
	if !strings.Contains(raw, "bm90") {
		t.Errorf("the unreadable configuration was modified: %s", raw)
	}
}
