// Package crypto: envelope encryption for Fact.payload, which holds a stored
// fact's key and value (internal/facts' atrest.go, the owner's decision of
// 2026-11-19).
//
// It is the bound envelope the credential and mesh signing key columns use,
// with one difference in who seals. Those columns are sealed by their write
// hook and opened by an interceptor, so a caller never sees a ciphertext. A
// fact's store (internal/facts/entstore) seals and opens its payload itself,
// through SealFactPayload and OpenFactPayload, and refuses to work without a
// service to do it with: a store that leaned on a hook would write plaintext
// through any client the hook was never installed on, which is every test
// client and any composition root that forgets. FactPayloadHook is the second
// line behind that: it seals a payload that reaches the table unsealed, and
// refuses an update that would replace a sealed payload with anything else.
//
// # The binding
//
// The associated data is the row's key_token. The token is HMAC-SHA-256 over
// the device's ID and the fact's key under the store's token key, so it is
// stable for the life of the row, is not a secret, and already names the one
// device and key the row belongs to. A payload copied onto a row with another
// token does not open. A whole row moved to another device still opens, since
// its token moved with it, and is caught one step later by the store, which
// holds the key it read to the token the row carries and the device it was
// asked for (entstore's open).
//
// # Why the payload has a setter at all
//
// A fact row is insert-only, and every other field of it is immutable in the
// schema. Rotating MASTER_ENCRYPTION_KEY has to seal each payload again under
// the new key, and a change of the facts key has to seal it again for the
// row's new token (internal/facts/entstore's rekey.go, which rewrites the
// token itself through internal/ent's fact_rekey.go): RotateFactPayloads and
// that re-key are the two writers, the hook refuses an update whose payload
// is not a sealed envelope, and the device's hash chain covers the key and
// value the payload holds, so a payload replaced by another sealed one is
// found by a chain check. RotateFactPayloads writes only while the row keeps
// the token it read, so the two cannot undo each other.
package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/hook"
)

// factBindingLabel separates a fact payload's associated data from any other
// use of a token as associated data.
const factBindingLabel = "pleiades/fact-payload/v1\x00"

// factRotationPage is how many fact rows one rotation query reads. A fleet's
// facts are millions of rows, far more than fit in memory at once.
const factRotationPage = 500

// ErrFactNotSealed marks a fact payload that is not a sealed envelope: a row
// some writer stored in plaintext, which no reader accepts.
var ErrFactNotSealed = errors.New("crypto: a stored fact's payload is not sealed")

// ErrFactPayloadUpdate is returned when an update would replace a fact's
// payload with something that is not a sealed envelope. Only a master key
// rotation and a change of the facts key write a payload again, and each
// writes it sealed.
var ErrFactPayloadUpdate = errors.New("crypto: a stored fact's payload can only be replaced by the same payload sealed again")

// factBinding is the associated data of the payload of the row keyToken
// names.
func factBinding(keyToken string) []byte {
	return []byte(factBindingLabel + keyToken)
}

// sealedFactEnvelope is the envelope a fact payload holds, when it is the
// sealed form: one entry, named EncryptedKeyMarker, holding a bound envelope.
func sealedFactEnvelope(payload map[string]interface{}) (string, bool) {
	if len(payload) != 1 {
		return "", false
	}
	envelope, ok := payload[EncryptedKeyMarker].(string)
	if !ok || !IsBoundEnvelope(envelope) {
		return "", false
	}
	return envelope, true
}

// FactPayloadVersion is the version tag of the master key a sealed fact
// payload names, read from its envelope's head without opening it: the
// version a service must hold a key for to open it (resolveKEK). It is false
// for a payload that is not the sealed form. The tag is an identifier, stored
// in the clear, never part of a key.
func FactPayloadVersion(payload map[string]interface{}) (string, bool) {
	envelope, ok := sealedFactEnvelope(payload)
	if !ok {
		return "", false
	}
	// A bound envelope has four $-separated fields, the version first.
	version, _, _ := strings.Cut(envelope, "$")
	return version, true
}

