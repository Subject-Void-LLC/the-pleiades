package crypto

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/hook"
)

// EncryptedKeyMarker is the sentinel key an encrypted Device.properties
// map is replaced by: {"_encrypted": "<envelope string>"}. Its presence is
// what DeviceEnvelopePropertiesInterceptor checks to tell an
// already-encrypted map (never re-encrypted) from a plaintext one.
const EncryptedKeyMarker = "_encrypted"

// DeviceEnvelopePropertiesHook intercepts Device mutations to encrypt
// Properties, the JSONB bag that holds connection details (host, port,
// and any credential-shaped value a caller adds, such as a Cisco DevNet
// dynamic AAA login) — PLAN.md Section 17.2's target, not Fact.payload,
// which Section 22.4 defines as telemetry.
//
// Registered for ent.OpCreate, ent.OpUpdate, and ent.OpUpdateOne: unlike
// Fact.payload, Device.properties is not .Immutable() in the schema
// (internal/ent/schema/device.go). OpCreate covers fresh devices;
// OpUpdate covers both internal/inventory/ent_save.go's bulk Save path
// (the real production write path) and RotateDeviceProperties's own
// conditional per-row rewrite (rotate.go), which uses the bulk Update
// builder scoped to one row via a Where clause, not UpdateOneID, so its
// compare-and-swap write can be conditional on the row's stored version.
// OpUpdateOne is registered defensively even though no real caller uses
// it today, matching this package's Interceptor pattern rationale
// (PATTERNS.md): the interception point, not which builder a future
// caller happens to choose, is what must guarantee the invariant.
func DeviceEnvelopePropertiesHook(svc *EnvelopeService) ent.Hook {
	return hook.On(
		func(next ent.Mutator) ent.Mutator {
			return hook.DeviceFunc(func(ctx context.Context, m *ent.DeviceMutation) (ent.Value, error) {
				properties, exists := m.Properties()
				if exists && properties != nil {
					encrypted, err := encryptPropertiesMap(svc, properties)
					if err != nil {
						return nil, fmt.Errorf("failed to encrypt device properties: %w", err)
					}
					m.SetProperties(encrypted)
				}
				return next.Mutate(ctx, m)
			})
		},
		ent.OpCreate|ent.OpUpdate|ent.OpUpdateOne,
	)
}

// DeviceEnvelopePropertiesInterceptor intercepts Device queries to decrypt
// Properties transparently, the read-side counterpart of
// DeviceEnvelopePropertiesHook.
//
// Every ent-generated Device read this interceptor can ever see arrives as
// []*ent.Device, never a bare *ent.Device: Only/Get/OnlyX are themselves
// implemented as Limit(2).All(ctx) followed by a length check that reduces
// the slice to one node AFTER the interceptor chain has already run
// (confirmed against the generated code, internal/ent/device_query.go's
// own Only and internal/ent/client.go's own Get, not assumed). An earlier
// draft of this function type-switched on both shapes and hard-failed the
// slice case on the very first row's decrypt error, which an adversarial
// review of this phase caught as a real defect: one poisoned or corrupted
// row aborted the ENTIRE result set, breaking every other, perfectly
// healthy device in the same query -- including RotateDeviceProperties's
// own opening listing query (rotate.go), which could then never again make
// progress on any row. A single-result caller (Get/Only) is not exempt
// from that same risk either, since it is the identical []*ent.Device path
// underneath; there is no ent-level way to give it different treatment.
//
// A decrypt failure is logged and the affected device's Properties is left
// in its raw, still-encrypted shape (which isAlreadyEncryptedShape, used
// by RotateDeviceProperties, recognizes and itself skips re-writing)
// rather than either aborting the query or silently fabricating a
// decrypted-looking value.
func DeviceEnvelopePropertiesInterceptor(svc *EnvelopeService) ent.Interceptor {
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}

			result, ok := v.([]*ent.Device)
			if !ok {
				return v, nil
			}

			for _, dev := range result {
				if err := decryptDeviceProperties(svc, dev); err != nil {
					slog.Warn("failed to decrypt device properties, leaving encrypted",
						slog.String("device", dev.Name), slog.String("error", err.Error()))
				}
			}

			return v, nil
		})
	})
}

