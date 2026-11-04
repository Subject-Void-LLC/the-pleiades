// Package facts: keyed digests (Phase 41c). A digest of a file that is not
// world-readable is an offline guessing oracle for a small secret, so a method
// hands such a digest over under one reserved field (RawDigestField), and
// nothing keeps or shows it there: on the Crawl tier the engine replaces it at
// once with HMAC-SHA-256 under a key derived from the project's master key; on
// the Walk tier the Runner, which holds no such key, seals it to the
// Controller's public key (SealedDigestField) and the Controller opens and keys
// it before storing. Prepare drops a raw digest wherever one is left, and a
// sealed one wherever sealing is not expected, so a path that forgets this
// stores nothing of it rather than the digest itself. Two runs over an unchanged
// file give the same keyed value, so drift still shows; a reader without the
// key can test no guess against it.
package facts

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// The reserved fields of a fact's object value that carry a digest.
const (
	// RawDigestField holds a SHA-256 (hex) the method read, which must be
	// keyed before anything keeps or shows it.
	RawDigestField = "sha256_raw"
	// SealedDigestField holds a raw digest sealed to the Controller's public
	// key by a Runner, which only the Controller opens.
	SealedDigestField = "sha256_sealed"
	// KeyedDigestField holds the keyed digest a store keeps.
	KeyedDigestField = "hmac_sha256"
	// KeyedDigestKeyField names the key a digest was keyed under, so a key's
	// rotation reads as a key change, not as the file changing.
	KeyedDigestKeyField = "hmac_key"
)

// keyedDigestLabel separates the HMAC key from the secret it is derived from,
// so the same secret keys nothing else the same way.
const keyedDigestLabel = "pleiades/facts/keyed-digest/v1"

// keyIDLabel separates a key's printed identity from any digest under it.
const keyIDLabel = "pleiades/facts/keyed-digest/key-id"

// Keyer keys raw digests for one store. Build one with NewKeyer, or with
// NewLazyKeyer to read its secret only when a digest needs keying.
type Keyer struct {
	once    sync.Once
	resolve func() ([]byte, error)
	source  string
	key     []byte
	id      string
	err     error
}

// NewKeyer derives the keyed-digest key from secret (at least 32 bytes: a
// project's master key, or a Controller's dedicated digest key), so the secret
// itself never keys a digest. source names where the secret came from
// (project, controller), and leads the key's identity.
func NewKeyer(secret []byte, source string) (*Keyer, error) {
	k := NewLazyKeyer(func() ([]byte, error) { return secret, nil }, source)
	if err := k.load(); err != nil {
		return nil, err
	}
	return k, nil
}

// NewLazyKeyer is a Keyer that calls resolve for its secret the first time a
// digest needs keying, and never otherwise, as a project's credential store
// reads its master key only when a credential is used. A secret that cannot be
// read keys nothing: each raw digest is then dropped, never kept.
func NewLazyKeyer(resolve func() ([]byte, error), source string) *Keyer {
	return &Keyer{resolve: resolve, source: source}
}

// load reads and derives the key, once.
func (k *Keyer) load() error {
	k.once.Do(func() {
		secret, err := k.resolve()
		if err != nil {
			k.err = fmt.Errorf("facts: reading the keyed-digest secret: %w", err)
			return
		}
		if len(secret) < 32 {
			k.err = fmt.Errorf("facts: a keyed-digest secret must be at least 32 bytes, got %d", len(secret))
			return
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(keyedDigestLabel))
		k.key = mac.Sum(nil)
		id := sha256.Sum256(append([]byte(keyIDLabel), k.key...))
		k.id = k.source + ":" + hex.EncodeToString(id[:8])
	})
	return k.err
}

// ID is the key's identity as a value records it: where it came from and 16
// hex characters that say which key, and nothing of it; empty when the secret
// could not be read.
func (k *Keyer) ID() string {
	if k.load() != nil {
		return ""
	}
	return k.id
}

// errNotADigest is a raw value that is not a SHA-256 in hex.
var errNotADigest = errors.New("not a SHA-256 hex digest")

