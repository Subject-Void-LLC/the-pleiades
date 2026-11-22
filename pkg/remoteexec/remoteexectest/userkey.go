// A stored RSA user key for a test to log in to a Server with, so a test of
// the legacy user key signature rule in any package makes its key the same
// way.
package remoteexectest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"

	cryptossh "golang.org/x/crypto/ssh"
)

// RSAUserKey returns a new 2048-bit RSA private key in the OpenSSH PEM form
// a credential stores, and its public half for Options.AuthorizedKey. With
// Options.PublicKeyAuthAlgorithms set to ssh-rsa alone, a Server takes this
// key only with a SHA-1 signature.
func RSAUserKey() (privatePEM []byte, public cryptossh.PublicKey, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("remoteexectest: generating an RSA user key: %w", err)
	}
	block, err := cryptossh.MarshalPrivateKey(key, "")
	if err != nil {
		return nil, nil, fmt.Errorf("remoteexectest: encoding the RSA user key: %w", err)
	}
	public, err = cryptossh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("remoteexectest: the RSA user key's public half: %w", err)
	}
	return pem.EncodeToMemory(block), public, nil
}
