package crypto

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// Envelope encryption for MeshSigningKey.seed, using the BOUND form.
//
// This is the fourth entity to get encryption at rest and the second to
// need the binding. It needs it for a sharper reason than Credential does.
// Relocating one credential's ciphertext onto another row makes the
// platform inject the wrong secret into a job, which is bad. Relocating a
// SIGNING KEY's ciphertext onto another row makes the platform mint
// credentials that a different account trusts, which is not a leak of one
// secret but the manufacture of new identities. The attacker still reads
// nothing.
//
// The binding is MeshSigningKey.secret_binding, an immutable per-row UUID,
// resolved exactly as credential_hook.go resolves its own: from the
// mutation on Create (ent's defaults() runs before hooks), from
// OldSecretBinding on UpdateOne, and refused on a bulk Update.
//
// # Why the marker is the envelope itself rather than a map key
//
// Credential.inputs is a map, so an already-sealed value is recognised by a
// reserved key inside it. A seed is one string, so there is nowhere to put
// a marker, and the envelope's own shape is the marker instead:
// IsBoundEnvelope reads the algorithm tag off the wire format.
//
// That discrimination is safe rather than merely convenient, and it is
// worth saying why it is safe HERE specifically. A plaintext value in this
// column is always an nkey seed: it begins with "S", is base32, and can
// never contain "$", which the envelope format uses as its field
// separator. So no plaintext seed can be mistaken for an envelope, and no
// envelope can be mistaken for a seed. A column whose plaintext could
// contain "$" would need a different test.

// ErrBulkMeshSigningKeySeed is returned when a bulk update tries to set
// seeds across many rows at once.
var ErrBulkMeshSigningKeySeed = errors.New("crypto: mesh signing key seeds cannot be set by a bulk update, because each row's ciphertext is bound to that row")

// MeshSigningKeySeedHook encrypts MeshSigningKey.seed on write, binding
// the ciphertext to the row.
func MeshSigningKeySeedHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.MeshSigningKeyFunc(func(ctx context.Context, m *ent.MeshSigningKeyMutation) (ent.Value, error) {
				seed, exists := m.Seed()
				if !exists || seed == "" {
					return next.Mutate(ctx, m)
				}

				// Idempotent: a caller that read the row (decrypted by the
				// interceptor) and writes it back must not double-seal,
				// and a caller that never read it must not have its
				// plaintext skipped.
				if IsBoundEnvelope(seed) {
					return next.Mutate(ctx, m)
				}

				binding, err := meshSigningKeyBinding(ctx, m)
				if err != nil {
					return nil, err
				}

				sealed, err := svc.EncryptBound([]byte(seed), []byte(binding))
				if err != nil {
					return nil, fmt.Errorf("failed to encrypt mesh signing key seed: %w", err)
				}
				m.SetSeed(sealed)

				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// meshSigningKeyBinding resolves the associated data for this mutation.
func meshSigningKeyBinding(ctx context.Context, m *ent.MeshSigningKeyMutation) (string, error) {
	switch {
	case m.Op().Is(ent.OpCreate):
		binding, exists := m.SecretBinding()
		if !exists || binding == "" {
			return "", errors.New("crypto: mesh signing key has no secret binding on create, so its seed cannot be bound to it")
		}
		return binding, nil

	case m.Op().Is(ent.OpUpdateOne):
		binding, err := m.OldSecretBinding(ctx)
		if err != nil {
			return "", fmt.Errorf("crypto: cannot read the mesh signing key's secret binding: %w", err)
		}
		if binding == "" {
			return "", errors.New("crypto: mesh signing key has no secret binding, so its seed cannot be bound to it")
		}
		return binding, nil

	default:
		return "", ErrBulkMeshSigningKeySeed
	}
}

// MeshSigningKeySeedInterceptor decrypts seed on read.
//
// A decrypt failure leaves that row's seed in its sealed form and logs,
// rather than failing the query, for the reason the Credential interceptor
// states: results arrive as a slice even for Only and Get, so hard-failing
// on one bad row would break every healthy row in the same query.
//
// The consequence here is more specific than elsewhere, so it is stated
// rather than inherited: a row left sealed will fail later at
// meshid.NewIssuer, which refuses anything that is not a valid account
// seed. That is the correct place for it to fail, because by then the
// error can name which key and what was expected, whereas a query-time
// abort would only say a list could not be loaded.
func MeshSigningKeySeedInterceptor(svc *EnvelopeService) ent.Interceptor {
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
				if row.Seed == "" || !IsBoundEnvelope(row.Seed) {
					continue
				}
				if row.SecretBinding == "" {
					slog.Warn("mesh signing key has a sealed seed but no secret binding to open it with",
						slog.Int("mesh_signing_key", row.ID), slog.String("key_id", row.KeyID))
					continue
				}
				plaintext, err := svc.DecryptBound(row.Seed, []byte(row.SecretBinding))
				if err != nil {
					slog.Warn("failed to decrypt mesh signing key seed, leaving sealed; this is either a corrupted row or a ciphertext that does not belong to it",
						slog.Int("mesh_signing_key", row.ID), slog.String("key_id", row.KeyID), slog.String("error", err.Error()))
					continue
				}
				row.Seed = string(plaintext)
			}

			return v, nil
		})
	})
}
