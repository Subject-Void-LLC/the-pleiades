// This file holds the rule that a value written as secret is never shown as
// plain, in the store because every edit of a credential type reaches it.
//
// redactInputs decides what a read shows from the type's CURRENT schema, and
// it has to: it is the one place every read path goes through. So the
// guarantee cannot live in the reader alone. It used to be possible to break
// it with a type edit, and the API answered 200 each time: clearing an
// input's secret flag showed every stored value of it in plain text, and so
// did removing the input and adding it back as non-secret, because a removal
// left the values behind in every credential. Four parts close that:
//
//  1. An input's secret flag never goes from true to false on a saved type
//     (checkSecretKept). Making an input non-secret is a removal and an add.
//  2. After an edit of a custom type a credential keeps a stored value, an
//     external reference and an input source binding only for an input the
//     schema after the edit declares, and for an input it adds only when it
//     adds it as secret (purgeInputs, droppedByEdit). A removal deletes what
//     credentials held for the input. A non-secret add deletes whatever was
//     stored under the new id before it existed: a value a removal made
//     before this rule left behind, or one a restore brought back. Deleting
//     rather than refusing the add is the choice that cannot leak and leaves
//     the operator something to do, since no request can remove such a value
//     (a credential's own update refuses an input its type does not
//     declare). A secret add keeps it, shown only as the marker (addedPlain
//     says why that cannot leak). And a value under an id the new schema
//     does not declare, left by such an earlier removal, is deleted by the
//     next edit of the type of any kind; until then a credential update
//     carries it untouched and no read shows it (carried.go). A release's
//     reconcile of a managed type deletes only on a non-secret add
//     (adoptExistingType says why).
//  3. A type edit and a credential create, update or input-source write of
//     that type never interleave (lockType). Each takes the type row's write
//     lock inside its transaction before it reads anything it checks or
//     writes back, and a PATCH or a form folds its change over the record
//     read under that lock (UpdateTypeWith, UpdateCredentialWith), never
//     over a copy read before it. So no credential write can be checked
//     against one schema and committed under another, and none of those
//     writes runs between a purge's read of a credential and its write. Two
//     writers take no lock. DeleteCredential removes a row and writes no
//     value, so a credential deleted between a purge's read and its write is
//     simply skipped (purgeCredential). Key rotation
//     (internal/crypto/rotate.go) writes credentials without this store: it
//     rewrites the same values under a new key, in one UPDATE conditional on
//     the updated_at it read, which every write of a credential moves, so a
//     purge that commits after rotation's read makes it skip the row
//     (TestRotate_StaleWriteAfterPurgeIsSkipped), and a purge that reads
//     after rotation's write writes the same values back.
//  4. A read takes no lock, so that no read waits behind a purge. It reads a
//     credential and its type in one snapshot instead (queryCredentials,
//     and the resolver's own read): read in two statements, a type edit
//     committed between them, removing a secret input and adding it back
//     non-secret, would pair a value written as secret with a schema that
//     shows it.
//
// A credential whose stored values this Controller cannot decrypt stops a
// type edit rather than being skipped: it might hold a value for the input
// being removed, and its ciphertext is never rewritten blind.
//
// # What the lock costs
//
// The lock is exclusive: every credential create, update and input-source
// write of a type queues behind every other one of the same type, across
// tenants, on PostgreSQL as well as SQLite. Credential writes only need to
// exclude type edits, so a shared lock (SELECT ... FOR SHARE) for them would
// be enough, but ent offers one only through its sql/lock or sql/modifier
// feature, which this repository's generated client does not enable, and
// refuses it on SQLite, so taking it would mean regenerating the whole
// client and choosing the lock by dialect at run time. A credential write
// holds the lock for its own transaction, measured at about 0.35
// milliseconds on a SQLite file database, which bounds one type's credential
// writes to a few thousand a second; they are an administrator's writes, not
// a run's, and on SQLite every write is serialized by the database anyway. A
// type edit holds it for its purge, which reads every credential of the type
// and rewrites those it changes: see purgeInputs.
package credstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialinputsource"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialtype"
)

