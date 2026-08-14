package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// Envelope encryption for Credential.inputs, using the BOUND form.
//
// This is the third entity to get encryption at rest here and the first to
// need the binding. Device.properties and SavedLaunchConfig.answers use the
// unbound envelope, which is safe against a database dump and not against a
// database writer who relocates one row's ciphertext onto another. That was
// accepted for those two. It is not acceptable here: relocating one
// organization's credential inputs onto another organization's credential
// makes the platform inject the first organization's secrets into the second
// organization's jobs, and the attacker never reads anything.
// envelope_bound.go carries the full reasoning and the test that performs
// the relocation.
//
// The binding is Credential.secret_binding, an immutable per-row UUID. Where
// it comes from differs by operation, which is why this hook registers for
// two operations and refuses a third:
//
//   - Create: ent's defaults() runs before hooks, so the DefaultFunc-
//     generated UUID is already in the mutation.
//   - UpdateOne: the row exists, so OldSecretBinding loads it.
//   - Update (bulk): refused outright when it touches inputs. Every row has
//     its own binding, so one encrypted value written across many rows could
//     be correct for at most one of them. This is not a limitation being
//     worked around; a bulk update of credential inputs is meaningless.

// ErrBulkCredentialInputs is returned when a bulk update tries to set
// inputs across many rows at once.
var ErrBulkCredentialInputs = errors.New("crypto: credential inputs cannot be set by a bulk update, because each row's ciphertext is bound to that row")

// CredentialInputsHook encrypts Credential.inputs on write, binding the
// ciphertext to the row.
func CredentialInputsHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.CredentialFunc(func(ctx context.Context, m *ent.CredentialMutation) (ent.Value, error) {
				inputs, exists := m.Inputs()
				if !exists || inputs == nil {
					return next.Mutate(ctx, m)
				}

				binding, err := credentialBinding(ctx, m)
				if err != nil {
					return nil, err
				}

				encrypted, err := encryptCredentialInputs(svc, inputs, binding)
				if err != nil {
					return nil, fmt.Errorf("failed to encrypt credential inputs: %w", err)
				}
				m.SetInputs(encrypted)

				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// credentialBinding resolves the associated data for this mutation.
func credentialBinding(ctx context.Context, m *ent.CredentialMutation) (string, error) {
	switch {
	case m.Op().Is(ent.OpCreate):
		// defaults() ran before this hook, so the generated UUID is here.
		binding, exists := m.SecretBinding()
		if !exists || binding == "" {
			return "", errors.New("crypto: credential has no secret binding on create, so its inputs cannot be bound to it")
		}
		return binding, nil

	case m.Op().Is(ent.OpUpdateOne):
		binding, err := m.OldSecretBinding(ctx)
		if err != nil {
			return "", fmt.Errorf("crypto: cannot read the credential's secret binding: %w", err)
		}
		if binding == "" {
			return "", errors.New("crypto: credential has no secret binding, so its inputs cannot be bound to it")
		}
		return binding, nil

	default:
		// A bulk update. See this file's own comment: one ciphertext
		// across many rows could be correct for at most one of them, so
		// this fails loudly rather than writing something wrong.
		return "", ErrBulkCredentialInputs
	}
}

// CredentialInputsInterceptor decrypts inputs on read.
//
// A decrypt failure is logged and that row's inputs are left in their raw,
// still-encrypted shape rather than aborting the query, for the reason the
// Device interceptor states at length: results arrive as a slice even for
// Only and Get, so hard-failing on the first bad row would break every
// healthy row in the same query. Here that means one unreadable credential
// does not make an organization's whole credential list unloadable.
//
// The failure is worth reading carefully in the log when it happens, though,
// and the message says why: with the bound envelope, a decrypt failure is
// either a corrupted row OR a ciphertext that does not belong to this row.
// The second is an attack. Nothing here can tell them apart, which is
// correct, so both get the same warning and neither is dismissed.
func CredentialInputsInterceptor(svc *EnvelopeService) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			result, ok := v.([]*ent.Credential)
			if !ok {
				return v, nil
			}

			for _, cred := range result {
				if cred.Inputs == nil {
					continue
				}
				decrypted, err := decryptCredentialInputs(svc, cred.Inputs, cred.SecretBinding)
				if err != nil {
					slog.Warn("failed to decrypt credential inputs, leaving encrypted; this is either a corrupted row or a ciphertext that does not belong to it",
						slog.Int("credential", cred.ID), slog.String("error", err.Error()))
					continue
				}
				cred.Inputs = decrypted
			}

			return v, nil
		})
	})
}

// encryptCredentialInputs seals a whole input map into a single-entry map
// keyed by EncryptedKeyMarker.
//
// The WHOLE map rather than per field, for the reason
// internal/ent/schema/credential.go states: a mutation hook cannot join to
// the credential's type to learn which input ids are secret, so a per-field
// scheme would need the hook to query mid-mutation or the caller to pass
// the answer in, and both are places to get it wrong per row.
//
// An already-encrypted map is passed through untouched. That is what makes
// the hook idempotent, which matters because a caller reading a credential
// (decrypted by the interceptor) and writing it back must not double-seal,
// and a caller that never read it must not have its plaintext skipped.
func encryptCredentialInputs(svc *EnvelopeService, inputs map[string]string, binding string) (map[string]string, error) {
	if _, already := inputs[EncryptedKeyMarker]; already {
		return inputs, nil
	}
	if len(inputs) == 0 {
		return inputs, nil
	}

	plaintext, err := marshalStringMap(inputs)
	if err != nil {
		return nil, err
	}

	sealed, err := svc.EncryptBound(plaintext, []byte(binding))
	if err != nil {
		return nil, err
	}
	return map[string]string{EncryptedKeyMarker: sealed}, nil
}

// decryptCredentialInputs reverses encryptCredentialInputs, tolerating a
// map that is already plaintext.
//
// Tolerating plaintext is not laxness. It is what lets this be turned on
// against a database that already has rows, and it mirrors what
// decryptPropertiesMap already does for Device: a map without the marker is
// left exactly as it is.
func decryptCredentialInputs(svc *EnvelopeService, stored map[string]string, binding string) (map[string]string, error) {
	sealed, encrypted := stored[EncryptedKeyMarker]
	if !encrypted {
		return stored, nil
	}
	if binding == "" {
		return nil, errors.New("crypto: credential has encrypted inputs but no secret binding to open them with")
	}

	plaintext, err := svc.DecryptBound(sealed, []byte(binding))
	if err != nil {
		return nil, err
	}
	return unmarshalStringMap(plaintext)
}

// marshalStringMap encodes an input map for sealing.
//
// A dedicated pair rather than reusing the Device path's helpers, because
// those work on map[string]interface{} and a credential's inputs are
// map[string]string. Converting between the two to share four lines would
// mean a round trip through interface{} on a path that handles secrets,
// which is exactly where AGENTS.md's typing rule earns its keep.
func marshalStringMap(m map[string]string) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to encode credential inputs: %w", err)
	}
	return raw, nil
}

// unmarshalStringMap decodes what marshalStringMap produced.
func unmarshalStringMap(plaintext []byte) (map[string]string, error) {
	var out map[string]string
	if err := json.Unmarshal(plaintext, &out); err != nil {
		return nil, fmt.Errorf("crypto: failed to decode credential inputs: %w", err)
	}
	return out, nil
}
