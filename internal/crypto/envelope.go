package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// algorithmTag is the literal algorithm identifier every envelope string
// carries as its second $-delimited field. It is currently the only
// algorithm this package produces or accepts; Decrypt rejects any other
// value rather than guessing at how to interpret it.
const algorithmTag = "AES256GCM"

var (
	// ErrUnknownAlgorithm is returned by EnvelopeService.Decrypt when a
	// ciphertext string's algorithm field is not algorithmTag.
	ErrUnknownAlgorithm = errors.New("envelope: unrecognized algorithm tag")
	// ErrUnknownKeyVersion is returned by EnvelopeService.Decrypt when a
	// ciphertext string's version field matches neither the service's
	// current nor (if configured) previous key version.
	ErrUnknownKeyVersion = errors.New("envelope: unrecognized key version tag")
	// ErrMalformedEnvelope is returned by EnvelopeService.Decrypt when a
	// ciphertext string does not have the shape
	// "<version>$AES256GCM$<wrapped DEK>$<sealed data>", or when either
	// base64 segment fails to decode.
	ErrMalformedEnvelope = errors.New("envelope: malformed ciphertext header")
)

// EnvelopeService implements DEK/KEK envelope encryption (PATTERNS.md's
// "Envelope Encryption (Data at Rest)" entry, PLAN.md Section 17.2): each
// Encrypt call generates a fresh, single-use Data Encryption Key, seals the
// plaintext under it, then seals the DEK itself under a longer-lived Key
// Encryption Key. This is what makes it envelope encryption rather than
// the single-key field encryption internal/crypto/aes.go's Service alone
// provides: compromising one ciphertext's DEK never exposes the KEK, and
// rotating the KEK (below) never requires touching the DEK generation
// logic.
//
// A service is constructed with a required current KEK and version tag,
// plus an optional previous KEK and version tag for the window during a
// key rotation: Encrypt always seals new DEKs under the current KEK, while
// Decrypt accepts ciphertext produced under either, selected by the
// version tag each ciphertext string carries. This is the Pattern Entry
// Gate's named "Strategy": which KEK a given ciphertext resolves to is a
// version-tag-driven lookup, not a hardcoded single key.
//
// Known, deliberate residual risk: ciphertext strings carry no associated
// data binding them to the record they were encrypted for (an
// adversarial review of this phase confirmed this concretely, by copying
// one Device row's stored envelope string onto another and observing
// Decrypt succeed against the wrong row's data). Adding it would mean
// threading a record identifier through every caller and could not be
// done by extending internal/crypto/aes.go's Service interface, which is
// deliberately unchanged this phase (load-bearing for
// internal/credential's own, unrelated on-disk file format). The threat
// this closes is a DB-level actor with write access relocating one row's
// ciphertext onto another under a shared KEK; the threat this phase's own
// stated goal targets is a database dump yielding only ciphertext
// (PATTERNS.md's Envelope Encryption entry), a lower privilege tier than
// write access, which this design fully closes. Not implemented this
// phase; a real gap if the former threat model is ever in scope.
type EnvelopeService struct {
	currentKEK     Service
	currentVersion string

	// previousKEK is nil when no rotation is in progress. hasPrevious
	// distinguishes that from "the zero Service value", which is not a
	// meaningful state for this field.
	previousKEK     Service
	previousVersion string
	hasPrevious     bool
}

// NewEnvelopeService constructs an EnvelopeService from a required current
// KEK (exactly 32 bytes) and its version tag (non-empty, must not contain
// "$", the field delimiter every envelope string uses), plus an optional
// previous KEK and version tag: pass a nil previousKey and empty
// previousVersion together when no rotation is in progress, or both a
// non-nil previousKey and non-empty previousVersion together to accept
// ciphertext produced by an earlier key during a rotation window. Passing
// only one of the two is a construction error, since a previous slot with
// no way to identify which ciphertext strings belong to it (or vice versa)
// can never be resolved by Decrypt.
func NewEnvelopeService(currentKey []byte, currentVersion string, previousKey []byte, previousVersion string) (*EnvelopeService, error) {
	currentKEK, err := NewAESService(currentKey)
	if err != nil {
		return nil, fmt.Errorf("envelope: current key: %w", err)
	}
	if err := validateVersionTag(currentVersion); err != nil {
		return nil, fmt.Errorf("envelope: current version: %w", err)
	}

	svc := &EnvelopeService{currentKEK: currentKEK, currentVersion: currentVersion}

	havePreviousKey := previousKey != nil
	havePreviousVersion := previousVersion != ""
	if havePreviousKey != havePreviousVersion {
		return nil, fmt.Errorf("envelope: previous key and previous version must be set together, or not at all")
	}
	if !havePreviousKey {
		return svc, nil
	}

	previousKEK, err := NewAESService(previousKey)
	if err != nil {
		return nil, fmt.Errorf("envelope: previous key: %w", err)
	}
	if err := validateVersionTag(previousVersion); err != nil {
		return nil, fmt.Errorf("envelope: previous version: %w", err)
	}
	if previousVersion == currentVersion {
		return nil, fmt.Errorf("envelope: previous version %q must differ from current version, or ciphertext could not be resolved to a key unambiguously", previousVersion)
	}

	svc.previousKEK = previousKEK
	svc.previousVersion = previousVersion
	svc.hasPrevious = true
	return svc, nil
}