// purgePage is how many credentials a purge decrypts and holds at once. It
// bounds the plaintext in memory, not the time the lock is held.
const purgePage = 200

// lockType takes the write lock on one credential type's row and returns
// the row, with its organization, as it stands once the lock is held. It
// must run inside a transaction, whose end releases the lock, and asks
// ent.TxFromContext whether one is open: a client in the context may be the
// root one, on which the lock would end with its own statement.
//
// The lock is a write to updated_at, which nothing reads for a credential
// type; the write is what matters. On SQLite it takes the database's write
// lock, which every other writer waits for; on PostgreSQL it takes the row's
// lock, which every other writer of the row waits for, and the read that
// follows sees the latest committed row because read committed reads each
// statement afresh.
func (s *entStore) lockType(ctx context.Context, id int) (*ent.CredentialType, error) {
	tx := ent.TxFromContext(ctx)
	if tx == nil {
		return nil, errors.New("credstore: a credential type can only be locked inside a transaction")
	}
	db := tx.Client()
	if err := db.CredentialType.UpdateOneID(id).SetUpdatedAt(time.Now()).Exec(ctx); err != nil {
		return nil, wrapNotFound(err, "credential type")
	}
	row, err := db.CredentialType.Query().Where(credentialtype.IDEQ(id)).WithOrganization().Only(ctx)
	if err != nil {
		return nil, wrapNotFound(err, "credential type")
	}
	return row, nil
}

// checkSecretKept refuses a schema that would make an input non-secret when
// the stored schema marks it secret.
//
// The message names the input by the stored schema's own id, which is the
// type's, never a value a caller sent.
func checkSecretKept(stored, next credtype.InputSchema) error {
	for _, field := range stored.Fields {
		if !field.Secret {
			continue
		}
		if kept, declared := next.Field(field.ID); declared && !kept.Secret {
			return fmt.Errorf(
				"%w: input %q is secret and cannot be made non-secret; to make it non-secret, remove it and add "+
					"it again, which deletes the value every credential of this type stores for it",
				credtype.ErrInvalidType, field.ID)
		}
	}
	return nil
}

// droppedByEdit returns the rule a custom type's edit purges by: an id next
// does not declare (removed now, or left by an earlier removal), and an id
// next adds as non-secret. An id next adds as secret keeps what is stored
// under it (addedPlain says why that cannot leak).
func droppedByEdit(stored, next credtype.InputSchema) func(string) bool {
	return func(id string) bool {
		after, declared := next.Field(id)
		if !declared {
			return true
		}
		_, before := stored.Field(id)
		return !before && !after.Secret
	}
}

// addedPlain returns the ids next adds as non-secret, which stored does not
// declare, and the rule that drops exactly those: the only purge a release's
// reconcile makes, and the add half of a custom type's edit.
//
// An id added as secret keeps whatever is stored under it: a value a newer
// build wrote before an older build's reconcile left the input out, one a
// removal made before this rule left behind, or one a restore brought back.
// Keeping it cannot leak. It reads back only as the marker while the input
// is secret; checkSecretKept refuses making it non-secret in place, so it
// can become non-secret only by being removed and added back, and a
// non-secret add purges it. What is left is a cost, not a leak: a non-secret
// input's values written by a newer build are deleted when that build adds
// the input back after an older build's reconcile left it out. The complete
// fix, a release ordinal on managed rows so an older build never rewrites a
// newer build's row (LESSONS 422), needs a schema change and is not built.
func addedPlain(stored, next credtype.InputSchema) (int, func(string) bool) {
	added := make(map[string]struct{})
	for _, field := range next.Fields {
		if _, declared := stored.Field(field.ID); !declared && !field.Secret {
			added[field.ID] = struct{}{}
		}
	}
	return len(added), func(id string) bool {
		_, isAdded := added[id]
		return isAdded
	}
}

