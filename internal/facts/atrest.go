// Package facts: stored facts at rest (Phase 41, the owner's decision of
// 2026-11-19, tokenization on both tiers).
//
// # Design note
//
// A stored fact used to be plaintext: its key, its value and a plain SHA-256
// of the two. Sealing the value alone would not be enough. A plain hash lets
// anyone holding a copy of the store test a guess at a short value (a
// version, a port, yes or no), and a key names a package, a user, a service
// or a port by itself. So a store keeps three things differently, all from
// one secret it holds (Keys):
//
//   - The fact hash is keyed: HMAC-SHA-256 over the device, a NUL and the
//     canonical JSON of [key, value], under a hash key. It is deterministic,
//     so an unchanged value still compares equal and a changed one still
//     writes a new row; without the key no guess can be tested against it.
//     The device is bound in, so one value on two devices does not show as
//     the same hash. The per-device chain (chain.go) covers this keyed hash,
//     and VerifyChain recomputes it with the same Keys.
//   - The key is looked up by a token: HMAC-SHA-256 over the device, a NUL
//     and the key, under a token key. The Controller's table indexes the
//     token, and a coverage lists tokens, so the database names no key. One
//     package on two devices gives two tokens.
//   - The key and the value themselves are sealed. On the Controller that is
//     internal/crypto's bound envelope under MASTER_ENCRYPTION_KEY, bound to
//     the row's device and token (internal/crypto's fact_hook.go,
//     internal/facts/entstore). On the Crawl CLI each line of
//     <device>.jsonl and <device>.seen.jsonl, and <device>.state.json whole,
//     is sealed with AES-256-GCM under a line key (SealLine), with
//     associated data naming the device, the file's role and the line's
//     byte offset, so a line moved to another device, another file or
//     another place fails to open.
//
// Each key is derived from the store's one secret under a label of its own
// (HMAC-SHA-256 of the label under the secret), and none is the keyed-digest
// key (keyed.go) or the Runner seal key (entstore's digests.go), which have
// labels of their own. The Controller's secret is PLEIADES_FACTS_DIGEST_KEY;
// the CLI's is the project's master key. A store with no Keys stores
// nothing and reads nothing (ErrNoKeys): plaintext is never the fallback.
//
// What crosses a process boundary is unchanged: Prepare and Verify still
// compute and check the plain Hash of a value in transit, which says a
// published hash describes its value. A store keys the hash as it records
// (Keys.Rekey), so an Entry read back from a store carries the keyed hash.
//
// A change of the secret is not a rotation this build performs. Every keyed
// hash, token and chain link would have to be rewritten, and the history is
// insert-only. Each Controller row names the Keys it was written under, and
// a store refuses a device whose rows name other Keys rather than start a
// second, unlinked history beside the first. The CLI's lines simply do not
// open under another key, which reads as an error naming the key.
package facts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// The labels each at-rest key is derived under. A new suite is a new label.
const (
	atRestHashLabel  = "pleiades/facts/at-rest/hash/v1"
	atRestTokenLabel = "pleiades/facts/at-rest/token/v1"
	atRestLineLabel  = "pleiades/facts/at-rest/line/v1/aes-256-gcm"
	atRestIDLabel    = "pleiades/facts/at-rest/key-id"
)

// linePrefix opens every sealed line, and names its format.
const linePrefix = "v1."

// The roles a sealed line of the CLI's store can have, one per file.
const (
	// RoleValues is a line of <device>.jsonl.
	RoleValues = "values"
	// RoleSeen is a line of <device>.seen.jsonl.
	RoleSeen = "seen"
	// RoleState is <device>.state.json, whole.
	RoleState = "state"
)

// ErrNoKeys marks a store asked to keep or read facts with no key to seal
// them under. Nothing is stored and nothing is read: plaintext is never the
// fallback.
var ErrNoKeys = errors.New("facts: no key to seal stored facts with")

// ErrSealed marks a stored line that does not open: it was sealed under
// another key, belongs to another device, file or place, was edited, or was
// written before facts were sealed.
var ErrSealed = errors.New("facts: a stored fact does not open under this store's key")

// Keys is one store's at-rest keys: the key that keys fact hashes, the key
// that tokenizes fact keys, and the key the CLI seals its lines under, each
// derived from the store's one secret. Build one with NewKeys, or with
// NewLazyKeys to read the secret only when a fact is stored or read. A nil
// *Keys answers every call with ErrNoKeys.
type Keys struct {
	once    sync.Once
	resolve func() ([]byte, error)
	source  string
	hash    []byte
	token   []byte
	line    cipher.AEAD
	id      string
	err     error
}

// NewKeys derives a store's at-rest keys from secret (at least 32 bytes).
// source names where the secret came from (project, controller) and leads
// the keys' identity.
func NewKeys(secret []byte, source string) (*Keys, error) {
	k := NewLazyKeys(func() ([]byte, error) { return secret, nil }, source)
	if err := k.load(); err != nil {
		return nil, err
	}
	return k, nil
}

// NewLazyKeys is a Keys that calls resolve for its secret the first time a
// fact is stored or read, and never otherwise. A secret that cannot be read
// stores and reads nothing.
func NewLazyKeys(resolve func() ([]byte, error), source string) *Keys {
	return &Keys{resolve: resolve, source: source}
}

