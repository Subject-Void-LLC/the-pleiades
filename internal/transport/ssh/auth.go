package ssh

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// buildAuthMethod translates cred into exactly one ssh.AuthMethod,
// preferring password authentication when Password is set, falling back
// to key authentication when PrivateKeyPEM is set, and returning an
// explicit error when neither is set. It never returns a nil AuthMethod
// with a nil error: proceeding with no usable authentication would let a
// misconfigured credential silently attempt an unauthenticated
// connection, which this function refuses to do.
func buildAuthMethod(cred credential.Credential) (ssh.AuthMethod, error) {
	switch {
	case cred.Password != "":
		return ssh.Password(cred.Password), nil

	case len(cred.PrivateKeyPEM) > 0:
		var signer ssh.Signer
		var err error
		if cred.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(cred.PrivateKeyPEM, []byte(cred.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(cred.PrivateKeyPEM)
		}
		if err != nil {
			// A parse failure is a wrapped, explicit error; this never
			// silently falls through to no-auth or to a different auth
			// method than the caller asked for.
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return ssh.PublicKeys(signer), nil

	default:
		return nil, errors.New("no usable authentication method for this credential")
	}
}
