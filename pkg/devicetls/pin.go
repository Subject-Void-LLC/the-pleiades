// Package devicetls: trusting pinned certificates or public keys, and saying
// which certificate a device presented when none of its settings trusts it,
// so a person can check it out of band before pinning it.
//
// A pin takes one of two forms. A certificate pin ("sha256:") is the SHA-256
// of the certificate's DER bytes and accepts that one certificate. A key pin
// ("spki-sha256:") is the SHA-256 of the certificate's SubjectPublicKeyInfo
// and accepts any certificate for that key, whatever its issuer, dates or
// names, so it survives a device re-issuing its self-signed certificate with
// the same key (an upgrade, a new host name, a yearly rollover). The
// certificate pin stays the default the documentation shows, since it is the
// narrower trust.
package devicetls

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The two forms a pin takes in tls_fingerprint.
const (
	// CertificatePinPrefix leads a certificate pin, the SHA-256 of the
	// certificate's DER bytes, which accepts that one certificate.
	CertificatePinPrefix = "sha256:"
	// KeyPinPrefix leads a key pin, the SHA-256 of the certificate's
	// SubjectPublicKeyInfo, which accepts any certificate for that key.
	KeyPinPrefix = "spki-sha256:"
)

// MaxPins is the most pins tls_fingerprint may hold: room for a planned
// rotation of both members of an HA pair in both forms, and few enough that
// the person keeping the list can still read it.
const MaxPins = 8

// Fingerprint is a certificate's SHA-256 over its DER bytes, written
// "sha256:<hex>", the form a certificate pin takes.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return CertificatePinPrefix + hex.EncodeToString(sum[:])
}

// KeyFingerprint is the SHA-256 of a certificate's SubjectPublicKeyInfo,
// written "spki-sha256:<hex>", the form a key pin takes.
func KeyFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return KeyPinPrefix + hex.EncodeToString(sum[:])
}

// pin is one pinned certificate or public key.
type pin struct {
	// key is set for a key pin, clear for a certificate pin.
	key bool
	// sum is the SHA-256, in lowercase hex.
	sum string
}

// String writes p in the form tls_fingerprint takes it.
func (p pin) String() string {
	if p.key {
		return KeyPinPrefix + p.sum
	}
	return CertificatePinPrefix + p.sum
}

// matches reports whether cert is the pinned certificate, or a certificate
// for the pinned key.
func (p pin) matches(cert *x509.Certificate) bool {
	if p.key {
		return KeyFingerprint(cert) == p.String()
	}
	return Fingerprint(cert) == p.String()
}

// errPinShape is why text is not a pin.
var errPinShape = errors.New(`a pin is "sha256:" and the 64 hex digits of a certificate's SHA-256, or "spki-sha256:" and those of its public key's`)

// parsePin reads one pin, either form, in either case.
func parsePin(text string) (pin, error) {
	t := strings.ToLower(strings.TrimSpace(text))
	p := pin{}
	// The key form is tried first: its prefix ends in the certificate's.
	hexPart, ok := strings.CutPrefix(t, KeyPinPrefix)
	if ok {
		p.key = true
	} else {
		hexPart, ok = strings.CutPrefix(t, CertificatePinPrefix)
	}
	if b, err := hex.DecodeString(hexPart); !ok || err != nil || len(b) != sha256.Size {
		return pin{}, errPinShape
	}
	p.sum = hexPart
	return p, nil
}

// NormalizePin returns text, one pin in either form and either case, in the
// form tls_fingerprint holds it, or why it is not a pin.
func NormalizePin(text string) (string, error) {
	p, err := parsePin(text)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// parsePins reads tls_fingerprint: one pin, a list of them, or one text
// holding several separated by commas (the form a command line's
// key=value carries). Any one of them matching is trusted, so a rotation
// can hold the old and the new together. An empty list, a list longer than
// MaxPins and a pin written twice are refused: each is a mistake that
// would leave the list trusting other than what its author meant.
func parsePins(v inventory.PropertyValue) ([]pin, error) {
	var texts []string
	switch x := v.(type) {
	case string:
		texts = strings.Split(x, ",")
	case []string:
		texts = x
	case []any:
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("each pin in the list must be text")
			}
			texts = append(texts, s)
		}
	default:
		return nil, errors.New("it must be a pin or a list of pins")
	}
	switch {
	case len(texts) == 0:
		return nil, errors.New("it lists no pin")
	case len(texts) > MaxPins:
		return nil, fmt.Errorf("it lists %d pins, more than the %d it may hold: remove each pin nothing presents any more", len(texts), MaxPins)
	}
	pins := make([]pin, 0, len(texts))
	for _, text := range texts {
		p, err := parsePin(text)
		if err != nil {
			return nil, err
		}
		for _, seen := range pins {
			if seen == p {
				return nil, fmt.Errorf("it lists %s twice", p)
			}
		}
		pins = append(pins, p)
	}
	return pins, nil
}

