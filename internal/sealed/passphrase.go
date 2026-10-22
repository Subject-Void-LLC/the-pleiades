// Sealing to a passphrase with argon2id and AES-256-GCM.
package sealed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPassphraseLength is the shortest passphrase SealWithPassphrase takes,
// in characters. A sealed file can be copied and guessed at offline for as
// long as anyone likes, so a short passphrase protects it for very little.
const MinPassphraseLength = 12

// argon2idParams are the cost of one key derivation.
type argon2idParams struct {
	time      uint32
	memoryKiB uint32
	threads   uint8
}

// sealParams is what SealWithPassphrase writes: RFC 9106's second
// recommended setting (three passes over 64 MiB).
var sealParams = argon2idParams{time: 3, memoryKiB: 64 << 10, threads: 4}

// The bounds a file's own parameters must fall within before anything is
// derived. A file states its own cost, so without a ceiling a crafted one
// could ask for more memory than the machine opening it has.
const (
	maxTime      = 10
	minMemoryKiB = 8 << 10
	maxMemoryKiB = 256 << 10
	maxThreads   = 16
	saltSize     = 16
)

func (p argon2idParams) String() string {
	return fmt.Sprintf("t=%d,m=%d,p=%d", p.time, p.memoryKiB, p.threads)
}

// parseArgon2id reads the Argon2id header, refusing parameters outside the
// bounds above.
func parseArgon2id(s string) (argon2idParams, error) {
	var p argon2idParams
	fields := strings.Split(s, ",")
	if len(fields) != 3 {
		return p, fmt.Errorf("sealed file Argon2id header %q is not t=,m=,p=", s)
	}
	values := map[string]uint64{}
	for i, name := range []string{"t", "m", "p"} {
		value, ok := strings.CutPrefix(fields[i], name+"=")
		if !ok {
			return p, fmt.Errorf("sealed file Argon2id header %q is not t=,m=,p=", s)
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return p, fmt.Errorf("sealed file Argon2id header %q: %w", s, err)
		}
		values[name] = n
	}
	if values["t"] < 1 || values["t"] > maxTime ||
		values["m"] < minMemoryKiB || values["m"] > maxMemoryKiB ||
		values["p"] < 1 || values["p"] > maxThreads {
		return p, fmt.Errorf("sealed file Argon2id parameters %q are outside what this pleiades derives (t 1-%d, m %d-%d KiB, p 1-%d)",
			s, maxTime, minMemoryKiB, maxMemoryKiB, maxThreads)
	}
	return argon2idParams{time: uint32(values["t"]), memoryKiB: uint32(values["m"]), threads: uint8(values["p"])}, nil
}

// decodeSalt reads the Salt header.
func decodeSalt(s string) ([]byte, error) {
	salt, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(salt) != saltSize {
		return nil, fmt.Errorf("sealed file Salt header is not %d bytes of base64", saltSize)
	}
	return salt, nil
}

// passphraseAEAD derives the file key from passphrase and returns its cipher.
func passphraseAEAD(passphrase string, salt []byte, p argon2idParams) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, p.time, p.memoryKiB, p.threads, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create the cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// SealWithPassphrase seals secret, for purpose, so that passphrase opens it.
func SealWithPassphrase(passphrase, purpose string, secret []byte) ([]byte, error) {
	if utf8.RuneCountInString(passphrase) < MinPassphraseLength {
		return nil, fmt.Errorf("a passphrase must be at least %d characters: anyone holding the file can guess at it offline", MinPassphraseLength)
	}
	if err := checkSecret(purpose, secret); err != nil {
		return nil, err
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate a salt: %w", err)
	}
	headers := map[string]string{
		headerVersion:  Version,
		headerMethod:   MethodPassphrase,
		headerPurpose:  purpose,
		headerArgon2id: sealParams.String(),
		headerSalt:     base64.RawStdEncoding.EncodeToString(salt),
	}
	aead, err := passphraseAEAD(passphrase, salt, sealParams)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate a nonce: %w", err)
	}
	return encode(headers, aead.Seal(nonce, nonce, secret, binding(headers))), nil
}

// OpenWithPassphrase opens a file sealed to passphrase.
func (f *File) OpenWithPassphrase(passphrase string) ([]byte, error) {
	if f.Method != MethodPassphrase {
		return nil, fmt.Errorf("this file is sealed with %s, not with a passphrase", f.Method)
	}
	// Parse checked both headers; reading them again keeps this method
	// correct for a File built some other way.
	p, err := parseArgon2id(f.headers[headerArgon2id])
	if err != nil {
		return nil, err
	}
	salt, err := decodeSalt(f.headers[headerSalt])
	if err != nil {
		return nil, err
	}
	aead, err := passphraseAEAD(passphrase, salt, p)
	if err != nil {
		return nil, err
	}
	if len(f.body) < aead.NonceSize() {
		return nil, fmt.Errorf("%w: it is too short", ErrCannotOpen)
	}
	nonce, body := f.body[:aead.NonceSize()], f.body[aead.NonceSize():]
	secret, err := aead.Open(nil, nonce, body, binding(f.headers))
	if err != nil {
		return nil, fmt.Errorf("%w: the passphrase is wrong, or the file was changed after it was sealed", ErrCannotOpen)
	}
	return secret, nil
}
