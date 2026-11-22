// Package remoteexec: what a stored key refused a legacy signature says, as a
// login's error and as a connection's warning.
//
// The words are careful about what is known. With no server-sig-algs
// extension the library asks the server only about ssh-rsa, so a refusal
// knows the server would take the key with ssh-rsa and that it did not
// advertise SHA-2 for it, never that it takes the key only with ssh-rsa. An
// advertisement a machine in the middle removed looks exactly the same, so
// the refusal says to set the property only for a device known not to take
// SHA-2.
package remoteexec

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// describeSignature says what a legacy signature algorithm is, for a message.
func describeSignature(algorithm string) string {
	if algorithm == ssh.KeyAlgoRSA {
		return "an ssh-rsa (SHA-1) signature"
	}
	return "an " + algorithm + " (DSA) signature"
}

// serverTook says what the server answered for key, refused algorithm: for an
// RSA key, that it advertised no SHA-2 signature for it and would take it with
// ssh-rsa; for a DSA key, which has no other signature, that it would take it.
func serverTook(key, algorithm string) string {
	if algorithm == ssh.KeyAlgoRSA {
		return fmt.Sprintf("did not advertise a SHA-2 signature for this %s and would take it with %s", key, describeSignature(algorithm))
	}
	return fmt.Sprintf("would take this %s, which signs only with %s", key, describeSignature(algorithm))
}

// removedInTransit is the caution an RSA refusal carries: a device known to
// take SHA-2 that reaches it may have had its advertisement removed by a
// machine in the middle, and the property would then let that machine win.
const removedInTransit = "if the device is known to take SHA-2 (OpenSSH 7.2 or later does), its advertisement may have been removed in transit, so do not set the property but find out why"

// controllersFirst is what a refusal naming the property adds for the Walk
// tier: a Runner reads the record from its Controller, and one that predates
// the property does not pass it on.
const controllersFirst = "on the Walk tier a Runner reads the property from its Controller, and a Controller that predates it does not pass it on, so upgrade Controllers before Runners"

// LegacySignatureRefusedError is a stored key this side would not sign a
// login with: the server at Addr did not advertise a SHA-2 signature for it
// and would take it with a legacy one (ssh-rsa, or a DSA key's ssh-dss),
// which the device's record does not allow, or which FIPS 140 mode forbids
// whatever the record says. It is an answer rather than the network failing:
// the server answers the same on every attempt.
type LegacySignatureRefusedError struct {
	// Addr is the address dialed.
	Addr string
	// Key names the stored key: "RSA key", "DSA key", or a certificate.
	Key string
	// Algorithm is the legacy signature the server would take.
	Algorithm string
	// Property is the device property that allows it, empty under FIPS 140
	// mode, where setting it would not help.
	Property string
	// fips is set when FIPS 140 mode refused it.
	fips bool
	// noRecord is set for a login no device record governs (HoldToSHA2), a
	// project's git server, where nothing can allow it.
	noRecord bool
	// err is the login's own failure when another method was tried after the
	// key and failed too, nil when the key was the only one.
	err error
}

// Error says what the server answered and what, if anything, would allow it.
func (e *LegacySignatureRefusedError) Error() string {
	server := "the SSH server"
	if e.Addr != "" {
		server += " at " + e.Addr
	}
	msg := server + " " + serverTook(e.Key, e.Algorithm)
	switch {
	case e.fips:
		msg += ", which FIPS 140 mode forbids whatever the record says: log in with an ed25519 or ECDSA key, or have the server take rsa-sha2-256"
	case e.noRecord:
		msg += ", which nothing allows here, since no device record governs this login: log in with an ed25519 or ECDSA key, or have the server take rsa-sha2-256"
		if e.Algorithm == ssh.KeyAlgoRSA {
			msg += "; a server known to take SHA-2 (OpenSSH 7.2 or later does) may have had its advertisement removed in transit"
		}
	default:
		msg += ", which this device's record does not allow: "
		if e.Algorithm == ssh.KeyAlgoRSA {
			msg += removedInTransit + "; otherwise "
		}
		msg += fmt.Sprintf("set %s: true on the device to allow it, knowing it is weak, and it is warned about on every connection (%s)", e.Property, controllersFirst)
	}
	if e.err != nil {
		msg += ": " + e.err.Error()
	}
	return msg
}

// Unwrap exposes the login's own failure, when there was one past the key.
func (e *LegacySignatureRefusedError) Unwrap() error { return e.err }

// signatureRefused reports whether err is the signer's own refusal, the key
// being the only method: an answer, not the network failing, so it is neither
// retried nor counted against the address's circuit. A refusal wrapping a
// later failure is judged by that failure (a refused password is an answer
// too, a dropped connection is not).
func signatureRefused(err error) bool {
	refused, ok := errors.AsType[*LegacySignatureRefusedError](err)
	return ok && refused.err == nil
}

// explainLogin returns a failed login's err naming the stored key's refusal
// when there was one and err does not already: a key refused for a legacy
// signature beside a password that was then refused too would otherwise leave
// only the password's refusal, and an operator nothing to act on.
func explainLogin(err error, addr string, key userKeyOutcome) error {
	if err == nil || key.refused == "" {
		return err
	}
	if _, ok := errors.AsType[*LegacySignatureRefusedError](err); ok {
		return err
	}
	return key.refusal(addr, err)
}

// skippedKeyWarnings says, for the named device, that a login did not use its
// stored key because the server did not advertise a SHA-2 signature for it,
// and logged in with the password instead. It is said whatever the record
// allows, so a key that never signs is never quietly replaced by a password.
func skippedKeyWarnings(device string, key userKeyOutcome) []string {
	if key.refused == "" {
		return nil
	}
	why := fmt.Sprintf("which the record does not allow (%s)", LegacyUserKeySignaturesProperty)
	if key.fips {
		why = "which FIPS 140 mode forbids whatever the record says"
	}
	return []string{fmt.Sprintf("device %q did not use the stored %s: the SSH server %s, %s; this connection logged in with the password",
		device, key.key, serverTook(key.key, key.refused), why)}
}
