// Package meshkey is custody for the account signing key the Controller
// mints mesh identities with.
//
// It is the seam between two packages that must not know about each
// other. internal/meshid is pure cryptography: it turns keys and a
// permission set into a signed credential, holds no state and imports no
// storage, which is what lets its permission decisions be tested without
// a database or a broker. internal/ent is storage. This package is the
// only thing that touches both.
//
// # What is stored, and what is deliberately not
//
// Only the account SIGNING key. Not the operator key, which can mint a
// new account and therefore a new tenant of the mesh, and not the account
// identity key, whose only use is re-minting the account itself. Both of
// those stay offline, and internal/meshid's package doc explains why a
// Controller holding either turns a Controller compromise into a mesh
// compromise. This package cannot store them: it takes an account and
// keeps the one field that is meant to be online.
//
// The seed is sealed at rest by the existing envelope service under the
// one MASTER_ENCRYPTION_KEY, through crypto.MeshSigningKeySeedHook, which
// cmd/controller registers. That is not this package's doing and it does
// not get to opt out: writing through the ent client is what applies it.
//
// # The rule this package exists to enforce
//
// One ACTIVE key per account. The schema says outright that this is
// enforced in application code rather than by a unique index, and
// explains why the obvious index is wrong: a unique index on
// (account_subject, active) does not mean "one active key", it means one
// row per distinct value of active, which equally forbids a second
// RETIRED key, and retired keys are exactly the history the entity
// exists to keep. Until this package existed, nothing enforced it at all.
package meshkey

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
)

// ErrNoActiveKey reports that this deployment holds no signing key for the
// account it was asked about.
//
// It is a named error because the right response differs by caller and
// neither is "fail": a Controller starting without one is an
// unauthenticated deployment, which is the supported default, while a
// command asked to issue a credential has nothing to issue with and must
// say so.
var ErrNoActiveKey = errors.New("meshkey: no active signing key for this account")

// Store reads and writes signing keys through an ent client.
//
// A struct rather than free functions because the client carries the
// crypto hooks that seal the seed, and a caller holding a Store cannot
// accidentally reach a client that does not.
type Store struct {
	client *ent.Client
}

// NewStore returns a Store over client.
//
// client must be one that has had installCryptoHooks applied. That is not
// checkable here, since a hook leaves no readable mark on the client, and
// it is covered instead by cmd/controller's own composition test that
// every exported crypto hook is registered.
func NewStore(client *ent.Client) *Store { return &Store{client: client} }

