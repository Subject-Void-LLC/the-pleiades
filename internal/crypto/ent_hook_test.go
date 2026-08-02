package crypto_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

func TestEnvelopeEncryptionHook(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc, err := crypto.NewAESService(key)
	if err != nil {
		t.Fatalf("failed to init crypto service: %v", err)
	}

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Apply Hooks and Interceptors
	client.Fact.Use(crypto.EnvelopeEncryptionHook(svc))
	client.Fact.Intercept(crypto.EnvelopeDecryptionInterceptor(svc))

	ctx := context.Background()

	// 1. Create a Device (required by Fact edge)
	dev := client.Device.Create().
		SetName("devnet-sandbox-1").
		SaveX(ctx)

	// 2. Save a Fact with sensitive DevNet AAA login
	payload := map[string]interface{}{
		"AAA_LOGIN": "Privilege15_Dynamic_Token_12345",
	}

	_ = client.Fact.Create().
		SetHash("hash123").
		SetDevice(dev).
		SetPayload(payload).
		SaveX(ctx)

	// 3. Verify it was returned decrypted from the SaveX mutation itself.
	// Wait, SaveX returns the struct. The hook sets the payload to the encrypted map before saving,
	// but currently the hook DOES NOT decrypt it on the way out of SaveX.
	// Let's query it back explicitly.

	retrievedFact := client.Fact.Query().FirstX(ctx)
	if val, ok := retrievedFact.Payload["AAA_LOGIN"]; !ok || val != "Privilege15_Dynamic_Token_12345" {
		t.Fatalf("Interceptor failed to decrypt! Got payload: %v", retrievedFact.Payload)
	}

	// 4. Release Gate: Assert that the raw database actually holds unreadable ciphertext.
	// We bypass the interceptors by querying the DB driver directly.
	rawDB, err := sql.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	defer rawDB.Close()

	rows, err := rawDB.QueryContext(ctx, "SELECT payload FROM facts")
	if err != nil {
		t.Fatalf("failed to query raw DB: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		var rawJSON string
		if err := rows.Scan(&rawJSON); err != nil {
			t.Fatalf("failed to scan raw json: %v", err)
		}

		// The raw JSON should literally be {"_encrypted": "base64..."}
		if !strings.Contains(rawJSON, `"_encrypted"`) {
			t.Fatalf("Raw database row is NOT encrypted! Got: %s", rawJSON)
		}
		if strings.Contains(rawJSON, "Privilege15") {
			t.Fatalf("Raw database row leaked plaintext! Got: %s", rawJSON)
		}
	} else {
		t.Fatalf("No rows found in raw DB query")
	}
}
