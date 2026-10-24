// Package cml: reaching a controller whose certificate no public CA vouches
// for, by pinning the exact certificate or a CA, never by trusting anything.
package cml

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Fingerprint is a certificate's SHA-256 over its DER bytes, written
// "sha256:<hex>", the form TLSConfig pins.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// UntrustedError is a controller certificate that neither the system's
// CAs, a pinned CA nor a pinned fingerprint vouches for. It carries what a
// person needs to check the certificate out of band before pinning it.
type UntrustedError struct {
	Fingerprint string
	Subject     string
	Issuer      string
	NotAfter    string
	Cause       error
}

// Error says the certificate is untrusted and how to pin it once checked.
func (e *UntrustedError) Error() string {
	return fmt.Sprintf("the controller's certificate is not trusted (%v): subject %q, issued by %q, valid until %s, fingerprint %s; once you have checked that fingerprint with the controller's owner, pass it as --tls-fingerprint",
		e.Cause, e.Subject, e.Issuer, e.NotAfter, e.Fingerprint)
}

// TLSConfig returns the TLS configuration for a controller: with
// fingerprint ("sha256:<hex>"), the one certificate whose fingerprint that
// is, whatever its chain or dates, since the pin is the identity; with
// roots, a chain to those CAs, dates included; with neither, the system's
// CAs, and the certificate must name host (an address or a name) either
// way but the pin. A certificate none of those accepts fails with
// *UntrustedError.
func TLSConfig(host, fingerprint string, roots *x509.CertPool) (*tls.Config, error) {
	want := ""
	if fingerprint != "" {
		hexPart, ok := strings.CutPrefix(strings.ToLower(fingerprint), "sha256:")
		if b, err := hex.DecodeString(hexPart); !ok || err != nil || len(b) != sha256.Size {
			return nil, errors.New(`a TLS fingerprint is "sha256:" and 64 hex digits`)
		}
		want = "sha256:" + hexPart
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification is done below, by the rule the caller chose; Go's
		// own is replaced, never skipped.
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection below verifies every handshake
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the controller sent no certificate")
			}
			leaf := cs.PeerCertificates[0]
			var cause error
			if want != "" {
				if Fingerprint(leaf) == want {
					return nil
				}
				cause = errors.New("its fingerprint is not the pinned one")
			} else {
				inter := x509.NewCertPool()
				for _, c := range cs.PeerCertificates[1:] {
					inter.AddCert(c)
				}
				// Verify skips the name when DNSName is empty, which it is
				// for a controller reached by address, so the name is
				// checked on its own.
				if _, cause = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter}); cause == nil {
					if cause = leaf.VerifyHostname(host); cause == nil {
						return nil
					}
				}
			}
			return &UntrustedError{
				Fingerprint: Fingerprint(leaf), Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(),
				NotAfter: leaf.NotAfter.UTC().Format("2006-01-02"), Cause: cause,
			}
		},
	}, nil
}
