// Package devicetls: the pins a device's settings trust, said to a person on
// every use, and the one pin onboarding may record on first use.
//
// A first-use pin is what onboarding wrote, on an explicit opt-in, as the
// certificate a device first presented, the way SSH's accept-new records a
// host key: it is kept apart from tls_fingerprint, in a property only
// onboarding writes (tls_first_use_fingerprint, reserved like the discovery
// it is recorded beside), so it cannot be forged by any other write path and
// is never mistaken for a pin a person checked. It is trusted only where the
// record names nothing it trusts of its own, every use says it is unconfirmed,
// and confirming it is a person moving it into tls_fingerprint. A record that
// loses it (a deleted state file, a hosts.yaml copied to another project)
// loses the trust with it, and the device is refused rather than trusted as if
// a person had checked it.
package devicetls

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// withFirstUse reads the first-use pin v into s: trusted when the record
// names nothing it trusts of its own (no tls_fingerprint, no tls_ca_pem),
// and otherwise kept only to be reported as unused. It must be the one
// certificate pin onboarding writes; anything else is refused, since nothing
// else writes the property.
func (s *Settings) withFirstUse(v inventory.PropertyValue) error {
	text, _ := v.(string)
	p, err := parsePin(text)
	if err != nil || p.key {
		return fmt.Errorf("property %s must be the one certificate fingerprint onboarding recorded (sha256: and 64 hex digits)", FirstUseProperty)
	}
	if len(s.pins) > 0 || s.roots != nil {
		s.unusedFirstUse = p.String()
		return nil
	}
	s.pins = []pin{p}
	s.firstUse = true
	return nil
}

// Pins returns the pins the settings trust, each in the form tls_fingerprint
// takes it, the first-use pin included when it is the one trusted; nil when
// the settings trust a chain.
func (s Settings) Pins() []string {
	if len(s.pins) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.pins))
	for _, p := range s.pins {
		out = append(out, p.String())
	}
	return out
}

// FirstUsePin returns the pin onboarding recorded on first use when it is
// what the settings trust, unconfirmed; "" otherwise.
func (s Settings) FirstUsePin() string {
	if !s.firstUse {
		return ""
	}
	return s.pins[0].String()
}

// UnusedFirstUsePin returns a first-use pin the record's own trust
// overrides, which onboarding removes; "" when there is none.
func (s Settings) UnusedFirstUsePin() string { return s.unusedFirstUse }

// pinWarnings says, for the named device, what each pin trusts: a person's
// one certificate or key, a list of them, or a first-use pin nobody has
// confirmed, and a first-use pin the record no longer uses.
func (s Settings) pinWarnings(device string) []string {
	var out []string
	switch {
	case s.firstUse:
		out = append(out, fmt.Sprintf("device %q trusts a certificate pinned on first use (%s %s) that no person has confirmed: onboarding took the certificate that answered, "+
			"so check that fingerprint with the device's owner, then confirm it (pleiades confirm-tls-pin, or the Controller's tls-pin confirm route), which moves it to %s",
			device, FirstUseProperty, s.pins[0], FingerprintProperty))
	case len(s.pins) == 1 && s.pins[0].key:
		out = append(out, fmt.Sprintf("device %q trusts any certificate for one pinned public key (%s %s), whatever its issuer, dates or names, so a certificate the device issues again for the same key is accepted unseen: "+
			"pin the new key if the key changes, and check it with the device's owner first",
			device, FingerprintProperty, s.pins[0]))
	case len(s.pins) == 1:
		out = append(out, fmt.Sprintf("device %q trusts one pinned certificate (%s %s) whatever its issuer, dates or names: pin the new one when the certificate is renewed, and check it with the device's owner first",
			device, FingerprintProperty, s.pins[0]))
	case len(s.pins) > 1:
		out = append(out, fmt.Sprintf("device %q trusts %d pins (%s: %s), any one of them, whatever each certificate's issuer, dates or names: "+
			"remove each pin once nothing presents it, such as the old one when a rotation is done",
			device, len(s.pins), FingerprintProperty, joinPins(s.pins)))
	}
	if s.unusedFirstUse != "" {
		own := FingerprintProperty
		if s.roots != nil {
			own = CAPEMProperty
		}
		out = append(out, fmt.Sprintf("device %q holds a pin recorded on first use (%s %s) that its own %s overrides, so it is not trusted; onboarding the device again removes it",
			device, FirstUseProperty, s.unusedFirstUse, own))
	}
	return out
}