// normalDigest is raw as 64 lower-case hex characters, or an error.
func normalDigest(raw string) (string, error) {
	raw = strings.ToLower(raw)
	if b, err := hex.DecodeString(raw); err != nil || len(b) != sha256.Size {
		return "", errNotADigest
	}
	return raw, nil
}

// Seal is raw (a SHA-256 in hex) keyed for one fact of one device:
// HMAC-SHA-256 over the device, the fact's key and the digest, each separated
// by a NUL, which none of them can hold.
func (k *Keyer) Seal(deviceID, factKey, raw string) (string, error) {
	if err := k.load(); err != nil {
		return "", err
	}
	raw, err := normalDigest(raw)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, k.key)
	mac.Write(bindDigest(deviceID, factKey, raw))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// KeyRaw is value with its raw digest (RawDigestField) replaced by the keyed
// one and the key's identity, as a new object; value itself is not changed. A
// raw value that is not a digest is dropped, never kept. A value with no raw
// digest comes back as it was.
func (k *Keyer) KeyRaw(deviceID, factKey string, value any) any {
	obj, raw, ok := takeField(value, RawDigestField)
	if !ok {
		return value
	}
	if keyed, err := k.Seal(deviceID, factKey, raw); err == nil {
		obj[KeyedDigestField], obj[KeyedDigestKeyField] = keyed, k.id
	}
	return obj
}

// ReplaceRaw is value with its raw digest replaced by what replace makes of it
// (a sealed digest on a Runner), as a new object; the raw field is dropped
// whatever replace answers, and a replacement it refuses adds nothing. A value
// with no raw digest comes back as it was.
func ReplaceRaw(value any, replace func(raw string) (map[string]any, error)) any {
	obj, raw, ok := takeField(value, RawDigestField)
	if !ok {
		return value
	}
	if raw, err := normalDigest(raw); err == nil && replace != nil {
		if fields, err := replace(raw); err == nil {
			for name, v := range fields {
				obj[name] = v
			}
		}
	}
	return obj
}

// ScrubDigests is value without a raw digest, and without a sealed one unless
// allowSealed (a Runner that seals, or a Controller about to open one), as a
// new object when anything went. It is the rule every writer is held to: a
// digest nothing keyed is never stored, whatever path brought it.
func ScrubDigests(value any, allowSealed bool) any {
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	_, raw := obj[RawDigestField]
	_, sealed := obj[SealedDigestField]
	if !raw && !(sealed && !allowSealed) {
		return value
	}
	out := copyObject(obj)
	delete(out, RawDigestField)
	if !allowSealed {
		delete(out, SealedDigestField)
	}
	return out
}

// takeField is value as a new object without field, and field's value as a
// string, when value is an object holding field.
func takeField(value any, field string) (map[string]any, string, bool) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, "", false
	}
	v, present := obj[field]
	if !present {
		return nil, "", false
	}
	out := copyObject(obj)
	delete(out, field)
	s, _ := v.(string)
	return out, s, true
}

// copyObject is obj's top level, copied.
func copyObject(obj map[string]any) map[string]any {
	out := make(map[string]any, len(obj)+1)
	for name, v := range obj {
		out[name] = v
	}
	return out
}

// bindDigest is a digest bound to one fact of one device: the three joined by
// NULs, which none of them can hold. It is what is keyed, and what a Runner
// seals, so a sealed digest moved to another fact or device opens as the
// wrong one and is refused.
func bindDigest(deviceID, factKey, raw string) []byte {
	return []byte(deviceID + "\x00" + factKey + "\x00" + raw)
}

// SealPlaintext is what a Runner seals for one fact's digest (bindDigest).
func SealPlaintext(deviceID, factKey, raw string) ([]byte, error) {
	raw, err := normalDigest(raw)
	if err != nil {
		return nil, err
	}
	return bindDigest(deviceID, factKey, raw), nil
}

// OpenPlaintext is the digest a sealed plaintext holds, when it was sealed for
// deviceID's fact factKey; anything else is refused.
func OpenPlaintext(plaintext []byte, deviceID, factKey string) (string, error) {
	parts := bytes.Split(plaintext, []byte{0})
	if len(parts) != 3 || string(parts[0]) != deviceID || string(parts[1]) != factKey {
		return "", errors.New("facts: a sealed digest names another fact or device")
	}
	return normalDigest(string(parts[2]))
}
