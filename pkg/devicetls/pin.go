// Package devicetls: trusting one pinned certificate, and saying which
// certificate a device presented when none of its settings trusts it, so a
// person can check it out of band before pinning it.
package devicetls

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
)

// Fingerprint is a certificate's SHA-256 over its DER bytes, written
// "sha256:<hex>", the form tls_fingerprint pins.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// UntrustedError is a device certificate that neither the system's
// authorities, a pinned authority nor a pinned certificate vouches for. It
// carries what a person needs to check the certificate with the device's
// owner before pinning it.
type UntrustedError struct {
	// Fingerprint is the presented certificate's, in the form a pin takes.
	Fingerprint string
	// Subject and Issuer are the certificate's names, as text.
	Subject, Issuer string
	// NotAfter is the last day it is valid, YYYY-MM-DD in UTC.
	NotAfter string
	// Cause is why it was not trusted.
	Cause error
}

// Error says the certificate is untrusted and how to pin it once checked.
func (e *UntrustedError) Error() string {
	return fmt.Sprintf("the device's certificate is not trusted (%v): subject %q, issued by %q, valid until %s, fingerprint %s; once you have checked that fingerprint with the device's owner, pin it as the device's %s property (--tls-fingerprint on an import command)",
		e.Cause, e.Subject, e.Issuer, e.NotAfter, e.Fingerprint, FingerprintProperty)
}

// Unwrap returns why the certificate was not trusted.
func (e *UntrustedError) Unwrap() error { return e.Cause }

// untrusted describes leaf as an UntrustedError for cause.
func untrusted(leaf *x509.Certificate, cause error) *UntrustedError {
	return &UntrustedError{
		Fingerprint: Fingerprint(leaf), Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(),
		NotAfter: leaf.NotAfter.UTC().Format("2006-01-02"), Cause: cause,
	}
}

// verifyPin returns the check a pinned connection runs on every handshake:
// the device's own certificate must be the pinned one, whatever its chain,
// dates or names, since the pin is the identity.
func verifyPin(want string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the device sent no certificate")
		}
		leaf := cs.PeerCertificates[0]
		if Fingerprint(leaf) == want {
			return nil
		}
		return untrusted(leaf, fmt.Errorf("its fingerprint is not the pinned %s", want))
	}
}

// Explain returns err as an *UntrustedError when it is Go's refusal of the
// certificate a device presented (an unknown authority, a certificate
// outside its dates or for another name), so the message names the
// certificate's fingerprint; any other error is returned as it is.
func Explain(err error) error {
	var (
		unknown  x509.UnknownAuthorityError
		invalid  x509.CertificateInvalidError
		hostname x509.HostnameError
		verify   *tls.CertificateVerificationError
	)
	switch {
	case errors.As(err, &unknown) && unknown.Cert != nil:
		return untrusted(unknown.Cert, err)
	case errors.As(err, &invalid) && invalid.Cert != nil:
		return untrusted(invalid.Cert, err)
	case errors.As(err, &hostname) && hostname.Certificate != nil:
		return untrusted(hostname.Certificate, err)
	case errors.As(err, &verify) && len(verify.UnverifiedCertificates) > 0:
		return untrusted(verify.UnverifiedCertificates[0], err)
	}
	return err
}
