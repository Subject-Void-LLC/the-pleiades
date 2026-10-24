// Package iossecret writes and checks the password hashes Cisco IOS and
// IOS XE keep for "username ... secret" and "enable secret": type 8
// (PBKDF2 with SHA-256, 20000 rounds) and type 9 (scrypt, N 16384, r 1,
// p 1), each over a 14-character salt and written "$8$<salt>$<hash>" or
// "$9$<salt>$<hash>" in Cisco's base64 alphabet. A device given one of
// these never sees the password, so a configuration a lab stores (CML's
// day-0 configurations) holds only the hash.
//
// Both forms are proven against hashes a real IOS XE 17.15 router produced
// for known passwords (iossecret_test.go), not only against this package's
// own output.
package iossecret

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// The two hash types, by the number IOS writes before the hash.
const (
	// Type8 is PBKDF2 with HMAC-SHA256.
	Type8 = 8
	// Type9 is scrypt, the stronger, and what IOS XE writes by default
	// with algorithm-type scrypt.
	Type9 = 9
)

// The parameters IOS uses, fixed by the format: a hash names no cost.
const (
	pbkdf2Rounds = 20000
	scryptN      = 16384
	scryptR      = 1
	scryptP      = 1
	keyLen       = 32
	// SaltLen is how many characters a salt has.
	SaltLen = 14
)

// alphabet is Cisco's base64 alphabet, the crypt(3) one, in the order its
// values run; IOS encodes with the standard bit order otherwise, unpadded.
const alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// encoding is base64 in Cisco's alphabet, without padding.
var encoding = base64.NewEncoding(alphabet).WithPadding(base64.NoPadding)

// NewSalt returns a random salt of SaltLen characters of Cisco's alphabet.
func NewSalt() (string, error) {
	var raw [SaltLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("iossecret: reading random bytes for a salt: %w", err)
	}
	salt := make([]byte, SaltLen)
	for i, b := range raw {
		// 256 is a multiple of 64, so taking the low six bits keeps every
		// character equally likely.
		salt[i] = alphabet[b&63]
	}
	return string(salt), nil
}

// Hash returns password's hash of typ (Type8 or Type9) under a new random
// salt, in the form IOS writes after "secret 8 " or "secret 9 ".
func Hash(typ int, password string) (string, error) {
	salt, err := NewSalt()
	if err != nil {
		return "", err
	}
	return HashWithSalt(typ, password, salt)
}

// HashWithSalt returns password's hash of typ under salt, which must be
// SaltLen characters of Cisco's alphabet.
func HashWithSalt(typ int, password, salt string) (string, error) {
	if password == "" {
		return "", errors.New("iossecret: an empty password has no secret worth hashing")
	}
	if len(salt) != SaltLen || strings.Trim(salt, alphabet) != "" {
		return "", fmt.Errorf("iossecret: a salt is %d characters of %s", SaltLen, alphabet)
	}
	var key []byte
	var err error
	switch typ {
	case Type8:
		key, err = pbkdf2.Key(sha256.New, password, []byte(salt), pbkdf2Rounds, keyLen)
	case Type9:
		key, err = scrypt.Key([]byte(password), []byte(salt), scryptN, scryptR, scryptP, keyLen)
	default:
		return "", fmt.Errorf("iossecret: type %d is not 8 or 9", typ)
	}
	if err != nil {
		return "", fmt.Errorf("iossecret: type %d: %w", typ, err)
	}
	return fmt.Sprintf("$%d$%s$%s", typ, salt, encoding.EncodeToString(key)), nil
}

// Verify reports whether hash, in the form IOS writes ("$9$<salt>$<hash>"),
// is password's, comparing in constant time. A hash it cannot read is an
// error, never a mismatch.
func Verify(password, hash string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "" || (parts[1] != "8" && parts[1] != "9") {
		return false, errors.New(`iossecret: a hash is "$8$" or "$9$", a salt, "$" and the hash`)
	}
	want, err := HashWithSalt(int(parts[1][0]-'0'), password, parts[2])
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(hash)) == 1, nil
}
