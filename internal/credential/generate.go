// Generating a credential for a machine Pleiades is about to create, so
// no person has to choose, type or see its secrets.
package credential

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"strings"

	"golang.org/x/crypto/ssh"
)

// entropy is where generated keys and passwords draw their randomness; a
// test replaces it with a source that fails.
var entropy io.Reader = rand.Reader

// GeneratedPasswordLength is how many characters a generated password
// has: about 138 bits from passwordAlphabet.
const GeneratedPasswordLength = 24

// passwordAlphabet is letters and digits without the ones read alike
// (0 and O, 1, l and I), so a password can be typed at a VM's console
// under any keyboard layout and read off a screen without a mistake.
const passwordAlphabet = upper + lower + digits

// Generate returns a credential for username holding a new ed25519
// private key, in OpenSSH's own form and labelled comment, and a new
// random password, both drawn from crypto/rand.
//
// The key is what logs in; the password is for what a key cannot reach,
// a VM's console. Both are stored encrypted like any other credential,
// and neither is ever printed: PublicKey gives the one part that may be.
func Generate(username, comment string) (Credential, error) {
	if username == "" {
		return Credential{}, fmt.Errorf("credential: a generated credential needs a username")
	}
	_, private, err := ed25519.GenerateKey(entropy)
	if err != nil {
		return Credential{}, fmt.Errorf("credential: generating a key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(private, comment)
	if err != nil {
		return Credential{}, fmt.Errorf("credential: encoding the key: %w", err)
	}
	password, err := randomPassword(GeneratedPasswordLength)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Username: username, Password: password, PrivateKeyPEM: pem.EncodeToMemory(block)}, nil
}

// randomPassword returns length characters of passwordAlphabet, each
// chosen uniformly by crypto/rand, drawn again until they hold an upper
// case letter, a lower case letter and a digit. Windows' default policy
// refuses a password with fewer than three kinds of character, and about
// one draw in 38 of 24 characters from this alphabet has no digit, so a
// VM seeded with it would be refused its password.
func randomPassword(length int) (string, error) {
	for {
		var b strings.Builder
		limit := big.NewInt(int64(len(passwordAlphabet)))
		for b.Len() < length {
			n, err := rand.Int(entropy, limit)
			if err != nil {
				return "", fmt.Errorf("credential: generating a password: %w", err)
			}
			b.WriteByte(passwordAlphabet[n.Int64()])
		}
		if p := b.String(); length < 3 || (strings.ContainsAny(p, upper) && strings.ContainsAny(p, lower) && strings.ContainsAny(p, digits)) {
			return p, nil
		}
	}
}

// The kinds of character in passwordAlphabet.
const (
	upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	lower  = "abcdefghijkmnopqrstuvwxyz"
	digits = "23456789"
)

// PublicKey returns the authorized_keys line for cred's private key,
// labelled comment. It is not a secret: it is what a machine is given so
// that the key may log in to it.
func PublicKey(cred Credential, comment string) (string, error) {
	if len(cred.PrivateKeyPEM) == 0 {
		return "", fmt.Errorf("credential: holds no private key")
	}
	var signer ssh.Signer
	var err error
	if cred.Passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(cred.PrivateKeyPEM, []byte(cred.Passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(cred.PrivateKeyPEM)
	}
	if err != nil {
		return "", fmt.Errorf("credential: reading the private key: %w", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if comment != "" {
		line += " " + comment
	}
	return line, nil
}
