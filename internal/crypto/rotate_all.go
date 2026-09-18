// Running every rotation pass, and reporting what each one did and did not do.
package crypto

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
)

// RotationCount is what one rotation pass did to one table.
//
// It exists because a bare count of rows converted cannot answer the one
// question an operator rotating a key has to answer before removing the old
// one: does any row still need it? A pass that converted 40 rows says
// nothing about the 3 it could not write, and those 3 are exactly the rows
// that become unreadable the moment the previous key is taken away.
type RotationCount struct {
	// Rotated rows were re-encrypted under the current key.
	Rotated int

	// Skipped rows were readable but were not rewritten: a concurrent
	// writer won the compare-and-swap, or the write itself failed. They
	// are still on whatever key they were on, which may be the previous
	// one, and a later pass picks them up.
	Skipped int

	// Unreadable rows opened under neither configured key, or failed the
	// check that binds a ciphertext to its own row. Removing the previous
	// key does not change them, because it could not open them either.
	Unreadable int
}

// Complete reports whether no row this pass read is still waiting on a
// rewrite. Only a pass with nothing skipped leaves nothing that could still
// depend on the previous key.
func (c RotationCount) Complete() bool { return c.Skipped == 0 }

// TableRotation is one table's result inside a full rotation.
type TableRotation struct {
	// Table names the rows in plain words, for a log line an operator
	// reads: "credentials", not "credential.inputs".
	Table string

	// Count is what the pass did to them.
	Count RotationCount
}

// RotateAll runs every rotation pass and reports each table's result.
//
// Before this existed the controller ran the device pass alone, while the
// production guide told operators there was one pass per entity and to drop
// the old key once each had reported. Following that guide lost every
// credential and every saved survey answer, because those two passes were
// written, tested, and never called. The passes come from encryptedColumns,
// the one list of sealed columns this package keeps, and
// TestEveryEncryptedColumnIsListed pairs that list with the hooks this
// package exports, so a fifth encrypted column cannot land without a pass.
//
// A pass that fails to list its table does not stop the others. Its error is
// joined into the returned error and its table is absent from the result,
// which is the honest shape: that table's rows were not looked at, so no
// count for them would be true.
func RotateAll(ctx context.Context, client *ent.Client, svc *EnvelopeService) ([]TableRotation, error) {
	results := make([]TableRotation, 0, len(encryptedColumns))
	var errs []error
	for _, col := range encryptedColumns {
		count, err := col.rotate(ctx, client, svc)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", col.noun, err))
			continue
		}
		results = append(results, TableRotation{Table: col.noun, Count: count})
	}
	return results, errors.Join(errs...)
}

// RotateMeshSigningKeySeeds re-encrypts every MeshSigningKey row's seed under
// svc's current key and version.
//
// client must already have MeshSigningKeySeedHook and
// MeshSigningKeySeedInterceptor installed with svc. It follows
// RotateCredentialInputs in every respect that matters: UpdateOne because
// the hook refuses a bulk write, and a compare-and-swap on updated_at
// because this entity has no version column either.
//
// A row whose seed the interceptor could not open comes back still sealed,
// which IsBoundEnvelope recognises, because a plaintext nkey seed can never
// contain the "$" the envelope format separates its fields with.
//
// Nothing in this platform writes a seed today, so on a production database
// this pass finds no rows. It exists so the day something does write one,
// rotating the master key does not quietly strand it.
func RotateMeshSigningKeySeeds(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	rows, err := client.MeshSigningKey.Query().All(ctx)
	if err != nil {
		return RotationCount{}, fmt.Errorf("failed to list mesh signing keys for key rotation: %w", err)
	}

	var count RotationCount
	for _, row := range rows {
		if row.Seed == "" {
			continue
		}
		if IsBoundEnvelope(row.Seed) {
			count.Unreadable++
			slog.Warn("skipping mesh signing key with an undecryptable seed during key rotation; this is either a corrupted row or a ciphertext that does not belong to it",
				slog.String("key_id", row.KeyID))
			continue
		}

		_, err := client.MeshSigningKey.UpdateOneID(row.ID).
			Where(meshsigningkey.UpdatedAtEQ(row.UpdatedAt)).
			SetSeed(row.Seed).
			Save(ctx)
		switch {
		case err == nil:
			count.Rotated++
		case ent.IsNotFound(err):
			count.Skipped++
			slog.Warn("mesh signing key changed concurrently during key rotation, will retry on a later pass",
				slog.String("key_id", row.KeyID))
		default:
			count.Skipped++
			slog.Warn("failed to rotate mesh signing key seed, will retry on a later pass",
				slog.String("key_id", row.KeyID), slog.String("error", err.Error()))
		}
	}

	return count, nil
}