// purgeInputs deletes, from every credential of the type, each stored value,
// external reference and input source binding for an input drop names. It
// runs inside the transaction lockType opened, so no credential of the type
// is written through this store while it reads and rewrites them.
//
// Credentials are read a page at a time in id order, so the plaintext held
// at once is bounded; the lock is held for the whole purge regardless, and
// a credential write of the type waits that long. Measured on a SQLite file
// database with two thousand credentials of one type, an edit that drops
// nothing read each in about 4 microseconds and one that drops an input read
// and rewrote each in about 35, so a type with ten thousand credentials holds
// its writers for about a third of a second when an input is removed. On
// PostgreSQL each rewrite is a round trip, so expect several times that
// (reasoned, not measured); docs/10-running-in-production.md says so.
//
// Each credential is rewritten through the same mutation hook every write
// uses, so the values it keeps are sealed again under the row's own binding;
// nothing here edits ciphertext. A credential deleted while this ran has
// nothing left to purge and is skipped.
func (s *entStore) purgeInputs(ctx context.Context, typeID int, drop func(string) bool) error {
	db := s.db(ctx)
	ofType := credential.HasCredentialTypeWith(credentialtype.IDEQ(typeID))

	for after := 0; ; {
		rows, err := db.Credential.Query().
			Where(ofType, credential.IDGT(after)).
			Order(ent.Asc(credential.FieldID)).
			Limit(purgePage).
			All(ctx)
		if err != nil {
			return fmt.Errorf("credstore: reading the credentials of credential type %d: %w", typeID, err)
		}
		for _, row := range rows {
			if err := s.purgeCredential(ctx, row, drop); err != nil {
				return err
			}
		}
		if len(rows) < purgePage {
			break
		}
		after = rows[len(rows)-1].ID
	}

	sources, err := db.CredentialInputSource.Query().
		Where(credentialinputsource.HasTargetCredentialWith(ofType)).
		Select(credentialinputsource.FieldID, credentialinputsource.FieldInputID).
		All(ctx)
	if err != nil {
		return fmt.Errorf("credstore: reading the input sources of credential type %d: %w", typeID, err)
	}
	var dropped []int
	for _, source := range sources {
		if drop(source.InputID) {
			dropped = append(dropped, source.ID)
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	if _, err := db.CredentialInputSource.Delete().Where(credentialinputsource.IDIn(dropped...)).Exec(ctx); err != nil {
		return fmt.Errorf("credstore: deleting the input sources of removed inputs of credential type %d: %w", typeID, err)
	}
	return nil
}

// purgeCredential rewrites one credential without the inputs and references
// drop names, writing nothing when it holds none.
func (s *entStore) purgeCredential(ctx context.Context, row *ent.Credential, drop func(string) bool) error {
	// The interceptor leaves a row it cannot decrypt sealed, and the edit is
	// refused rather than filtering the sealed map (unreadable).
	if sealed(row.Inputs) {
		return unreadable(row.ID, "they cannot be checked against this change")
	}
	inputs, inputsChanged := without(row.Inputs, drop)
	external, externalChanged := without(row.External, drop)
	if !inputsChanged && !externalChanged {
		return nil
	}
	update := s.db(ctx).Credential.UpdateOneID(row.ID)
	if inputsChanged {
		update.SetInputs(inputs)
	}
	if externalChanged {
		update.SetExternal(external)
	}
	if err := update.Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("credstore: deleting the removed inputs of credential %d: %w", row.ID, err)
	}
	return nil
}

// without returns m without the keys drop names, and whether that left any
// out.
func without(m map[string]string, drop func(string) bool) (map[string]string, bool) {
	if len(m) == 0 {
		return m, false
	}
	out := make(map[string]string, len(m))
	for id, value := range m {
		if !drop(id) {
			out[id] = value
		}
	}
	return out, len(out) != len(m)
}
