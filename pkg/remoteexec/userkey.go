// Package remoteexec: a stored user key, held to a SHA-2 signature unless the
// device's record allows a legacy one.
//
// An RSA key can sign a login three ways: rsa-sha2-512, rsa-sha2-256, or
// ssh-rsa, which hashes with SHA-1. golang.org/x/crypto/ssh signs with ssh-rsa
// whenever the server does not advertise SHA-2 (client_auth.go's
// pickSignatureAlgorithm): no server-sig-algs extension, which OpenSSH before
// 7.2 and some network devices are said not to send (not measured here), one
// listing ssh-rsa alone (OpenSSH set to PubkeyAcceptedAlgorithms ssh-rsa,
// measured on OpenSSH 10.3), or one listing no RSA algorithm at all. A machine
// in the middle can delete that extension on a server without strict key
// exchange (the Terrapin attack) and talk a client down the same way. So
// every connection wraps the stored key in a userKeySigner. It offers SHA-2
// first, so a server that advertises SHA-2 always gets it, and it refuses a
// legacy signature (ssh-rsa, or a DSA key's ssh-dss) unless the device's
// record sets ssh_allow_legacy_user_key_signatures, which no other legacy
// property implies.
//
// The refusal comes at the signature, after the server has said it would
// take the key with that algorithm, so it names the property only where
// setting it could help. With no server-sig-algs the library asks only about
// ssh-rsa, so a refusal never says the server takes the key only that way:
// it says the server did not advertise SHA-2 for it, which a removed
// advertisement also explains (userkeyerror.go). A key refused this way beside
// a password lets the password log in, as a key the server refuses would, and
// the connection says so in its warnings (Conn.LegacyWarnings) whatever the
// record allows.
package remoteexec

import (
	"crypto/fips140"
	"fmt"
	"io"
	"slices"
	"sync"

	"golang.org/x/crypto/ssh"
)

// LegacyUserKeySignaturesProperty allows a stored user key to sign a login
// with ssh-rsa (SHA-1) or a DSA key's ssh-dss, and their certificates.
const LegacyUserKeySignaturesProperty = "ssh_allow_legacy_user_key_signatures"

// legacyUserKeySignatures are the user key signature algorithms the library
// calls insecure: ssh-rsa (SHA-1) and ssh-dss.
func legacyUserKeySignatures() []string { return ssh.InsecureAlgorithms().PublicKeyAuths }

// keyFormat returns the type of the key pub proves: a certificate's own key
// for a certificate, since a certificate is signed for with its key's
// algorithm.
func keyFormat(pub ssh.PublicKey) string {
	if cert, ok := pub.(*ssh.Certificate); ok {
		return cert.Key.Type()
	}
	return pub.Type()
}

// signatureAlgorithms returns the signature algorithms a key of format may
// sign a login with, strongest first: for an RSA key SHA-512, then SHA-256,
// then ssh-rsa, so a server that advertises SHA-2 always gets it (the library
// keeps a signer's order). ssh-rsa stays in the list even when the record does
// not allow it, so the server is asked whether it would take the key that way
// and the refusal can say whether the property would help.
func signatureAlgorithms(format string) []string {
	if format == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{format}
}

// keyNoun names a stored key in a message: "RSA key", "DSA key", or a
// certificate of either.
func keyNoun(pub ssh.PublicKey) string {
	kind := "key"
	switch keyFormat(pub) {
	case ssh.KeyAlgoRSA:
		kind = "RSA key"
	case ssh.InsecureKeyAlgoDSA:
		kind = "DSA key"
	}
	if _, ok := pub.(*ssh.Certificate); ok {
		return kind + " certificate"
	}
	return kind
}

// userKeyOutcome is what a stored key did during one login: the algorithm it
// signed with, or the legacy one it would not sign with and why. It is
// comparable, so a Conn holds it by value.
type userKeyOutcome struct {
	// key names the stored key ("RSA key"), set once it signed or refused.
	key string
	// signed is the algorithm the key signed the login with, empty when it
	// signed nothing.
	signed string
	// refused is the legacy algorithm the key would not sign with, empty when
	// it refused nothing.
	refused string
	// fips is set when the refusal was FIPS 140 mode's, which the record
	// cannot lift, rather than the record's own.
	fips bool
	// noRecord is set for a login no device record governs (HoldToSHA2),
	// where nothing can allow a legacy signature.
	noRecord bool
}

// legacySigned returns the legacy algorithm the key signed with, or empty when
// it signed with a secure one or not at all.
func (o userKeyOutcome) legacySigned() string {
	if slices.Contains(legacyUserKeySignatures(), o.signed) {
		return o.signed
	}
	return ""
}

