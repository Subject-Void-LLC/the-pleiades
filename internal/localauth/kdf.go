// The Argon2id key derivation function, its PHC encoding, and the decoy
// derivation that keeps an unknown account as expensive as a known one.
//
// This file holds every decision about HOW a password becomes a stored
// string. localauth.go holds the port and the projection that decide who
// may ask for that to happen.
//
// internal/crypto is deliberately absent here and is the wrong tool. It is
// reversible BY DESIGN: its Service declares Encrypt and Decrypt as a pair,
// EnvelopeService.Decrypt is public, one process-wide KEK from
// MASTER_ENCRYPTION_KEY covers every row, and a previousKEK is deliberately
// retained so old ciphertext still decrypts. A reversible password store is
// a plaintext password store with extra steps: one key compromise exposes
// every account at once, which is the opposite of what per-password salt
// and work factor buy. internal/archtest enforces the absence, because the
// nearest existing primitive is the predictable mistake here rather than an
// unlikely one.
//
// The converse is already written down elsewhere and stays true.
// internal/ui/session's HashToken explains why a fast unsalted SHA-256 is
// correct for a session token and "would not be for a password": a session
// token is 256 bits of uniform randomness, so there is no dictionary to
// attack and no rainbow table to precompute. A password is not, which is
// what everything in this file exists to compensate for.
package localauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
//
// It is deliberately distinct from "the password did not match", and
// callers must still treat both as the same authentication failure. The
// separation exists so an operator reading logs can tell a corrupted row
// from a wrong password, not so a handler can answer differently.
var ErrInvalidHash = errors.New("localauth: stored password hash is not a valid Argon2id PHC string")