// decryptDeviceProperties decrypts dev.Properties in place if it carries
// EncryptedKeyMarker, leaving an already-plaintext (or nil) map untouched.
func decryptDeviceProperties(svc *EnvelopeService, dev *ent.Device) error {
	if dev.Properties == nil {
		return nil
	}
	decrypted, err := decryptPropertiesMap(svc, dev.Properties)
	if err != nil {
		return fmt.Errorf("failed to decrypt properties for device %s: %w", dev.Name, err)
	}
	dev.Properties = decrypted
	return nil
}

// isAlreadyEncryptedShape reports whether m has exactly the shape
// encryptPropertiesMap itself produces: a single key, EncryptedKeyMarker,
// holding a string value. This is deliberately strict, checked only on
// the write side (encryptPropertiesMap): checking mere presence of the
// marker key, as an earlier version of this failsafe did, let a
// caller-supplied property literally named EncryptedKeyMarker, alongside
// other real properties, cause the ENTIRE map — including any real
// secret sitting right next to it — to be persisted completely
// unencrypted, since the failsafe would see the marker and skip
// encryption of the whole map. That was a real, critical plaintext-leak
// defect an adversarial review caught before this phase shipped.
// Requiring the marker to be the map's only key closes it: any map
// containing more than the marker alone is always treated as ordinary
// plaintext and encrypted as a whole, even if one of its keys happens to
// share EncryptedKeyMarker's name.
//
// The read side (decryptPropertiesMap) deliberately does NOT use this
// same strict check: there, mere presence of the marker key is the
// correct, more lenient signal to attempt a decrypt, so that a
// corrupted or wrongly-shaped stored value (which, after this fix, this
// package's own hook can never again produce) fails loudly with a clear
// decrypt/type error instead of being silently passed through as if it
// were ordinary plaintext.
func isAlreadyEncryptedShape(m map[string]interface{}) bool {
	if len(m) != 1 {
		return false
	}
	marker, ok := m[EncryptedKeyMarker]
	if !ok {
		return false
	}
	_, isString := marker.(string)
	return isString
}

// encryptPropertiesMap and decryptPropertiesMap are entity-agnostic:
// they operate on the map[string]interface{} shape both Device.properties
// and (were it ever needed again) Fact.payload share, so a future entity
// needing the same treatment is a thin Mutate/Query adapter around these,
// not a second implementation.
func encryptPropertiesMap(svc *EnvelopeService, m map[string]interface{}) (map[string]interface{}, error) {
	if isAlreadyEncryptedShape(m) {
		// Should not happen on a fresh mutate, but is a cheap failsafe
		// against double-encrypting a value that somehow already carries
		// the exact shape this function itself produces (see
		// isAlreadyEncryptedShape's own doc comment for why checking mere
		// key presence, not this exact shape, was a real, critical
		// plaintext-leak defect an adversarial review caught).
		return m, nil
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal properties for encryption: %w", err)
	}

	ciphertext, err := svc.Encrypt(raw)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{EncryptedKeyMarker: ciphertext}, nil
}

func decryptPropertiesMap(svc *EnvelopeService, m map[string]interface{}) (map[string]interface{}, error) {
	marker, isEncrypted := m[EncryptedKeyMarker]
	if !isEncrypted {
		return m, nil
	}

	ciphertext, ok := marker.(string)
	if !ok {
		return nil, fmt.Errorf("encrypted properties marker is not a string")
	}

	plaintext, err := svc.Decrypt(ciphertext)
	if err != nil {
		return nil, err
	}

	var decrypted map[string]interface{}
	if err := json.Unmarshal(plaintext, &decrypted); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decrypted properties: %w", err)
	}
	return decrypted, nil
}
