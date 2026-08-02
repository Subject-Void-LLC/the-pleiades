package crypto

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/hook"
)

const EncryptedKeyMarker = "_encrypted"

// EnvelopeEncryptionHook intercepts Fact mutations to encrypt the Payload.
// It serializes the entire JSON payload, encrypts it, and stores it as {"_encrypted": "base64..."}
func EnvelopeEncryptionHook(svc Service) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.FactFunc(func(ctx context.Context, m *ent.FactMutation) (ent.Value, error) {
				payload, exists := m.Payload()
				if exists && payload != nil {
					// Check if it's already encrypted (shouldn't happen on fresh mutate, but good failsafe)
					if _, isEncrypted := payload[EncryptedKeyMarker]; !isEncrypted {
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, fmt.Errorf("failed to marshal payload for encryption: %w", err)
						}

						ciphertext, err := svc.Encrypt(raw)
						if err != nil {
							return nil, fmt.Errorf("failed to encrypt payload: %w", err)
						}

						encryptedPayload := map[string]interface{}{
							EncryptedKeyMarker: base64.StdEncoding.EncodeToString(ciphertext),
						}
						m.SetPayload(encryptedPayload)
					}
				}

				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// EnvelopeDecryptionInterceptor intercepts Fact queries to decrypt the Payload transparently.
func EnvelopeDecryptionInterceptor(svc Service) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			// Execute the query first
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			// We must type-assert the result because ent.Value is an empty interface.
			// Depending on the query type (All, First, Only), it could be a slice or a single pointer.
			switch result := v.(type) {
			case []*ent.Fact:
				for _, fact := range result {
					if err := decryptFactPayload(svc, fact); err != nil {
						return nil, err
					}
				}
			case *ent.Fact:
				if err := decryptFactPayload(svc, result); err != nil {
					return nil, err
				}
			}

			return v, nil
		})
	})
}

func decryptFactPayload(svc Service, f *ent.Fact) error {
	if f.Payload == nil {
		return nil
	}

	encryptedBase64, isEncrypted := f.Payload[EncryptedKeyMarker]
	if !isEncrypted {
		return nil // Not encrypted, leave as is
	}

	encodedStr, ok := encryptedBase64.(string)
	if !ok {
		return fmt.Errorf("encrypted payload marker is not a string")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encodedStr)
	if err != nil {
		return fmt.Errorf("failed to decode base64 ciphertext: %w", err)
	}

	plaintext, err := svc.Decrypt(ciphertext)
	if err != nil {
		return fmt.Errorf("failed to decrypt payload: %w", err)
	}

	var originalPayload map[string]interface{}
	if err := json.Unmarshal(plaintext, &originalPayload); err != nil {
		return fmt.Errorf("failed to unmarshal decrypted JSON: %w", err)
	}

	f.Payload = originalPayload
	return nil
}
