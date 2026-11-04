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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	if holdsNUL(deviceID, factKey) {
		return "", errBindingNUL
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

// The fields of a file-like fact's object that say whether anyone may read
// it, and what a private one never keeps: a plain digest or a size.
const (
	WorldReadableField = "world_readable"
	PlainDigestField   = "sha256"
	SizeField          = "size"
)

// ScrubDigests is value without a raw digest anywhere in it, without a sealed
// one anywhere but its top level and there only when allowSealed (a Runner
// that seals, or a Controller about to open one), and, for an object saying
// world_readable is false, without a plain digest or a size, as a new value.
// It is the rule every writer and every result is held to, whatever path
// brought the value and however deep the field sits: a digest nothing keyed
// is never stored or shown, and a store does not trust a sender to have left
// a private file's plain digest out.
func ScrubDigests(value any, allowSealed bool) any {
	return scrubDigests(value, allowSealed, true)
}

// scrubDigests is ScrubDigests at one depth; top says value is the fact's
// own object, the one place a sealed digest may sit.
func scrubDigests(value any, allowSealed, top bool) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for name, field := range v {
			if name == RawDigestField || (name == SealedDigestField && !(allowSealed && top)) {
				continue
			}
			out[name] = scrubDigests(field, allowSealed, false)
		}
		if out[WorldReadableField] == false {
			delete(out, PlainDigestField)
			delete(out, SizeField)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = scrubDigests(item, allowSealed, false)
		}
		return out
	case []map[string]any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = scrubDigests(item, allowSealed, false)
		}
		return out
	case nil, string, bool, json.Number, []string,
		int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return value
	}
	// Any other map, slice or struct (a typed value a method set in this
	// process, never through JSON) is read as JSON reads it and held to the
	// same rule, so a digest cannot hide in a type this switch does not name.
	// A value that does not encode is dropped: nothing could store it.
	switch reflect.ValueOf(value).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct, reflect.Pointer, reflect.Interface:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var decoded any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			return nil
		}
		return scrubDigests(decoded, allowSealed, top)
	}
	return value
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

// errBindingNUL is a binding field holding a NUL, which would let one field's
// end be read as another's.
var errBindingNUL = errors.New("facts: a digest's job, device or fact holds a NUL")

// bindDigest is a digest bound to one fact of one device: the three joined by
// NULs, which none of them may hold (Seal refuses one). It is what is keyed.
func bindDigest(deviceID, factKey, raw string) []byte {
	return []byte(deviceID + "\x00" + factKey + "\x00" + raw)
}

// holdsNUL reports whether any of fields holds a NUL.
func holdsNUL(fields ...string) bool {
	for _, f := range fields {
		if strings.IndexByte(f, 0) >= 0 {
			return true
		}
	}
	return false
}

// SealPlaintext is what a Runner seals for one fact's digest: the job, the
// device, the fact's key and the digest, joined by NULs none of them may hold.
// The job is bound too, so a sealed digest replayed into a later job (to make
// a changed file look unchanged) opens as another job's and is refused.
func SealPlaintext(jobID, deviceID, factKey, raw string) ([]byte, error) {
	if holdsNUL(jobID, deviceID, factKey) {
		return nil, errBindingNUL
	}
	raw, err := normalDigest(raw)
	if err != nil {
		return nil, err
	}
	return []byte(jobID + "\x00" + deviceID + "\x00" + factKey + "\x00" + raw), nil
}

// OpenPlaintext is the digest a sealed plaintext holds, when it was sealed in
// jobID for deviceID's fact factKey; anything else is refused.
func OpenPlaintext(plaintext []byte, jobID, deviceID, factKey string) (string, error) {
	parts := bytes.Split(plaintext, []byte{0})
	if len(parts) != 4 || string(parts[0]) != jobID || string(parts[1]) != deviceID || string(parts[2]) != factKey {
		return "", errors.New("facts: a sealed digest names another job, device or fact")
	}
	return normalDigest(string(parts[3]))
}