// SealFactPayload seals a fact's payload (its key and its value's canonical
// JSON) for the row keyToken names, as the one-entry object the column holds.
func SealFactPayload(svc *EnvelopeService, keyToken string, payload map[string]interface{}) (map[string]interface{}, error) {
	if svc == nil {
		return nil, errors.New("crypto: no envelope service to seal a fact with")
	}
	if keyToken == "" {
		return nil, errors.New("crypto: a fact with no key token cannot have its payload bound to it")
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("crypto: encoding a fact's payload: %w", err)
	}
	envelope, err := svc.EncryptBound(plaintext, factBinding(keyToken))
	if err != nil {
		return nil, fmt.Errorf("crypto: sealing a fact's payload: %w", err)
	}
	return map[string]interface{}{EncryptedKeyMarker: envelope}, nil
}

// OpenFactPayload opens the payload of the row keyToken names. A payload
// that is not sealed is ErrFactNotSealed, never read as it stands; one that
// does not open (another row's, another key's, or edited) is an error.
func OpenFactPayload(svc *EnvelopeService, keyToken string, payload map[string]interface{}) (map[string]interface{}, error) {
	if svc == nil {
		return nil, errors.New("crypto: no envelope service to open a fact with")
	}
	envelope, ok := sealedFactEnvelope(payload)
	if !ok {
		return nil, ErrFactNotSealed
	}
	plaintext, err := svc.DecryptBound(envelope, factBinding(keyToken))
	if err != nil {
		return nil, err
	}
	var opened map[string]interface{}
	if err := json.Unmarshal(plaintext, &opened); err != nil {
		return nil, fmt.Errorf("crypto: a fact's payload opened to something that is not its key and value: %w", err)
	}
	return opened, nil
}

// FactPayloadHook keeps plaintext out of Fact.payload whatever client wrote
// the row: a payload created unsealed is sealed here, bound to the row's
// token, and an update may only set a payload that is already sealed.
func FactPayloadHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.FactFunc(func(ctx context.Context, m *ent.FactMutation) (ent.Value, error) {
				payload, exists := m.Payload()
				if !exists {
					return next.Mutate(ctx, m)
				}
				if _, sealed := sealedFactEnvelope(payload); sealed {
					return next.Mutate(ctx, m)
				}
				if !m.Op().Is(ent.OpCreate) {
					return nil, ErrFactPayloadUpdate
				}
				token, _ := m.KeyToken()
				sealed, err := SealFactPayload(svc, token, payload)
				if err != nil {
					return nil, err
				}
				m.SetPayload(sealed)
				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// RotateFactPayloads seals every Fact row's payload again under svc's
// current key and version.
//
// It reads the table a page at a time, in id order. A row already under the
// current version, which opened under the current key, is left as it is and
// counted nowhere, so a second pass over a rotated table writes nothing. A
// row that opens under neither configured key, or that is not sealed at all,
// is counted unreadable and left alone. The update changes the payload
// alone; the hook above refuses anything but a sealed one.
func RotateFactPayloads(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	var count RotationCount
	after := 0
	for {
		rows, err := client.Fact.Query().
			Where(fact.IDGT(after)).
			Order(ent.Asc(fact.FieldID)).
			Limit(factRotationPage).
			All(ctx)
		if err != nil {
			return RotationCount{}, fmt.Errorf("failed to list facts for key rotation: %w", err)
		}
		if len(rows) == 0 {
			return count, nil
		}
		for _, row := range rows {
			after = row.ID
			opened, err := OpenFactPayload(svc, row.KeyToken, row.Payload)
			if err != nil {
				count.Unreadable++
				// Named by row, never by token or content.
				slog.Warn("skipping a fact whose payload does not open during key rotation; this is either a corrupted row or a ciphertext that does not belong to it",
					slog.Int("fact", row.ID))
				continue
			}
			if envelope, _ := sealedFactEnvelope(row.Payload); strings.HasPrefix(envelope, svc.currentVersion+"$") {
				continue
			}
			sealed, err := SealFactPayload(svc, row.KeyToken, opened)
			if err == nil {
				// Only while the row still has the token the payload was
				// sealed for: a change of the facts key (internal/facts/
				// entstore's rekey.go) may re-key the row between the read
				// and this write, and its payload, sealed for the new token,
				// must not be replaced by one sealed for the old.
				err = client.Fact.UpdateOneID(row.ID).Where(fact.KeyTokenEQ(row.KeyToken)).SetPayload(sealed).Exec(ctx)
			}
			if err != nil {
				count.Skipped++
				slog.Warn("failed to rotate a fact's payload, will retry on a later pass",
					slog.Int("fact", row.ID), slog.String("error", err.Error()))
				continue
			}
			count.Rotated++
		}
	}
}