// Params are one hash's Argon2id cost parameters.
//
// They travel WITH the hash rather than beside it in separate columns. See
// Encode for why.
type Params struct {
	// Memory is the KiB of memory the derivation fills.
	Memory uint32
	// Time is the number of passes over that memory.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
	// SaltLen and KeyLen are lengths in bytes.
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams is current policy, and the parameters every new hash is
// written with.
//
// 19 MiB, two passes, one lane. The reasoning, because a KDF cost picked
// without one is a number nobody can later defend or safely change:
//
// The threat that matters is OFFLINE cracking of a stolen database dump
// holding a handful of break-glass administrator accounts, which is exactly
// the population PLAN.md Section 18.1 says must be hardened. Memory
// hardness is the property that scales against an attacker who already has
// the rows, because it denies them the GPU and ASIC parallelism a
// memory-light hash hands over for free. That is the argument for Argon2id
// over bcrypt.
//
// The counter-pressure is that cmd/controller is not a login server. The
// same process holds the ent pool, the NATS connection, the leader-elected
// session sweeper and the DAG executor, and POST /ui/login is
// unauthenticated. An unauthenticated endpoint that allocates 64 MiB per
// call is a memory-exhaustion denial of service against a control plane,
// delivered by ordinary HTTP. 19456 KiB is OWASP's documented
// memory-constrained configuration, and it is paired with the bounded
// concurrency in Gate so the worst-case transient footprint is a number
// rather than a function of how fast an attacker can open sockets.
//
// Rejected, recorded rather than merely decided: bcrypt carries its own
// encoded format, cost extraction and constant-time compare in-library,
// which is materially less code to get wrong, and is rejected for its
// 72-byte input cap (which caps passphrase entropy for precisely the
// hardened accounts above) and for having no memory hardness at all.
// scrypt is memory-hard but has no canonical parameter guidance this
// project could cite and none of Argon2id's hybrid side-channel posture.
var DefaultParams = Params{
	Memory:  19456,
	Time:    2,
	Threads: 1,
	SaltLen: 16,
	KeyLen:  32,
}

// Bounds on what a decoded hash may claim, enforced BEFORE any derivation.
//
// These are not defensive programming for its own sake. The encoded string
// arrives from a database row, so an attacker who can write one row can
// otherwise choose the memory a login allocates. m=4294967295 is a 4 TiB
// allocation reachable from ordinary data, and it would present as an
// unexplained OOM in the control plane rather than as an attack.
const (
	minMemory  = 8 // Argon2 requires memory >= 8 * threads; threads >= 1.
	maxMemory  = 1 << 21
	maxTime    = 16
	maxThreads = 16
	minSaltLen = 8
	maxSaltLen = 64
	minKeyLen  = 16
	maxKeyLen  = 64
	// maxPasswordLen caps input before the KDF runs. Without it a large
	// form field is a CPU and memory amplifier on an unauthenticated
	// endpoint, since Argon2 hashes the whole password on every pass.
	// bcrypt's own loud refusal above its input cap is the precedent for
	// capping rather than silently truncating.
	maxPasswordLen = 1024
)

// algorithm is the PHC identifier this package writes and the only one it
// accepts. A future cost bump or algorithm change adds a case here and
// needs no migration, which is the point of storing it.
const algorithm = "argon2id"

// Hash derives a new PHC-encoded Argon2id hash of password at current
// policy, with a fresh random salt.
func Hash(password string) (string, error) {
	return hashWith(password, DefaultParams)
}

// hashWith is Hash with explicit parameters, so tests can exercise a
// cheap cost without weakening the exported default.
func hashWith(password string, p Params) (string, error) {
	if len(password) == 0 {
		return "", errors.New("localauth: refusing to hash an empty password")
	}
	if len(password) > maxPasswordLen {
		return "", fmt.Errorf("localauth: password is %d bytes, over the %d byte limit", len(password), maxPasswordLen)
	}

	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		// Fail closed. A salt from a degraded entropy source is worse than
		// no hash at all, because it is indistinguishable from a good one
		// afterwards.
		return "", fmt.Errorf("localauth: failed to read salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return Encode(p, salt, key), nil
}

// Encode renders one hash as a PHC string:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
//
// One self-describing string rather than a digest beside parallel cost
// columns, because rehash-on-login needs the parameters that produced THIS
// hash: they then arrive with the row already loaded, there is no second
// column to forget to write, and there is no way for the two to disagree.
// It is also the format every other Argon2 implementation emits, which
// keeps the column portable. PLAN.md Section 17.2's "v1$AES256GCM$"
// ciphertext prefix is the same argument already made once in this
// codebase.
func Encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		algorithm,
		argon2.Version,
		p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// Decode parses a PHC string back into its parameters, salt and key.
//
// Every branch fails closed. An unrecognized algorithm, a wrong segment
// count, a non-numeric or out-of-range cost, or a salt or key outside its
// bounds is an error, never a zero value passed on to the caller: a
// permissive parse here would turn a corrupted row into "no password set",
// which is an authentication bypass rather than a data problem.
func Decode(encoded string) (Params, []byte, []byte, error) {
	// Six parts, because the leading "$" produces an empty first field.
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: expected 5 dollar-separated fields, got %d", ErrInvalidHash, len(parts)-1)
	}
	if parts[1] != algorithm {
		return Params{}, nil, nil, fmt.Errorf("%w: algorithm is %q, not %q", ErrInvalidHash, parts[1], algorithm)
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unparsable version field %q", ErrInvalidHash, parts[2])
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("%w: version %d, this build derives with %d", ErrInvalidHash, version, argon2.Version)
	}

	var p Params
	// Sscanf is strict about the literal separators but not about trailing
	// input, so the field is length-checked by reconstructing it below.
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unparsable cost field %q", ErrInvalidHash, parts[3])
	}
	if got := fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads); got != parts[3] {
		return Params{}, nil, nil, fmt.Errorf("%w: cost field %q has trailing or non-canonical content", ErrInvalidHash, parts[3])
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: salt is not raw standard base64", ErrInvalidHash)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: key is not raw standard base64", ErrInvalidHash)
	}

	// Bounded as int BEFORE the widening conversion, rather than after it
	// inside validate. The order is what makes the conversion provably
	// lossless instead of merely lossless in practice, which matters here
	// because these lengths come from an attacker-writable database row and
	// because a silently wrapped length would be a shorter salt than the
	// parameters claim.
	saltLen, keyLen := len(salt), len(key)
	if saltLen < minSaltLen || saltLen > maxSaltLen {
		return Params{}, nil, nil, fmt.Errorf("%w: salt is %d bytes, outside [%d,%d]",
			ErrInvalidHash, saltLen, minSaltLen, maxSaltLen)
	}
	if keyLen < minKeyLen || keyLen > maxKeyLen {
		return Params{}, nil, nil, fmt.Errorf("%w: key is %d bytes, outside [%d,%d]",
			ErrInvalidHash, keyLen, minKeyLen, maxKeyLen)
	}
	p.SaltLen = uint32(saltLen)
	p.KeyLen = uint32(keyLen)

	if err := p.validate(); err != nil {
		return Params{}, nil, nil, err
	}
	return p, salt, key, nil
}