// derive is the key label names under secret.
func derive(secret []byte, label string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

// load reads the secret and derives the keys, once.
func (k *Keys) load() error {
	if k == nil {
		return ErrNoKeys
	}
	k.once.Do(func() {
		secret, err := k.resolve()
		if err != nil {
			k.err = fmt.Errorf("%w: reading its secret: %w", ErrNoKeys, err)
			return
		}
		if len(secret) < 32 {
			k.err = fmt.Errorf("%w: its secret must be at least 32 bytes, got %d", ErrNoKeys, len(secret))
			return
		}
		k.hash, k.token = derive(secret, atRestHashLabel), derive(secret, atRestTokenLabel)
		// A 32-byte key always makes an AES-256 cipher, and GCM over it.
		block, err := aes.NewCipher(derive(secret, atRestLineLabel))
		if err == nil {
			k.line, err = cipher.NewGCM(block)
		}
		if err != nil {
			k.err = fmt.Errorf("%w: %w", ErrNoKeys, err)
			return
		}
		id := sha256.Sum256(append(append([]byte(atRestIDLabel), k.hash...), k.token...))
		k.id = k.source + ":" + hex.EncodeToString(id[:8])
	})
	return k.err
}

// ID is the keys' identity as a row records it: where the secret came from
// and 16 hex characters that say which keys, and nothing of them; empty when
// there are none.
func (k *Keys) ID() string {
	if k.load() != nil {
		return ""
	}
	return k.id
}

// Hash is a fact's keyed hash: HMAC-SHA-256, hex-encoded, over the device, a
// NUL and the canonical JSON of [key, value]. It is what a store compares to
// see a change, and what the device's chain covers.
func (k *Keys) Hash(device, key string, value any) (string, error) {
	if err := k.load(); err != nil {
		return "", err
	}
	if holdsNUL(device) {
		return "", errBindingNUL
	}
	canon, err := Canonical([]any{key, value})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, k.hash)
	mac.Write([]byte(device))
	mac.Write([]byte{0})
	mac.Write(canon)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Token is the token a store looks a device's key up by: HMAC-SHA-256,
// hex-encoded, over the device, a NUL and the key.
func (k *Keys) Token(device, key string) (string, error) {
	if err := k.load(); err != nil {
		return "", err
	}
	if holdsNUL(device, key) {
		return "", errBindingNUL
	}
	mac := hmac.New(sha256.New, k.token)
	mac.Write([]byte(device + "\x00" + key))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Rekey is entries with each one's hash replaced by its keyed hash for
// device, as a new slice: what a store does to an observation before it
// compares or writes anything.
func (k *Keys) Rekey(device string, entries []Entry) ([]Entry, error) {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		hash, err := k.Hash(device, e.Key, e.Value)
		if err != nil {
			return nil, fmt.Errorf("facts: keying the hash of a fact: %w", err)
		}
		e.Hash = hash
		out[i] = e
	}
	return out, nil
}

// lineBinding is the associated data of one sealed line: the device, the
// file's role and the line's byte offset in it, none of which is secret.
func lineBinding(device, role string, offset int64) []byte {
	return []byte(atRestLineLabel + "\x00" + device + "\x00" + role + "\x00" + strconv.FormatInt(offset, 10))
}

// SealLine seals plaintext as one line of device's file of the given role,
// to be written at byte offset: the format's prefix and the base64 of a
// fresh nonce and the ciphertext, with no newline. It opens only as that
// device's line of that role at that offset.
func (k *Keys) SealLine(device, role string, offset int64, plaintext []byte) ([]byte, error) {
	if err := k.load(); err != nil {
		return nil, err
	}
	if holdsNUL(device, role) {
		return nil, errBindingNUL
	}
	nonce := make([]byte, k.line.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	sealed := k.line.Seal(nonce, nonce, plaintext, lineBinding(device, role, offset))
	out := make([]byte, len(linePrefix)+base64.RawStdEncoding.EncodedLen(len(sealed)))
	copy(out, linePrefix)
	base64.RawStdEncoding.Encode(out[len(linePrefix):], sealed)
	return out, nil
}

// OpenLine opens a line SealLine made for device's file of the given role at
// byte offset. Anything else is ErrSealed: a line sealed under another key,
// moved from another device, file or place, edited, or never sealed.
func (k *Keys) OpenLine(device, role string, offset int64, line []byte) ([]byte, error) {
	if err := k.load(); err != nil {
		return nil, err
	}
	if len(line) < len(linePrefix) || string(line[:len(linePrefix)]) != linePrefix {
		return nil, ErrSealed
	}
	sealed, err := base64.RawStdEncoding.DecodeString(string(line[len(linePrefix):]))
	if err != nil || len(sealed) < k.line.NonceSize() {
		return nil, ErrSealed
	}
	nonce, body := sealed[:k.line.NonceSize()], sealed[k.line.NonceSize():]
	plaintext, err := k.line.Open(nil, nonce, body, lineBinding(device, role, offset))
	if err != nil {
		return nil, ErrSealed
	}
	return plaintext, nil
}

// SealedLineBytes is how long SealLine's line is for a plaintext of n bytes,
// with the newline a file ends it with.
func SealedLineBytes(n int) int {
	// A GCM nonce is 12 bytes and its tag 16.
	return len(linePrefix) + base64.RawStdEncoding.EncodedLen(n+12+16) + 1
}
