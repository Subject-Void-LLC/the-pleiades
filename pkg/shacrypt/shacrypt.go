// Package shacrypt computes SHA-512 crypt password hashes, the "$6$"
// scheme that glibc's crypt(3), cloud-init and every current Linux
// distribution accept in /etc/shadow.
//
// It implements Ulrich Drepper's specification "Unix crypt using SHA-256
// and SHA-512" with the standard library only. Pleiades needs it to seed
// a new virtual machine's cloud-init user-data with a password hash, so
// the password itself is never written into a file the guest or the host
// keeps.
//
// The scope is deliberately small. Only SHA-512 is implemented, and only
// the default 5000 rounds, so an output never carries a "rounds=" field
// and Verify never accepts a hash that does.
//
// A password is hashed byte for byte as Go holds it. The C crypt(3)
// stops reading a password at its first NUL byte, so a password
// containing one produces a hash no Linux login can match.
package shacrypt

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strings"
)

// prefix is the identifier every SHA-512 crypt hash starts with, and the
// only one this package produces or verifies.
const prefix = "$6$"

// saltMax is the longest salt the specification uses. A longer salt is
// cut to this length, exactly as glibc does, rather than refused.
const saltMax = 16

// rounds is the specification's default round count. Using only the
// default is what lets the output omit the "rounds=" field.
const rounds = 5000

// alphabet is the crypt alphabet, in the order the specification's
// custom base64 encoding assigns values 0 to 63. Salts are drawn from it
// and checked against it.
const alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// encodeOrder lists, three bytes at a time, the order in which the
// specification feeds the final digest's bytes into its base64 encoding.
// Each triple becomes four characters. It is copied from the
// specification's reference implementation because the order has no
// shorter derivation a reader could check at a glance. The digest's last
// byte, index 63, is encoded on its own afterwards (see encode).
var encodeOrder = [21][3]int{
	{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45},
	{25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7},
	{50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32},
	{12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57},
	{37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19},
	{62, 20, 41},
}

// Hash returns the SHA-512 crypt hash of password as "$6$<salt>$<hash>",
// with a fresh 16-character salt drawn from crypto/rand and the default
// 5000 rounds. The error is returned only when the system's random
// source cannot be read.
func Hash(password string) (string, error) {
	return hashFrom(rand.Reader, password)
}

// hashFrom is Hash with the randomness source passed in, so a test can
// hand it a failing reader and prove the error path.
func hashFrom(random io.Reader, password string) (string, error) {
	salt := make([]byte, saltMax)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("shacrypt: read random salt: %w", err)
	}
	// The alphabet has exactly 64 characters and 256 is a multiple of 64,
	// so keeping a random byte's low six bits picks each character with
	// equal chance.
	for i, b := range salt {
		salt[i] = alphabet[b&0x3f]
	}
	// The salt is built from the alphabet, so HashWithSalt cannot refuse it.
	return HashWithSalt(password, string(salt))
}

// HashWithSalt returns the SHA-512 crypt hash of password with the given
// salt and the default 5000 rounds, as "$6$<salt>$<hash>".
//
// A salt longer than 16 characters is cut to 16, as the specification
// and glibc do. HashWithSalt returns an error, and no hash, for an empty
// salt or for a salt holding any character outside the crypt alphabet
// "./0-9A-Za-z" (which covers "$"). The whole salt is checked, including
// any part that truncation drops, so a malformed salt is never half used.
// glibc would accept an empty salt; it is refused here because a hash
// with no salt is the same for every account sharing a password.
func HashWithSalt(password, salt string) (string, error) {
	if err := checkSalt(salt); err != nil {
		return "", err
	}
	if len(salt) > saltMax {
		salt = salt[:saltMax]
	}
	sum := digest([]byte(password), []byte(salt))
	return prefix + salt + "$" + encode(sum), nil
}

// Verify reports whether password matches hash, a "$6$<salt>$<hash>"
// string using the default rounds. It returns false for anything else,
// including a hash with a "rounds=" field, which this package does not
// support. The final comparison takes constant time.
func Verify(password, hash string) bool {
	rest, ok := strings.CutPrefix(hash, prefix)
	if !ok {
		return false
	}
	salt, _, ok := strings.Cut(rest, "$")
	if !ok {
		return false
	}
	// A "rounds=" field reads as a salt holding "=", which HashWithSalt
	// refuses, so it lands here as a mismatch.
	want, err := HashWithSalt(password, salt)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(hash)) == 1
}

