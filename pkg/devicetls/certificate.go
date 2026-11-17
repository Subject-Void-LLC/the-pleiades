// Package devicetls: what a device's presented certificate says, as
// onboarding and the facts gatherers record it, and the warnings it calls
// for: a certificate near or past its last day, and a pin a rotation has
// left behind.
package devicetls

import (
	"crypto/x509"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ExpiryWindow is how long before a certificate's last day it is warned
// about: long enough to renew it, and pin the new one beside the old, before
// a scheduled read or a run meets the change.
const ExpiryWindow = 30 * 24 * time.Hour

// maxNameText bounds a certificate's subject or issuer as a record keeps it.
// A device writes its own names, and a name longer than this is cut rather
// than trusted to be brief.
const maxNameText = 512

// Certificate is what a presented certificate says, in the form onboarding
// and the facts gatherers record it: both of a pin's forms, its names and
// its last moment of validity.
type Certificate struct {
	// Fingerprint is the certificate's SHA-256, as a certificate pin.
	Fingerprint string
	// KeyFingerprint is its public key's SHA-256, as a key pin.
	KeyFingerprint string
	// Subject and Issuer are its names, as text with no control or format
	// character, at most maxNameText bytes.
	Subject, Issuer string
	// NotAfter is the last moment it is valid, in UTC.
	NotAfter time.Time
}

// Describe returns what cert says.
func Describe(cert *x509.Certificate) Certificate {
	return Certificate{
		Fingerprint:    Fingerprint(cert),
		KeyFingerprint: KeyFingerprint(cert),
		Subject:        nameText(cert.Subject.String()),
		Issuer:         nameText(cert.Issuer.String()),
		NotAfter:       cert.NotAfter.UTC(),
	}
}

// NotAfterText is NotAfter as a record writes it: RFC 3339 in UTC, which
// orders as text and reads the same in every store.
func (c Certificate) NotAfterText() string { return c.NotAfter.Format(time.RFC3339) }

// nameText makes a certificate name safe to keep: no control or format
// character (a terminal escape, a direction override), at most maxNameText
// bytes, cut on a character boundary.
func nameText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(s, "") {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			continue
		}
		if b.Len()+len(string(r)) > maxNameText {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// matching returns the pin cert matches, and whether one does.
func (s Settings) matching(cert *x509.Certificate) (pin, bool) {
	for _, p := range s.pins {
		if p.matches(cert) {
			return p, true
		}
	}
	return pin{}, false
}

// ExpiryWarning says, for the named device, that cert (the certificate it
// presented, which s trusted) has expired or will within ExpiryWindow of now,
// and what its replacement means for how s trusts it; "" when it is further
// off. A pin ignores dates, so an expired certificate is reached anyway, and
// a certificate pin refuses the renewed one until it is pinned too. An
// expired certificate nothing pins is never one s trusted (Go's own checks
// refuse it), so it draws no warning here.
func (s Settings) ExpiryWarning(device string, cert *x509.Certificate, now time.Time) string {
	left := cert.NotAfter.Sub(now)
	if left > ExpiryWindow {
		return ""
	}
	day := cert.NotAfter.UTC().Format("2006-01-02")
	matched, pinned := s.matching(cert)
	if left <= 0 {
		if !pinned {
			return ""
		}
		return fmt.Sprintf("device %q presents a certificate that expired on %s, and its pin accepts it anyway, since a pin ignores dates: renew it, and pin the new certificate before it replaces this one", device, day)
	}
	when := fmt.Sprintf("device %q's certificate expires on %s, in %d days", device, day, int(left.Hours()/24))
	switch {
	case pinned && matched.key:
		return when + ": its key pin accepts a renewed certificate for the same key, and refuses one for a new key until that key is pinned too"
	case pinned:
		return when + fmt.Sprintf(": its certificate pin refuses the renewed one until it is pinned too, so add the new pin to %s beside this one before the change, or pin the key (%s) if the device keeps it", FingerprintProperty, KeyFingerprint(cert))
	}
	return when + ": renew it before then, or connections to the device are refused"
}

// RetireWarnings says, for each of s's pins that cert (the certificate the
// device presented) does not match, that nothing presented it, when s holds
// more than one pin: a pin left after its rotation is done still trusts the
// old certificate or key for as long as it is listed. It is empty when s
// holds one pin or none.
func (s Settings) RetireWarnings(device string, cert *x509.Certificate) []string {
	if len(s.pins) < 2 {
		return nil
	}
	var out []string
	for _, p := range s.pins {
		if !p.matches(cert) {
			out = append(out, fmt.Sprintf("device %q presented a certificate that pin %s does not match: if no rotation or other member still presents it, remove it from %s, since a pin kept trusts its certificate or key for as long as it is listed",
				device, p, FingerprintProperty))
		}
	}
	return out
}
