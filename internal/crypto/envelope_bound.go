package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// Associated-data binding: the closure of the residual risk EnvelopeService's
// own doc comment records.
//
// That comment states the gap plainly and states why it was accepted: a
// ciphertext string carries nothing tying it to the record it was encrypted
// for, so a database-level actor with write access can copy one row's
// envelope onto another and Decrypt succeeds against the wrong row's data.
// It was verified concretely at the time, by doing exactly that with two
// Device rows. The judgement was that the threat this phase targeted was a
// database DUMP yielding only ciphertext, a strictly lower privilege tier,
// and that closing the write-access case would mean threading a record
// identifier through every caller.
//
// Phase 22 changes the arithmetic rather than the argument. A credential row
// is that attack's ideal target: relocate one organization's encrypted
// inputs onto another organization's credential, and it decrypts to the
// first organization's secrets under a binding the second one controls. The
// attacker does not need to read anything; they need one UPDATE on a column
// whose contents are opaque to them, and the platform then injects those
// secrets into a job they scheduled. That is a tenancy boundary failing, not
// a confidentiality margin narrowing.
//
// So the binding is added HERE, scoped to the one entity that needs it,
// rather than retrofitted across every caller:
//
//   - Service (aes.go) is unchanged. It is load bearing for
//     internal/credential's on-disk file format, and widening its interface
//     would change a format that already has files written in it.
//   - The DEK is still wrapped through Service exactly as before. Only the
//     DATA seal gains associated data, which is the only place it matters.
//   - A new algorithm tag distinguishes the two forms on the wire, so old
//     unbound ciphertext keeps decrypting and a bound ciphertext presented
//     without its associated data fails closed rather than succeeding.
//
// Device.properties and SavedLaunchConfig.answers remain in the UNBOUND
// form after this change. That is a known residual, recorded here rather
// than left to be inferred: those two entities would need a rotation pass
// to migrate, and a rotation pass over live encrypted data is its own piece
// of work with its own failure modes.

// boundAlgorithmTag is the algorithm identifier a bound envelope carries.
//
// A distinct tag rather than a flag is what makes the two forms fail closed
// against each other. Decrypt rejects this tag as unknown, and DecryptBound
// rejects the unbound tag, so neither can silently accept a ciphertext of
// the other kind: presenting a bound ciphertext without its associated data
// is exactly the relocation attack, and it must not degrade into a
// successful unbound decrypt.
const boundAlgorithmTag = "AES256GCM-AAD"

// IsBoundEnvelope reports whether a stored ciphertext is in the bound form.
//
// It exists for the migration window Phase 78c opens. Device.properties and
// SavedLaunchConfig.answers hold a MIX while a rotation pass converts them,
// and a reader has to know which of the two it is holding before it can
// choose Decrypt or DecryptBound. The algorithm tag already carries that
// fact, which is exactly why boundAlgorithmTag is a distinct tag rather
// than a flag: this function reads what is on the wire rather than guessing
// from what the row looks like.
//
// It answers false for anything malformed, which is correct and not lax: an
// unparseable ciphertext must reach Decrypt and fail there with the
// malformed-envelope error that names the real problem, rather than fail
// here as a binding mismatch and send somebody hunting a relocation attack
// that did not happen.
func IsBoundEnvelope(ciphertext string) bool {
	parts := strings.SplitN(ciphertext, "$", 4)
	return len(parts) == 4 && parts[1] == boundAlgorithmTag
}

