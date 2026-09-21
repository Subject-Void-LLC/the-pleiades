// Package pfx unlocks a PKCS#12 (.pfx/.p12) bundle in memory and hands
// back the PEM pair a TLS client presents.
//
// # Why this exists and why it is the last thing built rather than the first
//
// PLAN.md Section 17.4 asks that an operator store a PFX bundle, link a
// password credential to it, and have the Runner "use the password
// Just-In-Time to unlock the private key purely in-memory". Read literally
// that makes a decoder the entry point. Built in that order it produces a
// parser with nothing downstream of it, so the certificate-presenting path
// was built first taking PEM, and this package is an input adapter into a
// path that already works.
//
// # In memory, and never through a subprocess
//
// Everything here is Go. Section 17.4 is explicit that unlocking must not
// shell out to OpenSSL, because a password reaching a command line is a
// password in a process listing, and this package could not do so even if
// it wanted to: it has no exec path and no filesystem path. The bundle
// arrives as a string, the pair leaves as bytes, and nothing touches disk.
//
// # Where this runs, which is the part that matters
//
// It runs in the Runner's per-task child process (PLAN.md Section 17.5),
// because that is where a Collection method reads its secrets. So the
// sealed bundle and its passphrase are what cross the message broker, and
// the unlocked private key exists only inside the one short-lived process
// that presents it. Decoding on the Controller instead would have been
// simpler and would have put an unlocked private key on a JetStream stream
// for the whole of its retention window, which is the exposure Phase 78
// exists to shrink rather than to widen.
//
// # The library, and the one already in the module that was not used
//
// software.sslmate.com/src/go-pkcs12 is three-clause BSD, forked from
// golang.org/x/crypto/pkcs12 and carrying both the Opsmate and the Go
// Authors copyright, so it satisfies this repository's GPLv3 compatibility
// rule.
//
// golang.org/x/crypto/pkcs12 is already in this module's dependency graph
// and would have cost nothing to adopt, and it was rejected on evidence
// rather than preference. Three reasons, in order of how much they matter
// here:
//
//   - IT CANNOT RETURN A CHAIN. It exports Decode and ToPEM and nothing
//     else: Decode hands back exactly one certificate. Decode also refuses
//     an authenticated safe holding anything but exactly two items, which
//     is what a bundle carrying an issuer looks like. This package emits
//     the leaf followed by its intermediates precisely so a client can
//     present a path to a server that needs one, and that is not
//     expressible with the older API at all.
//   - It implements two password-based encryption schemes, 3DES and 40-bit
//     RC2, so a bundle sealed with AES fails outright.
//   - Its own package documentation declares it frozen and names this fork
//     as the alternative.
//
// One correction worth keeping, because the first draft of this comment got
// it wrong and a real bundle disproved it. It claimed Windows defaults
// Export-PfxCertificate to AES-256, so the older library could not open
// what operators produce. Measured against a real Windows 11 export on
// 2026-09-20, the default is pbeWithSHA1And3-KeyTripleDES-CBC, which the
// older library supports. AES-256 is what you get from
// -CryptoAlgorithmOption AES256_SHA256 and from much other tooling, not
// what you get by default. The dependency is still the right choice, for
// the first reason above rather than the second.
//
// # One caveat that belongs in the documentation rather than in the field
//
// This decoder reads DER and not BER. Bundles written by older Windows
// tooling are not reliably DER, so a bundle that other software opens can
// still be refused here. docs/10-running-in-production.md says so, with the
// re-export that fixes it.
package pfx

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"

	"software.sslmate.com/src/go-pkcs12"
)

// ErrBundle reports a bundle that could not be opened: not valid base64,
// not a PKCS#12 structure, the wrong passphrase, or a structure this
// decoder does not implement.
//
// It is one sentinel rather than several on purpose. The distinctions a
// caller could act on are in the message, and the distinctions it could
// not are exactly the ones worth refusing to broadcast: telling an
// unauthenticated caller apart "wrong password" from "malformed bundle"
// turns a decode into an oracle.
var ErrBundle = errors.New("pfx: bundle could not be opened")