// UntrustedError is a device certificate that neither the system's
// authorities, a pinned authority nor a pin vouches for. It carries what a
// person needs to check the certificate with the device's owner before
// pinning it, in both of the forms a pin takes.
type UntrustedError struct {
	// Fingerprint is the presented certificate's, in the form a
	// certificate pin takes.
	Fingerprint string
	// KeyFingerprint is its public key's, in the form a key pin takes.
	KeyFingerprint string
	// Subject and Issuer are the certificate's names, as text.
	Subject, Issuer string
	// NotAfter is the last day it is valid, YYYY-MM-DD in UTC.
	NotAfter string
	// Cause is why it was not trusted.
	Cause error
}

// Error says the certificate is untrusted and how to pin it once checked.
func (e *UntrustedError) Error() string {
	return fmt.Sprintf("the device's certificate is not trusted (%v): subject %q, issued by %q, valid until %s, fingerprint %s, key fingerprint %s; "+
		"once you have checked that fingerprint with the device's owner, pin it as the device's %s property (--tls-fingerprint on an import command), "+
		"or pin the key fingerprint instead to accept any certificate for the same key, whatever its dates or names",
		e.Cause, e.Subject, e.Issuer, e.NotAfter, e.Fingerprint, e.KeyFingerprint, FingerprintProperty)
}

// Unwrap returns why the certificate was not trusted.
func (e *UntrustedError) Unwrap() error { return e.Cause }

// Refused reports that this side refused the device's certificate: asking
// again presents the same certificate and gets the same refusal, so the
// engine's Refused stops a task's until and its loop's next item from
// trying again, as it does for an SSH host key known_hosts refuses. No
// credential was sent: the handshake ended before any request.
func (e *UntrustedError) Refused() bool { return true }

// untrusted describes leaf as an UntrustedError for cause.
func untrusted(leaf *x509.Certificate, cause error) *UntrustedError {
	return &UntrustedError{
		Fingerprint: Fingerprint(leaf), KeyFingerprint: KeyFingerprint(leaf),
		Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(),
		NotAfter: leaf.NotAfter.UTC().Format("2006-01-02"), Cause: cause,
	}
}

// verifyPin returns the check a pinned connection runs on every handshake:
// the device's own certificate must be one of the pinned ones, or one for a
// pinned key, whatever its chain, dates or names, since the pin is the
// identity.
func verifyPin(pins []pin) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the device sent no certificate")
		}
		leaf := cs.PeerCertificates[0]
		for _, p := range pins {
			if p.matches(leaf) {
				return nil
			}
		}
		return untrusted(leaf, mismatch(pins))
	}
}

// mismatch says which pins a certificate failed to match.
func mismatch(pins []pin) error {
	if len(pins) == 1 && pins[0].key {
		return fmt.Errorf("its key fingerprint is not the pinned %s", pins[0])
	}
	if len(pins) == 1 {
		return fmt.Errorf("its fingerprint is not the pinned %s", pins[0])
	}
	return fmt.Errorf("it matches none of the %d pinned: %s", len(pins), joinPins(pins))
}

// joinPins writes pins as a list a person reads.
func joinPins(pins []pin) string {
	texts := make([]string, 0, len(pins))
	for _, p := range pins {
		texts = append(texts, p.String())
	}
	return strings.Join(texts, ", ")
}

// Explain returns err as an *UntrustedError when it is Go's refusal of the
// certificate a device presented (an unknown authority, a certificate
// outside its dates or for another name), so the message names the
// certificate's fingerprints; any other error is returned as it is.
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