// checkSalt refuses an empty salt or one holding a character outside the
// crypt alphabet, for the reasons HashWithSalt documents.
func checkSalt(salt string) error {
	if salt == "" {
		return errors.New("shacrypt: salt is empty")
	}
	for i := 0; i < len(salt); i++ {
		if strings.IndexByte(alphabet, salt[i]) < 0 {
			return fmt.Errorf("shacrypt: salt byte %d is %q, which is outside the crypt alphabet ./0-9A-Za-z", i, salt[i])
		}
	}
	return nil
}

// digest runs the specification's steps 1 to 21 over password p and salt
// s, returning the final 64-byte digest that encode turns into text. The
// step numbers in the comments are the specification's.
func digest(p, s []byte) [sha512.Size]byte {
	// Steps 4 to 8: the alternate sum B = SHA-512(P + S + P).
	alt := sha512.New()
	alt.Write(p)
	alt.Write(s)
	alt.Write(p)
	b := alt.Sum(nil)

	// Steps 1 to 3: the intermediate sum A starts as SHA-512(P + S ...).
	a := sha512.New()
	a.Write(p)
	a.Write(s)
	// Steps 9 and 10: then B once per whole 64 bytes of password, then
	// the first len(P) % 64 bytes of B, which is len(P) bytes of B
	// repeated.
	a.Write(repeatTo(b, len(p)))
	// Step 11: then, for each bit of len(P) from the lowest up to its
	// highest set bit, B for a 1 bit and P for a 0 bit.
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			a.Write(b)
		} else {
			a.Write(p)
		}
	}
	// Step 12: finish A.
	sumA := a.Sum(nil)

	// Steps 13 to 16: the P byte sequence is len(P) bytes of
	// DP = SHA-512(P repeated len(P) times).
	dp := sha512.New()
	for range len(p) {
		dp.Write(p)
	}
	pSeq := repeatTo(dp.Sum(nil), len(p))

	// Steps 17 to 20: the S byte sequence is len(S) bytes of
	// DS = SHA-512(S repeated 16 + A[0] times).
	ds := sha512.New()
	for range 16 + int(sumA[0]) {
		ds.Write(s)
	}
	sSeq := repeatTo(ds.Sum(nil), len(s))

	// Step 21: the 5000 rounds. Each round hashes the previous result C
	// (A to start) with the P and S sequences in an order set by whether
	// the round number is odd, divisible by 3, and divisible by 7.
	c := sumA
	for i := range rounds {
		r := sha512.New()
		if i%2 != 0 {
			r.Write(pSeq)
		} else {
			r.Write(c)
		}
		if i%3 != 0 {
			r.Write(sSeq)
		}
		if i%7 != 0 {
			r.Write(pSeq)
		}
		if i%2 != 0 {
			r.Write(c)
		} else {
			r.Write(pSeq)
		}
		// r already copied c into its state, so the new digest can reuse
		// c's memory instead of allocating 5000 slices.
		c = r.Sum(c[:0])
	}
	return [sha512.Size]byte(c)
}

// repeatTo returns n bytes made of d repeated end to end, the last copy
// cut short. It builds the specification's "whole blocks, then the first
// N bytes" sequences (steps 9 and 10, 16 and 20) in one place.
func repeatTo(d []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, d[:min(len(d), n-len(out))]...)
	}
	return out
}

// encode is the specification's step 22: it turns the final digest into
// 86 characters of custom base64, in which each triple from encodeOrder becomes four characters,
// least significant six bits first, and the last byte becomes two.
func encode(sum [sha512.Size]byte) string {
	var out strings.Builder
	out.Grow(86)
	for _, t := range encodeOrder {
		putBase64(&out, uint(sum[t[0]])<<16|uint(sum[t[1]])<<8|uint(sum[t[2]]), 4)
	}
	putBase64(&out, uint(sum[63]), 2)
	return out.String()
}

// putBase64 writes the low 6*n bits of w as n crypt-alphabet characters,
// least significant six bits first, which is the specification's
// b64_from_24bit.
func putBase64(out *strings.Builder, w uint, n int) {
	for range n {
		out.WriteByte(alphabet[w&0x3f])
		w >>= 6
	}
}
