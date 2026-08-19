package remoteexec

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Auth is a username paired with exactly one resolved SSH
// authentication method.
//
// Both fields are unexported, and that is the point. The secret is
// consumed when the Auth is built and is never readable back out, so a
// caller cannot accidentally print, log, marshal or copy a password out
// of this type. That is a different answer to the same problem
// internal/credential.Credential solves with four redaction methods, and
// it is the better one available here: there is nothing to redact when
// there is nothing to read.
//
// The zero Auth is not usable and is refused before any dial; see
// Runner.Connect.
type Auth struct {
	user   string
	method ssh.AuthMethod
}

// usable reports whether this Auth carries a real authentication method.
// A zero Auth is not usable, which is what stops an unauthenticated
// connection attempt from ever leaving the process.
func (a Auth) usable() bool {
	return a.method != nil
}

// clientConfig builds the ssh.ClientConfig for one connection: this
// Auth's user and method, the caller's host key check, and dialTimeout
// as a fallback bound.
//
// It is a method on Auth rather than a free function so the method
// itself never has to leave this type, not even to be assembled into a
// config by code elsewhere in the package.
func (a Auth) clientConfig(hostKey ssh.HostKeyCallback, dialTimeout time.Duration) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            a.user,
		Auth:            []ssh.AuthMethod{a.method},
		HostKeyCallback: hostKey,
		// Timeout is a fallback bound only. The caller's ctx is honored
		// inside realDial and dialWithRetry, so a context deadline aborts
		// an in-flight dial rather than waiting this out. It matters when
		// the caller passes a context with no deadline at all.
		Timeout: dialTimeout,
	}
}

// PasswordAuth returns an Auth that authenticates as user with password.
func PasswordAuth(user, password string) Auth {
	return Auth{user: user, method: ssh.Password(password)}
}

// PrivateKeyAuth returns an Auth that authenticates as user with the
// given PEM-encoded private key, decrypting it with passphrase when
// passphrase is not empty.
//
// A key that cannot be parsed is an explicit, wrapped error. It never
// falls through to no authentication or to a different method than the
// caller asked for.
func PrivateKeyAuth(user string, privateKeyPEM []byte, passphrase string) (Auth, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(privateKeyPEM, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(privateKeyPEM)
	}
	if err != nil {
		return Auth{}, fmt.Errorf("parse private key: %w", err)
	}
	return Auth{user: user, method: ssh.PublicKeys(signer)}, nil
}

// AuthFrom turns the four pieces of authentication material this
// platform stores into exactly one Auth, preferring a password and
// falling back to a private key.
//
// This is the single place that preference order is written down. It has
// two callers with two different sources (a credential store on one
// side, a runbook context's injected secrets on the other), and having
// each of them decide independently is how the two would drift.
//
// It never returns a zero Auth with a nil error. When neither a password
// nor a key is present the caller gets an explicit refusal, because
// connecting with no authentication at all is never the right recovery
// from a device whose credential was not found.
func AuthFrom(user, password string, privateKeyPEM []byte, passphrase string) (Auth, error) {
	switch {
	case password != "":
		return PasswordAuth(user, password), nil

	case len(privateKeyPEM) > 0:
		return PrivateKeyAuth(user, privateKeyPEM, passphrase)

	default:
		return Auth{}, errors.New("no usable authentication method: supply a password or a private key for this device")
	}
}

// AuthFromSecrets builds an Auth from the flattened secret map a
// Collection method receives through sdk.RunbookContext.InjectSecrets.
//
// The keys it reads are wire.Secret*, the same constants the controller
// flattens a credential into on the way out, so the two ends of that map
// cannot drift apart by one side inventing its own spelling. A missing
// key reads as an empty string, which AuthFrom turns into a refusal
// rather than an unauthenticated attempt.
func AuthFromSecrets(secrets map[string]string) (Auth, error) {
	return AuthFrom(
		secrets[wire.SecretUsername],
		secrets[wire.SecretPassword],
		[]byte(secrets[wire.SecretPrivateKeyPEM]),
		secrets[wire.SecretPassphrase],
	)
}
