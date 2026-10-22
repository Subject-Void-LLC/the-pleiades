// Package sealed writes and opens a sealed secret: a file a person hands to
// pleiades, or to an agent driving it, holding a secret that reading the
// file does not reveal. Whoever seals chooses one of two ways:
//
//   - to a project's public key, with HPKE (RFC 9180): ML-KEM-768 with X25519,
//     HKDF-SHA256 and AES-256-GCM. Anyone holding the public key can seal on
//     any machine, and only the project holding the private key opens the
//     file, so the plaintext need never exist where the project runs.
//   - to a passphrase, with argon2id and AES-256-GCM, for a person who would
//     rather type a passphrase where the file is opened than handle a key.
//
// A sealed file is one PEM block. Its headers say how it was sealed and what
// it is for, and every header is bound into the encryption (HPKE's info, or
// AES-GCM's additional data), so a changed header makes the file refuse to
// open rather than open as something else: a password cannot be relabelled
// as another kind of secret, nor a weaker key derivation substituted.
//
// For another implementation to open one: a key-sealed body is HPKE base
// mode's single-shot output, the encapsulated key followed by the
// ciphertext, with KEM 0x647a (ML-KEM-768 with X25519), KDF 0x0001
// (HKDF-SHA256) and AEAD 0x0002 (AES-256-GCM), info set to the binding
// bytes (binding below) and no additional data. A passphrase-sealed body is
// a 12-byte nonce followed by the AES-256-GCM ciphertext, under the key
// argon2id derives from the passphrase with the file's own Salt and
// Argon2id parameters, the binding bytes as additional data. A file's
// Argon2id parameters are held to the bounds in passphrase.go when the file
// is parsed, before any memory is set aside for the derivation, so a
// crafted file cannot make opening it exhaust the machine.
package sealed

import (
	"bytes"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The format's fixed values. Version and the method names are written into
// every file and bound into its encryption, so changing any of them is a new
// format, never an edit.
const (
	pemType = "PLEIADES SEALED SECRET"
	// Version is the one format version this package writes and reads.
	Version = "1"
	// MethodKey seals to a project's public key with HPKE.
	MethodKey = "hpke-mlkem768x25519-hkdfsha256-aes256gcm"
	// MethodPassphrase seals to a passphrase with argon2id and AES-256-GCM.
	MethodPassphrase = "argon2id-aes256gcm"
	// PurposePassword is a login password, the one purpose add-credential
	// opens today.
	PurposePassword = "password"
	// MaxFileSize bounds a sealed file read from disk, far above any secret
	// this package seals, so a wrong path naming a large file is refused
	// rather than read.
	MaxFileSize = 1 << 20
	// MaxSecretSize bounds the secret a file may hold.
	MaxSecretSize = 64 << 10
)

// Header names. Every one present is bound into the encryption.
const (
	headerVersion   = "Version"
	headerMethod    = "Method"
	headerPurpose   = "Purpose"
	headerRecipient = "Recipient"
	headerArgon2id  = "Argon2id"
	headerSalt      = "Salt"
)

// purposes are the kinds of secret a file may say it holds.
var purposes = []string{PurposePassword}

var (
	// ErrNotSealed reports data that is not a sealed file at all, such as a
	// plaintext password, so a caller can say what to do instead.
	ErrNotSealed = errors.New("not a sealed file")
	// ErrWrongKey reports a file sealed to another public key than the one
	// offered to open it.
	ErrWrongKey = errors.New("sealed to another key")
	// ErrCannotOpen reports a file that did not decrypt: the wrong
	// passphrase, or a file changed after it was sealed.
	ErrCannotOpen = errors.New("cannot open the sealed file")
)

// File is a parsed sealed file, not yet opened.
type File struct {
	// Method is how it was sealed: MethodKey or MethodPassphrase.
	Method string
	// Purpose is what the secret is for, such as PurposePassword.
	Purpose string
	// Recipient is the fingerprint of the public key a MethodKey file was
	// sealed to, and empty for a passphrase.
	Recipient string

	headers map[string]string
	body    []byte
}

// Parse reads a sealed file without opening it. It refuses anything this
// version does not write: another version, method or purpose, a header it
// does not know, or data around the block, since an unknown field bound into
// the encryption is one this reader cannot judge.
func Parse(data []byte) (*File, error) {
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("%w: larger than %d bytes", ErrNotSealed, MaxFileSize)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != pemType {
		return nil, ErrNotSealed
	}
	// pem.Decode skips whatever comes before the block, so that is
	// checked here, as what follows it is: the file starts with the block.
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("-----BEGIN "+pemType+"-----")) {
		return nil, fmt.Errorf("unexpected data before the sealed block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("unexpected data after the sealed block")
	}
	h := block.Headers
	if h[headerVersion] != Version {
		return nil, fmt.Errorf("sealed file version %q is not one this pleiades reads (it reads %q)", h[headerVersion], Version)
	}
	if !slices.Contains(purposes, h[headerPurpose]) {
		return nil, fmt.Errorf("sealed file purpose %q is not one this pleiades knows", h[headerPurpose])
	}
	var allowed []string
	switch h[headerMethod] {
	case MethodKey:
		if h[headerRecipient] == "" {
			return nil, fmt.Errorf("a key-sealed file must name its recipient")
		}
		allowed = []string{headerVersion, headerMethod, headerPurpose, headerRecipient}
	case MethodPassphrase:
		if _, err := parseArgon2id(h[headerArgon2id]); err != nil {
			return nil, err
		}
		if _, err := decodeSalt(h[headerSalt]); err != nil {
			return nil, err
		}
		allowed = []string{headerVersion, headerMethod, headerPurpose, headerArgon2id, headerSalt}
	default:
		return nil, fmt.Errorf("sealed file method %q is not one this pleiades knows", h[headerMethod])
	}
	for name := range h {
		if !slices.Contains(allowed, name) {
			return nil, fmt.Errorf("sealed file header %q is not one this pleiades knows", name)
		}
	}
	return &File{
		Method:    h[headerMethod],
		Purpose:   h[headerPurpose],
		Recipient: h[headerRecipient],
		headers:   h,
		body:      block.Bytes,
	}, nil
}

// checkSecret refuses a secret no caller could mean to seal.
func checkSecret(purpose string, secret []byte) error {
	if !slices.Contains(purposes, purpose) {
		return fmt.Errorf("purpose %q is not one of %s", purpose, strings.Join(purposes, ", "))
	}
	if len(secret) == 0 {
		return fmt.Errorf("an empty secret is not allowed")
	}
	if len(secret) > MaxSecretSize {
		return fmt.Errorf("a secret of %d bytes is larger than the %d a sealed file holds", len(secret), MaxSecretSize)
	}
	return nil
}

// binding is the exact bytes every header contributes to the encryption:
// a fixed label, then each header as "name: value" in name order.
func binding(headers map[string]string) []byte {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("pleiades sealed secret\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s: %s\n", name, headers[name])
	}
	return []byte(b.String())
}

// encode writes one sealed file.
func encode(headers map[string]string, body []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: pemType, Headers: headers, Bytes: body})
}