// validate range-checks the COST parameters. It runs before any
// derivation, which is the whole point: see the bounds constants above.
//
// Salt and key lengths are not checked here. They are checked in Decode,
// immediately before the int-to-uint32 widening that produces them, because
// that is the only place the two operations can be kept in the order that
// makes the conversion provably lossless. Repeating the check here would be
// an unreachable branch that reads like a second line of defense.
func (p Params) validate() error {
	switch {
	case p.Threads < 1 || p.Threads > maxThreads:
		return fmt.Errorf("%w: parallelism %d outside [1,%d]", ErrInvalidHash, p.Threads, maxThreads)
	case p.Time < 1 || p.Time > maxTime:
		return fmt.Errorf("%w: passes %d outside [1,%d]", ErrInvalidHash, p.Time, maxTime)
	case p.Memory < minMemory || p.Memory > maxMemory:
		return fmt.Errorf("%w: memory %d KiB outside [%d,%d]", ErrInvalidHash, p.Memory, minMemory, maxMemory)
	case p.Memory < uint32(p.Threads)*8:
		// Argon2 itself requires this; violating it panics inside IDKey
		// rather than returning an error.
		return fmt.Errorf("%w: memory %d KiB is below 8x parallelism %d", ErrInvalidHash, p.Memory, p.Threads)
	}
	return nil
}

// Verify reports whether password produced encoded.
//
// It returns (false, nil) for a wrong password and (false, err) only when
// the stored string is unusable. Callers must answer identically in both
// cases; the distinction is for logs, not for responses.
func Verify(encoded, password string) (bool, error) {
	if len(password) > maxPasswordLen {
		// Refused before the derivation rather than after, so an oversized
		// field costs nothing. Not an ErrInvalidHash: the stored value is
		// fine, the submitted one is not.
		return false, nil
	}

	p, salt, want, err := Decode(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	// Constant time, because a byte-at-a-time comparison of a derived key
	// is a timing oracle on the key itself.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether encoded was derived at anything other than
// current policy.
//
// The caller rehashes only after a SUCCESSFUL verify, because that is the
// only moment the plaintext exists, and treats a failed rehash write as a
// log line rather than a failed authentication: a stale cost is worse than
// the current hash, not worse than locking the account's owner out.
func NeedsRehash(encoded string) (bool, error) {
	p, _, _, err := Decode(encoded)
	if err != nil {
		return false, err
	}
	return p != DefaultParams, nil
}

// decoy holds a hash of a random value, derived once, used to make a login
// for an unknown account cost the same as one for a known account.
var decoy struct {
	once sync.Once
	hash string
}

// DecoyHash returns a hash no submitted password can match.
//
// It exists for the timing half of username enumeration. A store that
// short-circuits on "no such user" skips the derivation entirely, which
// makes the unknown-account case tens of milliseconds faster and turns the
// login form into an account-existence oracle that anyone can query. The
// caller runs a real verification against this instead, then fails.
//
// The value is derived from crypto/rand at first use rather than being a
// constant in the source. A constant would be a genuine hardcoded
// credential, would be flagged as one, and would be identical across every
// deployment; generating it costs one derivation per process and is not on
// any hot path.
//
// It never returns an error: a failure to read entropy falls back to a
// syntactically valid hash of a fixed value, because the only thing this
// hash must guarantee is that verification takes the normal amount of time
// and does not match. It is not protecting anything.
func DecoyHash() string {
	decoy.once.Do(func() {
		seed := make([]byte, 32)
		if _, err := rand.Read(seed); err != nil {
			seed = []byte("localauth decoy fallback seed")
		}
		h, err := hashWith(base64.RawStdEncoding.EncodeToString(seed), DefaultParams)
		if err != nil {
			// Unreachable: the input above is neither empty nor oversized.
			// Encoded directly rather than left blank so a caller always
			// gets a string Verify will spend real time on.
			salt := make([]byte, DefaultParams.SaltLen)
			key := argon2.IDKey(seed, salt, DefaultParams.Time, DefaultParams.Memory, DefaultParams.Threads, DefaultParams.KeyLen)
			h = Encode(DefaultParams, salt, key)
		}
		decoy.hash = h
	})
	return decoy.hash
}

// BurnDecoy performs a derivation that cannot succeed, so an unknown
// account costs the same as a known one. The result is discarded on
// purpose.
func BurnDecoy(password string) {
	_, _ = Verify(DecoyHash(), password)
}