// Save records acct's signing key as the active one for its account, and
// retires whichever key was active before.
//
// The two writes are one transaction on purpose. A failure between them
// leaves either two active keys, which makes "which key signs" ambiguous
// and is the exact state the schema's own comment says must not occur, or
// none, which stops the Controller minting anything. Neither is a state
// an operator could diagnose from the outside.
//
// keyID is the operator-facing name for this key in the rotation
// sequence. It is deliberately not the public key: somebody retiring a
// key has to name it in a command before any key material is loaded, and
// a 56-character nkey is not a name.
func (s *Store) Save(ctx context.Context, keyID string, acct *meshid.Account) error {
	if acct == nil {
		return errors.New("meshkey: no account was given")
	}
	if keyID == "" {
		return errors.New("meshkey: a key needs an id an operator can name it by")
	}

	pub, err := meshid.SigningKeyPublic(acct.SigningKeySeed)
	if err != nil {
		return fmt.Errorf("meshkey: reading the signing key's public key: %w", err)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("meshkey: beginning a transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Retire first, create second. The other order leaves a moment with
	// two active keys, and a concurrent reader in that moment gets an
	// arbitrary one.
	if _, err := tx.MeshSigningKey.Update().
		Where(meshsigningkey.AccountSubjectEQ(acct.Subject), meshsigningkey.ActiveEQ(true)).
		SetActive(false).
		Save(ctx); err != nil {
		return fmt.Errorf("meshkey: retiring the previous active key: %w", err)
	}

	if _, err := tx.MeshSigningKey.Create().
		SetKeyID(keyID).
		SetAccountSubject(acct.Subject).
		SetPublicKey(pub).
		SetSeed(string(acct.SigningKeySeed)).
		SetActive(true).
		Save(ctx); err != nil {
		return fmt.Errorf("meshkey: recording the new signing key: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("meshkey: committing the new signing key: %w", err)
	}
	return nil
}

// Issuer returns an issuer for accountSubject's active signing key.
//
// Returns ErrNoActiveKey when this deployment holds none, which a caller
// distinguishes with errors.Is rather than by treating every failure as
// fatal: no key is the ordinary state of a deployment that has not turned
// mesh authentication on.
func (s *Store) Issuer(ctx context.Context, accountSubject string) (*meshid.Issuer, error) {
	row, err := s.client.MeshSigningKey.Query().
		Where(
			meshsigningkey.AccountSubjectEQ(accountSubject),
			meshsigningkey.ActiveEQ(true),
		).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %s", ErrNoActiveKey, accountSubject)
	}
	if err != nil {
		// A NotSingular error here means the one-active-key rule was
		// broken by something that did not go through Save. Say that,
		// rather than letting it read as a generic query failure, because
		// the fix is to retire a row rather than to retry.
		if _, ok := err.(*ent.NotSingularError); ok {
			return nil, fmt.Errorf("meshkey: account %s has more than one active signing key, so which key signs is ambiguous; retire all but one", accountSubject)
		}
		return nil, fmt.Errorf("meshkey: reading the active signing key: %w", err)
	}

	// The seed arrives decrypted, because reading through this client runs
	// crypto.MeshSigningKeySeedInterceptor.
	issuer, err := meshid.NewIssuer(row.AccountSubject, []byte(row.Seed))
	if err != nil {
		return nil, fmt.Errorf("meshkey: building an issuer from the stored key %q: %w", row.KeyID, err)
	}
	return issuer, nil
}

// ActiveAccount returns the one account this deployment holds an active
// signing key for.
//
// It exists so the Controller does not need an environment variable
// naming its own account. The account subject is already recorded on the
// key row, and a second copy in the environment is a second thing that
// can disagree: a deployment whose variable names one account while its
// key signs for another authenticates nothing and says nothing useful
// about why.
//
// More than one is refused rather than resolved. Holding keys for several
// accounts is a real multi-tenant arrangement that nothing in this
// platform builds yet, and picking one arbitrarily would mint identities
// for a tenant the operator did not name. When that arrangement exists it
// needs an explicit choice, not a default.
//
// Returns ErrNoActiveKey when there is none, which is the ordinary state
// of a deployment that has not turned mesh authentication on.
func (s *Store) ActiveAccount(ctx context.Context) (string, error) {
	rows, err := s.client.MeshSigningKey.Query().
		Where(meshsigningkey.ActiveEQ(true)).
		All(ctx)
	if err != nil {
		return "", fmt.Errorf("meshkey: reading the active signing keys: %w", err)
	}

	switch len(rows) {
	case 0:
		return "", ErrNoActiveKey
	case 1:
		return rows[0].AccountSubject, nil
	}

	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		seen[r.AccountSubject] = struct{}{}
	}
	if len(seen) == 1 {
		// Several active rows for ONE account is the ambiguity Issuer
		// already reports, and it has a different fix from the one below.
		return rows[0].AccountSubject, nil
	}
	return "", fmt.Errorf("meshkey: this deployment holds active signing keys for %d accounts, so which one it authenticates as has to be said explicitly rather than guessed", len(seen))
}

// ActiveKeyID returns the operator-facing name of the active key, for a
// command that reports what this deployment is signing with.
func (s *Store) ActiveKeyID(ctx context.Context, accountSubject string) (string, error) {
	row, err := s.client.MeshSigningKey.Query().
		Where(
			meshsigningkey.AccountSubjectEQ(accountSubject),
			meshsigningkey.ActiveEQ(true),
		).
		Only(ctx)
	if ent.IsNotFound(err) {
		return "", fmt.Errorf("%w: %s", ErrNoActiveKey, accountSubject)
	}
	if err != nil {
		return "", fmt.Errorf("meshkey: reading the active signing key: %w", err)
	}
	return row.KeyID, nil
}