// refusal returns the error a refused signature is, at addr, wrapping err:
// nil from the signer itself, or the login's own failure when another method
// was then tried and failed too.
func (o userKeyOutcome) refusal(addr string, err error) *LegacySignatureRefusedError {
	refused := &LegacySignatureRefusedError{Addr: addr, Key: o.key, Algorithm: o.refused, fips: o.fips, noRecord: o.noRecord, err: err}
	if !o.fips && !o.noRecord {
		refused.Property = LegacyUserKeySignaturesProperty
	}
	return refused
}

// userKeyUse collects one login's userKeyOutcome. dialChain starts a fresh one
// for each attempt, so what a Conn reports is the login that opened it.
type userKeyUse struct {
	mu  sync.Mutex
	got userKeyOutcome
}

// reset forgets what an earlier attempt recorded.
func (u *userKeyUse) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.got = userKeyOutcome{}
}

// record stores what the key did.
func (u *userKeyUse) record(o userKeyOutcome) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.got = o
}

// snapshot returns what has been recorded.
func (u *userKeyUse) snapshot() userKeyOutcome {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.got
}

// userKeySigner is a stored key as one connection offers it: SHA-2 first for
// an RSA key, and a legacy signature only when the device's record allows one
// and FIPS 140 mode is off. It is an ssh.MultiAlgorithmSigner, which the
// library uses as given rather than assuming every algorithm the key's type
// has.
type userKeySigner struct {
	key         ssh.AlgorithmSigner
	algorithms  []string
	allowLegacy bool
	addr        string
	use         *userKeyUse
	// noRecord marks a login no device record governs (HoldToSHA2).
	noRecord bool
}

// HoldToSHA2 returns signer as a login no device record governs offers it,
// for a connection this package does not make: a project's git server,
// reached through go-git. An RSA key signs with rsa-sha2-512 or rsa-sha2-256
// only, and ssh-rsa, or any signature from a DSA key, is refused with a
// LegacySignatureRefusedError naming no property, since no record can allow
// it. Other keys sign as they would. A signer that cannot choose its
// algorithm is refused, as keyAuth refuses one.
func HoldToSHA2(signer ssh.Signer) (ssh.Signer, error) {
	key, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("a %s key that cannot choose its signature algorithm", signer.PublicKey().Type())
	}
	held := newUserKeySigner(key, "", Legacy{}, nil)
	held.noRecord = true
	return held, nil
}

// newUserKeySigner wraps key for one connection to addr under legacy,
// recording what it signs or refuses in use (a fresh record when nil).
func newUserKeySigner(key ssh.AlgorithmSigner, addr string, legacy Legacy, use *userKeyUse) *userKeySigner {
	if use == nil {
		use = &userKeyUse{}
	}
	return &userKeySigner{
		key:         key,
		algorithms:  signatureAlgorithms(keyFormat(key.PublicKey())),
		allowLegacy: legacy.UserKeySignatures,
		addr:        addr,
		use:         use,
	}
}

// PublicKey returns the stored key's public half.
func (s *userKeySigner) PublicKey() ssh.PublicKey { return s.key.PublicKey() }

// Algorithms returns the signature algorithms this key offers, strongest
// first.
func (s *userKeySigner) Algorithms() []string { return slices.Clone(s.algorithms) }

// Sign signs with the key's own format, which for an RSA key is ssh-rsa and is
// held to the record as any other request for it is. The library never calls
// it for a login, since this is a MultiAlgorithmSigner.
func (s *userKeySigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return s.SignWithAlgorithm(rand, data, "")
}

// SignWithAlgorithm signs data with algorithm, the key's own format when
// empty. A legacy algorithm is refused with a LegacySignatureRefusedError
// unless the record allows it, and always under FIPS 140 mode, where the
// library would refuse it anyway with a message naming neither. What it signed
// or refused is recorded for the connection's warnings.
func (s *userKeySigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	if algorithm == "" {
		algorithm = keyFormat(s.key.PublicKey())
	}
	if !slices.Contains(s.algorithms, algorithm) {
		return nil, fmt.Errorf("remoteexec: this %s cannot sign with %s", keyNoun(s.key.PublicKey()), algorithm)
	}
	outcome := userKeyOutcome{key: keyNoun(s.key.PublicKey()), noRecord: s.noRecord}
	if slices.Contains(legacyUserKeySignatures(), algorithm) && (!s.allowLegacy || fips140.Enabled()) {
		// Under FIPS 140 mode the property would not help, so the refusal
		// does not name it, whether the record sets it or not.
		outcome.refused, outcome.fips = algorithm, fips140.Enabled()
		s.use.record(outcome)
		return nil, outcome.refusal(s.addr, nil)
	}
	sig, err := s.key.SignWithAlgorithm(rand, data, algorithm)
	if err != nil {
		return nil, err
	}
	outcome.signed = algorithm
	s.use.record(outcome)
	return sig, nil
}