// validateVersionTag rejects an empty tag (Decrypt could never match it
// against a real ciphertext's own non-empty field) and a tag containing
// "$" (it would be split across fields by strings.SplitN, corrupting
// every envelope string this service produces).
func validateVersionTag(tag string) error {
	if tag == "" {
		return errors.New("version tag must not be empty")
	}
	if strings.Contains(tag, "$") {
		return fmt.Errorf("version tag %q must not contain %q, the envelope field delimiter", tag, "$")
	}
	return nil
}

// CurrentVersion reports the version tag Encrypt stamps new ciphertext
// with.
func (s *EnvelopeService) CurrentVersion() string {
	return s.currentVersion
}

// Encrypt seals plaintext under a freshly generated, single-use Data
// Encryption Key, seals that DEK under the service's current Key
// Encryption Key, and returns the result as a
// "<version>$AES256GCM$<base64 wrapped DEK>$<base64 sealed data>" string
// (PLAN.md Section 17.2). The DEK exists only for the duration of this
// call and is explicitly zeroed before returning; this is defense in
// depth, not a guarantee, since the Go compiler is free to elide a dead
// store to a slice that is never read again.
func (s *EnvelopeService) Encrypt(plaintext []byte) (string, error) {
	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return "", fmt.Errorf("envelope: failed to generate data encryption key: %w", err)
	}
	defer zeroBytes(dek)

	dekSvc, err := NewAESService(dek)
	if err != nil {
		return "", fmt.Errorf("envelope: failed to init data key cipher: %w", err)
	}
	sealedData, err := dekSvc.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("envelope: failed to seal data: %w", err)
	}

	wrappedDEK, err := s.currentKEK.Encrypt(dek)
	if err != nil {
		return "", fmt.Errorf("envelope: failed to wrap data key: %w", err)
	}

	return fmt.Sprintf("%s$%s$%s$%s",
		s.currentVersion,
		algorithmTag,
		base64.StdEncoding.EncodeToString(wrappedDEK),
		base64.StdEncoding.EncodeToString(sealedData),
	), nil
}

// Decrypt reverses Encrypt. Every malformed-input path (wrong field count,
// unrecognized algorithm tag, unrecognized version tag, invalid base64,
// GCM authentication failure on either the wrapped key or the data) is a
// returned error, never a panic and never zero-value plaintext: the
// caller's own storage is the only source of this string, but a corrupted
// or tampered row must fail closed exactly like a hostile one would.
func (s *EnvelopeService) Decrypt(ciphertext string) ([]byte, error) {
	parts := strings.SplitN(ciphertext, "$", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("%w: expected 4 fields separated by %q, got %d", ErrMalformedEnvelope, "$", len(parts))
	}
	version, algorithm, wrappedB64, dataB64 := parts[0], parts[1], parts[2], parts[3]

	if algorithm != algorithmTag {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algorithm)
	}

	kek, err := s.resolveKEK(version)
	if err != nil {
		return nil, err
	}

	wrappedDEK, err := base64.StdEncoding.DecodeString(wrappedB64)
	if err != nil {
		return nil, fmt.Errorf("%w: wrapped key is not valid base64: %v", ErrMalformedEnvelope, err)
	}
	sealedData, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("%w: sealed data is not valid base64: %v", ErrMalformedEnvelope, err)
	}

	dek, err := kek.Decrypt(wrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("envelope: failed to unwrap data key: %w", err)
	}
	defer zeroBytes(dek)

	dekSvc, err := NewAESService(dek)
	if err != nil {
		// Only reachable if the unwrapped key is not exactly keySize
		// bytes: GCM authentication already passed above, so this
		// would mean a legitimate wrap of a wrong-length key, not
		// tampering. Defense in depth, not an expected path.
		return nil, fmt.Errorf("envelope: unwrapped data key is invalid: %w", err)
	}

	plaintext, err := dekSvc.Decrypt(sealedData)
	if err != nil {
		return nil, fmt.Errorf("envelope: failed to decrypt data: %w", err)
	}
	return plaintext, nil
}

// resolveKEK matches version against the service's current and (if
// configured) previous version tags, returning the corresponding KEK.
func (s *EnvelopeService) resolveKEK(version string) (Service, error) {
	switch {
	case version == s.currentVersion:
		return s.currentKEK, nil
	case s.hasPrevious && version == s.previousVersion:
		return s.previousKEK, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKeyVersion, version)
	}
}

// zeroBytes overwrites b in place. Best-effort defense in depth against a
// key surviving in memory longer than needed (PLAN.md Section 17.5); the
// Go compiler may still elide the store if it can prove b is never read
// again, so this is not a hard guarantee.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
