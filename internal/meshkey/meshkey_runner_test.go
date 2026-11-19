// Package meshkey_test: the runner signing key's storage (0.5.0). It is the
// account seed's custody, applied to the scoped seed that issues Runner
// credentials, so each property is asserted by the outcome it protects.
package meshkey_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshkey"
	"strings"
)

// TestRunnerKeyIssuesRunnerCredentials: a Runner credential comes out of the
// stored key, under the runner scope, not from the account signer.
func TestRunnerKeyIssuesRunnerCredentials(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "runner-issue")
	store := meshkey.NewStore(client)
	acct := newAccount(t, "PLEIADES")
	if err := store.Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}
	issuer, err := store.RunnerIssuer(ctx, acct.Subject)
	if err != nil {
		t.Fatalf("RunnerIssuer: %v", err)
	}
	cred, err := issuer.Issue("runner-1", meshid.DefaultFleetExpiry)
	if err != nil {
		t.Fatalf("issuing a runner credential from the stored key: %v", err)
	}
	if len(cred.Creds) == 0 {
		t.Fatal("the stored runner key issued an empty credential")
	}
}

// TestRunnerSeedIsNeverStoredInPlaintext reads the column with no
// interceptor, so it observes what is on disk rather than what a read returns.
func TestRunnerSeedIsNeverStoredInPlaintext(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "runner-plaintext")
	acct := newAccount(t, "PLEIADES")
	if err := meshkey.NewStore(client).Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw := enttest.Open(t, "sqlite3", "file:runner-plaintext?mode=memory&cache=shared&_fk=1")
	defer func() { _ = raw.Close() }()
	stored := raw.MeshSigningKey.Query().Where(meshsigningkey.AccountSubjectEQ(acct.Subject)).OnlyX(ctx)
	if stored.RunnerSeed == "" {
		t.Fatal("the runner seed was not stored")
	}
	if strings.Contains(stored.RunnerSeed, string(acct.RunnerSeed)) {
		t.Fatal("the runner seed is stored in plaintext; anyone who can read the database can mint Runner credentials the broker trusts")
	}
}

// TestAccountWithoutRunnerScopeRefusesRunnerIssuance: an account minted before
// the runner scope existed has no runner seed, and must say so by name rather
// than issue a credential the broker would refuse.
func TestAccountWithoutRunnerScopeRefusesRunnerIssuance(t *testing.T) {
	ctx := context.Background()
	client := hookedClient(t, "runner-absent")
	store := meshkey.NewStore(client)
	acct := newAccount(t, "PLEIADES")
	acct.RunnerSeed = nil
	if err := store.Save(ctx, "key-1", acct); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := store.RunnerIssuer(ctx, acct.Subject)
	if !errors.Is(err, meshkey.ErrNoRunnerKey) {
		t.Fatalf("RunnerIssuer error = %v, want ErrNoRunnerKey", err)
	}
}
