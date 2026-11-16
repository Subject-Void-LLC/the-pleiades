// What onboarding records of the certificate a TLS device presented to its
// probe, read from the probe's own verified handshakes
// (devicetls.WithPresented), so a changed certificate is a changed discovery
// and one near its last day is said before a pin refuses its replacement.
package onboard

import (
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// The discovery facts that describe the certificate a probe saw.
const (
	factCertificateFingerprint    = "tls_certificate_fingerprint"
	factCertificateKeyFingerprint = "tls_certificate_key_fingerprint"
	factCertificateSubject        = "tls_certificate_subject"
	factCertificateIssuer         = "tls_certificate_issuer"
	factCertificateNotAfter       = "tls_certificate_not_after"
)

// certificateFacts adds to facts what the certificate seen presented says,
// when the probe's verified handshakes saw exactly one, and returns the
// warnings it calls for under settings (the device's TLS as the probe used
// it): one past or near its last day, and a pin of a list nothing presented.
// A probe that saw more than one is recorded with none, since an address
// answered by more than one server would make the facts flap between them.
func certificateFacts(device string, settings devicetls.Settings, seen *devicetls.Presented, now time.Time, facts map[string]any) []string {
	certs, more := seen.Certificates()
	switch {
	case len(certs) == 0:
		return nil
	case len(certs) > 1 || more:
		return []string{fmt.Sprintf("device %q presented more than one certificate to the probe (%d seen), so none is recorded: more than one server answers at its address", device, len(certs))}
	}
	c := devicetls.Describe(certs[0])
	facts[factCertificateFingerprint] = c.Fingerprint
	facts[factCertificateKeyFingerprint] = c.KeyFingerprint
	facts[factCertificateSubject] = factText(c.Subject)
	facts[factCertificateIssuer] = factText(c.Issuer)
	facts[factCertificateNotAfter] = c.NotAfterText()
	var warnings []string
	if w := settings.ExpiryWarning(device, certs[0], now); w != "" {
		warnings = append(warnings, w)
	}
	return append(warnings, settings.RetireWarnings(device, certs[0])...)
}
