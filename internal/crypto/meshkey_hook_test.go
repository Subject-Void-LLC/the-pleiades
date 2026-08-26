package crypto_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"

	_ "github.com/mattn/go-sqlite3"
)

// meshKeyClient builds a client with the seed hook and interceptor
// registered, which is the composition cmd/controller performs.
func meshKeyClient(t *testing.T) (*ent.Client, *crypto.EnvelopeService) {
	t.Helper()
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService: %v", err)
	}
	client := enttest.Open(t, "sqlite3", "file:meshkey?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	client.MeshSigningKey.Use(crypto.MeshSigningKeySeedHook(svc))
	client.MeshSigningKey.Intercept(crypto.MeshSigningKeySeedInterceptor(svc))
	return client, svc
}

// theSeed is a realistic account seed shape: base32, leading "S", and no
// "$" anywhere, which is what makes the envelope's own format usable as
// the already-encrypted marker.
const theSeed = "SAAGY5ZQKLTJZ2QW3P5RXQ2N4L7VJZQK6Y3W5B2XQKZ4L7VJZQK6Y3W5B2"

// TestSeedIsSealedAtRestAndOpenedOnRead is the round trip, and it reads
// the raw column rather than trusting the interceptor to tell the truth
// about itself.
func TestSeedIsSealedAtRestAndOpenedOnRead(t *testing.T) {
	client, _ := meshKeyClient(t)
	ctx := context.Background()

	row := client.MeshSigningKey.Create().
		SetKeyID("2026-08-25").
		SetAccountSubject("ACCOUNTPUBKEY").
		SetPublicKey("APUBKEY").
		SetSeed(theSeed).
		SaveX(ctx)

	// Raw read, with the interceptor bypassed by querying a fresh client
	// that has none registered, so this observes what is actually stored.
	raw := enttest.Open(t, "sqlite3", "file:meshkey?mode=memory&cache=shared&_fk=1")
	defer raw.Close()
	stored := raw.MeshSigningKey.Query().Where(meshsigningkey.ID(row.ID)).OnlyX(ctx)

	if stored.Seed == theSeed {
		t.Fatal("the seed is stored in plaintext")
	}
	if !crypto.IsBoundEnvelope(stored.Seed) {
		t.Fatalf("stored seed %q is not a BOUND envelope, so it is not tied to its row", stored.Seed)
	}

	// And the interceptor opens it.
	got := client.MeshSigningKey.Query().Where(meshsigningkey.ID(row.ID)).OnlyX(ctx)
	if got.Seed != theSeed {
		t.Errorf("read back seed = %q, want %q", got.Seed, theSeed)
	}
}

// TestARelocatedSeedIsRefused is the whole reason this column uses the
// bound envelope rather than the unbound one, and it is the negative
// control: moving one row's ciphertext onto another row must not open.
//
// The consequence here is worse than for a credential. A relocated
// credential makes the platform inject the wrong secret; a relocated
// SIGNING KEY makes the platform mint credentials another account trusts,
// which manufactures identities rather than exposing a value.
func TestARelocatedSeedIsRefused(t *testing.T) {
	client, _ := meshKeyClient(t)
	ctx := context.Background()

	victim := client.MeshSigningKey.Create().
		SetKeyID("victim").SetAccountSubject("A").SetPublicKey("P1").SetSeed(theSeed).SaveX(ctx)
	other := client.MeshSigningKey.Create().
		SetKeyID("other").SetAccountSubject("A").SetPublicKey("P2").SetSeed(theSeed + "XX").SaveX(ctx)

	raw := enttest.Open(t, "sqlite3", "file:meshkey?mode=memory&cache=shared&_fk=1")
	defer raw.Close()
	victimSealed := raw.MeshSigningKey.Query().Where(meshsigningkey.ID(victim.ID)).OnlyX(ctx).Seed

	// The attack: write the victim's ciphertext onto the other row, at the
	// SQL layer, because no code path in this package can produce it.
	if _, err := raw.MeshSigningKey.Update().
		Where(meshsigningkey.ID(other.ID)).
		SetSeed(victimSealed).
		Save(ctx); err != nil {
		t.Fatalf("staging the relocation: %v", err)
	}

	got := client.MeshSigningKey.Query().Where(meshsigningkey.ID(other.ID)).OnlyX(ctx)
	if got.Seed == theSeed {
		t.Fatal("a seed relocated from another row decrypted, so the binding is doing nothing")
	}
	if !crypto.IsBoundEnvelope(got.Seed) {
		t.Errorf("relocated seed came back as %q; it should be left sealed", got.Seed)
	}
}

// TestTheHookIsIdempotent covers the caller that reads a row through the
// interceptor and writes it back: the already-sealed value must not be
// sealed a second time, or it becomes unreadable.
func TestTheHookIsIdempotent(t *testing.T) {
	client, _ := meshKeyClient(t)
	ctx := context.Background()

	row := client.MeshSigningKey.Create().
		SetKeyID("k").SetAccountSubject("A").SetPublicKey("P").SetSeed(theSeed).SaveX(ctx)

	read := client.MeshSigningKey.Query().Where(meshsigningkey.ID(row.ID)).OnlyX(ctx)
	client.MeshSigningKey.UpdateOneID(row.ID).SetSeed(read.Seed).SetActive(true).ExecX(ctx)

	again := client.MeshSigningKey.Query().Where(meshsigningkey.ID(row.ID)).OnlyX(ctx)
	if again.Seed != theSeed {
		t.Errorf("after a read-modify-write the seed reads back as %q, want %q", again.Seed, theSeed)
	}
}

// TestBulkUpdateIsRefused pins the refusal rather than the convenience.
// Every row's ciphertext is bound to that row, so one value written across
// many rows could be correct for at most one of them, and the tempting
// implementation fails INVISIBLY: the write would succeed and the rows
// would simply never open again.
func TestBulkUpdateIsRefused(t *testing.T) {
	client, _ := meshKeyClient(t)
	ctx := context.Background()

	client.MeshSigningKey.Create().
		SetKeyID("k1").SetAccountSubject("A").SetPublicKey("P1").SetSeed(theSeed).SaveX(ctx)

	_, err := client.MeshSigningKey.Update().SetSeed(theSeed).Save(ctx)
	if err == nil {
		t.Fatal("a bulk update of seeds succeeded")
	}
	if !errors.Is(err, crypto.ErrBulkMeshSigningKeySeed) {
		t.Errorf("bulk update error = %v, want ErrBulkMeshSigningKeySeed", err)
	}
	if !strings.Contains(err.Error(), "bound to that row") {
		t.Errorf("error %q does not say why the operation is meaningless", err)
	}
}
