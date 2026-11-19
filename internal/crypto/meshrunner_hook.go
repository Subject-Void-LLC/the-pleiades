// Package crypto: envelope encryption for MeshSigningKey.runner_seed, the
// seed of the scoped signing key that issues Runner credentials (0.5.0
// broker-enforced Runner identity).
//
// It is the mesh seed's scheme exactly: the same BOUND envelope, the same
// row binding (secret_binding, resolved by meshSigningKeyBinding), the same
// idempotent write and the same decrypt-or-leave-sealed read. The reason it
// is a separate field and not a second value in seed is isolation: a stolen
// Runner signer mints Runner credentials only, so the two seeds must be
// separately stored, not derived from each other.
package crypto

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/meshsigningkey"
)

// MeshRunnerSeedHook encrypts MeshSigningKey.runner_seed on write, binding
// the ciphertext to the row, as MeshSigningKeySeedHook does for seed.
func MeshRunnerSeedHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.MeshSigningKeyFunc(func(ctx context.Context, m *ent.MeshSigningKeyMutation) (ent.Value, error) {
				seed, exists := m.RunnerSeed()
				if !exists || seed == "" {
					return next.Mutate(ctx, m)
				}
				if IsBoundEnvelope(seed) {
					return next.Mutate(ctx, m)
				}
				binding, err := meshSigningKeyBinding(ctx, m)
				if err != nil {
					return nil, err
				}
				sealed, err := svc.EncryptBound([]byte(seed), []byte(binding))
				if err != nil {
					return nil, fmt.Errorf("failed to encrypt mesh runner signing seed: %w", err)
				}
				m.SetRunnerSeed(sealed)
				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// MeshRunnerSeedInterceptor decrypts runner_seed on read. A row it cannot
// open is left sealed and logged, as MeshSigningKeySeedInterceptor leaves
// seed; meshid.NewRunnerIssuer then refuses a sealed seed, which is where it
// should fail.
func MeshRunnerSeedInterceptor(svc *EnvelopeService) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}
			result, ok := v.([]*ent.MeshSigningKey)
			if !ok {
				return v, nil
			}
			for _, row := range result {
				if row.RunnerSeed == "" || !IsBoundEnvelope(row.RunnerSeed) {
					continue
				}
				if row.SecretBinding == "" {
					slog.Warn("mesh signing key has a sealed runner seed but no secret binding to open it with",
						slog.Int("mesh_signing_key", row.ID), slog.String("key_id", row.KeyID))
					continue
				}
				plaintext, err := svc.DecryptBound(row.RunnerSeed, []byte(row.SecretBinding))
				if err != nil {
					slog.Warn("failed to decrypt mesh runner signing seed, leaving sealed; this is either a corrupted row or a ciphertext that does not belong to it",
						slog.Int("mesh_signing_key", row.ID), slog.String("key_id", row.KeyID), slog.String("error", err.Error()))
					continue
				}
				row.RunnerSeed = string(plaintext)
			}
			return v, nil
		})
	})
}

// RotateMeshRunnerSeeds re-encrypts runner_seed under the current master key,
// as RotateMeshSigningKeySeeds does for seed. A row whose runner seed cannot
// be opened is counted as unreadable and left alone.
func RotateMeshRunnerSeeds(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	rows, err := client.MeshSigningKey.Query().Where(meshsigningkey.RunnerSeedNEQ("")).All(ctx)
	if err != nil {
		return RotationCount{}, fmt.Errorf("failed to list mesh signing keys for runner seed rotation: %w", err)
	}
	var count RotationCount
	for _, row := range rows {
		if IsBoundEnvelope(row.RunnerSeed) {
			count.Unreadable++
			slog.Warn("skipping mesh runner signing seed with an undecryptable value during key rotation",
				slog.String("key_id", row.KeyID))
			continue
		}
		_, err := client.MeshSigningKey.UpdateOneID(row.ID).
			Where(meshsigningkey.UpdatedAtEQ(row.UpdatedAt)).
			SetRunnerSeed(row.RunnerSeed).
			Save(ctx)
		switch {
		case err == nil:
			count.Rotated++
		case ent.IsNotFound(err):
			count.Skipped++
			slog.Warn("mesh signing key changed concurrently during runner seed rotation, will retry on a later pass",
				slog.String("key_id", row.KeyID))
		default:
			count.Skipped++
			slog.Warn("failed to rotate mesh runner signing seed, will retry on a later pass",
				slog.String("key_id", row.KeyID), slog.String("error", err.Error()))
		}
	}
	return count, nil
}