// MaxBundleBytes bounds the decoded size of a bundle.
//
// A certificate, its key and a realistic chain are a few kilobytes. One
// mebibyte is far above any legitimate bundle and far below anything that
// costs real time to parse, which is the same reasoning and the same figure
// internal/credtype/lookup/hashivault uses to bound a Vault response. The
// bound exists because this input arrives from a credential row an operator
// pasted into, and ASN.1 parsing of attacker-influenced bytes is the shape
// this repository has been bitten by before.
const MaxBundleBytes = 1 << 20

// Decode unlocks a base64-encoded PKCS#12 bundle and returns the
// certificate chain and private key as PEM.
//
// The certificate PEM carries the leaf first and any intermediates the
// bundle contained after it, which is the order crypto/tls expects and what
// lets a client present a chain rather than a bare leaf. A server that
// needs an intermediate to build a path to its trusted root will otherwise
// reject a certificate that is perfectly valid.
//
// An empty passphrase is passed through as an empty passphrase rather than
// refused. A bundle with no password is unusual and is not this package's
// business to forbid: the credential type decides whether a passphrase is
// required, and refusing here would make a legal bundle unusable for a
// reason nothing documents.
func Decode(bundleBase64, passphrase string) (certPEM, keyPEM []byte, err error) {
	if bundleBase64 == "" {
		return nil, nil, fmt.Errorf("%w: it is empty", ErrBundle)
	}

	// Bound the ENCODED length before decoding, so an oversized input is
	// refused without first allocating its decoded form. Base64 is four
	// characters per three bytes, so this is the encoded ceiling matching
	// MaxBundleBytes rather than a second, different limit.
	if maxEncoded := base64.StdEncoding.EncodedLen(MaxBundleBytes); len(bundleBase64) > maxEncoded {
		return nil, nil, fmt.Errorf("%w: it is larger than the %d byte limit", ErrBundle, MaxBundleBytes)
	}

	der, err := base64.StdEncoding.DecodeString(bundleBase64)
	if err != nil {
		// The decoder's own error quotes the offending byte, which is a
		// byte of a secret, so it is deliberately not wrapped.
		return nil, nil, fmt.Errorf("%w: it is not valid base64", ErrBundle)
	}
	// The DER holds the encrypted bundle, and it has no further use once the
	// pair below has been built.
	defer zero(der)

	key, leaf, chain, err := pkcs12.DecodeChain(der, passphrase)
	if err != nil {
		// Not wrapped, for the reason ErrBundle documents: the library
		// distinguishes a bad MAC from a malformed structure, and passing
		// that distinction on turns this into a passphrase oracle.
		return nil, nil, fmt.Errorf(
			"%w: check the passphrase, and note that this reads DER and not BER, so a bundle from older Windows "+
				"tooling may need re-exporting", ErrBundle)
	}
	if leaf == nil {
		return nil, nil, fmt.Errorf("%w: it holds a private key but no certificate", ErrBundle)
	}

	// PKCS#8 rather than a per-algorithm encoding, so one branch covers RSA,
	// ECDSA and Ed25519 and a key type added to crypto later needs no change
	// here. crypto/tls reads it through the same ParsePKCS8PrivateKey.
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: its private key is of a kind this platform cannot use: %v", ErrBundle, err)
	}
	defer zero(keyDER)

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	for _, ca := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
	}
	return certPEM, keyPEM, nil
}

// zero overwrites b.
//
// Section 17.4 asks for the memory holding a secret to be cleared once it
// has served its purpose, and this is that, applied to the two
// intermediates this package creates: the decrypted DER and the marshaled
// private key.
//
// What it is worth is worth stating exactly, because overstating it would
// be worse than not doing it. Go's garbage collector may copy a value while
// it lives, so this clears the copy this code holds and cannot promise
// there was only ever one. It is a real reduction in how long key material
// sits in a reusable heap page, not a guarantee that it is gone from the
// process. The returned PEM is deliberately NOT zeroed: the caller owns it
// and is still using it.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