// EncryptBound seals plaintext with aad cryptographically bound to it.
//
// The returned string has the same shape as Encrypt's, with a different
// algorithm tag: "<version>$AES256GCM-AAD$<wrapped DEK>$<sealed data>".
// Decrypting it requires the identical aad, so the ciphertext cannot be
// relocated to another record and still open.
//
// aad is authenticated but NOT encrypted, which is the property that makes
// this work and also the constraint on what may be passed: it must be
// something that identifies the record and is not itself a secret. The
// caller supplies it on both sides and is responsible for it being stable
// for the life of the row; Credential.secret_binding is immutable for that
// reason.
//
// An empty aad is refused. Accepting it would produce a ciphertext that
// looks bound, carries the bound tag, and binds nothing, which is worse
// than an unbound one because it reads as protected.
func (s *EnvelopeService) EncryptBound(plaintext, aad []byte) (string, error) {
	if len(aad) == 0 {
		return "", fmt.Errorf("envelope: refusing to seal with empty associated data, which would bind the ciphertext to nothing")
	}

	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return "", fmt.Errorf("envelope: failed to generate data encryption key: %w", err)
	}
	defer zeroBytes(dek)

	aead, err := newGCM(dek)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("envelope: failed to generate nonce: %w", err)
	}
	// Seal appends to nonce, so the nonce travels with the ciphertext, the
	// same layout aesService.Encrypt already uses.
	sealedData := aead.Seal(nonce, nonce, plaintext, aad)

	// The DEK wrap is unchanged and deliberately still goes through
	// Service: the KEK's own sealing has no record to bind to, and
	// changing it would change the format of every existing ciphertext.
	wrappedDEK, err := s.currentKEK.Encrypt(dek)
	if err != nil {
		return "", fmt.Errorf("envelope: failed to wrap data key: %w", err)
	}

	return fmt.Sprintf("%s$%s$%s$%s",
		s.currentVersion,
		boundAlgorithmTag,
		base64.StdEncoding.EncodeToString(wrappedDEK),
		base64.StdEncoding.EncodeToString(sealedData),
	), nil
}

// DecryptBound reverses EncryptBound, requiring the identical aad.
//
// Every failure is a returned error rather than zero-value plaintext, and
// the important one is the authentication failure: a ciphertext relocated
// from another record fails here with the same error a corrupted one does,
// which is correct. The caller cannot tell an attack from a bit flip and
// should not act differently on either.
func (s *EnvelopeService) DecryptBound(ciphertext string, aad []byte) ([]byte, error) {
	if len(aad) == 0 {
		return nil, fmt.Errorf("envelope: refusing to open with empty associated data")
	}

	parts := strings.SplitN(ciphertext, "$", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("%w: expected 4 fields separated by %q, got %d", ErrMalformedEnvelope, "$", len(parts))
	}
	version, algorithm, wrappedB64, dataB64 := parts[0], parts[1], parts[2], parts[3]

	// An unbound ciphertext reaching here is refused rather than opened
	// without its binding. See boundAlgorithmTag for why the two forms
	// must fail closed against each other.
	if algorithm != boundAlgorithmTag {
		return nil, fmt.Errorf("%w: %q is not a bound envelope", ErrUnknownAlgorithm, algorithm)
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

	aead, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	if len(sealedData) < aead.NonceSize() {
		return nil, fmt.Errorf("%w: sealed data is shorter than a nonce", ErrMalformedEnvelope)
	}

	nonce, body := sealedData[:aead.NonceSize()], sealedData[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, body, aad)
	if err != nil {
		// The message names neither the associated data nor the
		// ciphertext. A failure here means either tampering or a
		// relocated row, and both are things to report by their effect
		// rather than by quoting the material involved.
		return nil, fmt.Errorf("envelope: failed to open sealed data: the ciphertext does not belong to this record, or it has been altered")
	}
	return plaintext, nil
}

// IsBound reports whether ciphertext is in the bound form.
//
// It exists for the migration path: a reader holding rows written before
// this change needs to tell the two apart to know which method to call,
// and parsing the tag in every such caller would spread the format across
// the codebase.
func IsBound(ciphertext string) bool {
	parts := strings.SplitN(ciphertext, "$", 4)
	return len(parts) == 4 && parts[1] == boundAlgorithmTag
}

// newGCM builds an AEAD from a raw key.
//
// This duplicates two lines of NewAESService rather than calling it,
// because Service deliberately exposes no way to pass associated data and
// widening that interface would change a format with files already written
// in it. The duplication is two standard-library calls with no policy in
// them; the alternative was a policy change to a load-bearing interface.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envelope: failed to create cipher block: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: failed to create GCM mode: %w", err)
	}
	return aead, nil
}
