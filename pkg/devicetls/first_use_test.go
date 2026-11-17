// Tests for the pin onboarding records on first use, as a device's settings
// read it: trusted, and said to be unconfirmed on every use, only where the
// record names nothing it trusts of its own; never a key pin; and among the
// properties every device type dispatches and binds.
package devicetls_test

import (
	"encoding/pem"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestFirstUse_TrustedOnlyWhereTheRecordNamesNoTrustOfItsOwn: alone, a
// first-use pin reaches its certificate and refuses another, and every use
// says nobody confirmed it; beside a person's pin or a pinned authority it
// is not trusted at all, and is reported as unused.
func TestFirstUse_TrustedOnlyWhereTheRecordNamesNoTrustOfItsOwn(t *testing.T) {
	recorded := mint(t, newKey(t), 1, "fw.lab.invalid", time.Now().Add(300*24*time.Hour))
	other := mint(t, newKey(t), 2, "fw.lab.invalid", time.Now().Add(300*24*time.Hour))
	pin := devicetls.Fingerprint(recorded.cert)
	srv := serveMinted(t, recorded)

	alone, err := parse(map[string]inventory.PropertyValue{devicetls.FirstUseProperty: strings.ToUpper(pin)})
	if err != nil {
		t.Fatal(err)
	}
	if alone.FirstUsePin() != pin || alone.UnusedFirstUsePin() != "" || !slices.Equal(alone.Pins(), []string{pin}) {
		t.Errorf("alone: first use %q, unused %q, pins %q", alone.FirstUsePin(), alone.UnusedFirstUsePin(), alone.Pins())
	}
	if err := get(t, alone, srv); err != nil {
		t.Errorf("the first-use pin refused its own certificate: %v", err)
	}
	var untrusted *devicetls.UntrustedError
	if err := get(t, alone, serveMinted(t, other)); !errors.As(err, &untrusted) {
		t.Errorf("a changed certificate under a first-use pin: %v, want a refusal", err)
	}
	if w := alone.Warnings("fw"); len(w) != 1 || !strings.Contains(w[0], "pinned on first use") || !strings.Contains(w[0], "no person has confirmed") ||
		!strings.Contains(w[0], pin) || !strings.Contains(w[0], "confirm-tls-pin") {
		t.Errorf("a first-use pin's warning %q does not say it is unconfirmed and how to confirm it", w)
	}

	personPin := devicetls.Fingerprint(other.cert)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.der}))
	for own, props := range map[string]map[string]inventory.PropertyValue{
		devicetls.FingerprintProperty: {devicetls.FirstUseProperty: pin, devicetls.FingerprintProperty: personPin},
		devicetls.CAPEMProperty:       {devicetls.FirstUseProperty: pin, devicetls.CAPEMProperty: pemText},
	} {
		s, err := parse(props)
		if err != nil {
			t.Fatal(err)
		}
		if s.FirstUsePin() != "" || s.UnusedFirstUsePin() != pin || slices.Contains(s.Pins(), pin) {
			t.Errorf("beside %s: first use %q, unused %q, pins %q", own, s.FirstUsePin(), s.UnusedFirstUsePin(), s.Pins())
		}
		if err := get(t, s, srv); err == nil {
			t.Errorf("beside %s the first-use pin's certificate was still trusted", own)
		}
		w := s.Warnings("fw")
		if !slices.ContainsFunc(w, func(m string) bool { return strings.Contains(m, "that its own "+own+" overrides") }) {
			t.Errorf("beside %s the warnings %q do not say the first-use pin is unused", own, w)
		}
	}
}

// TestFirstUse_RefusesWhatOnboardingNeverWrites: onboarding writes one
// certificate pin, so a key pin, a list, text that is no pin and a value
// that is not text are refused, naming the property.
func TestFirstUse_RefusesWhatOnboardingNeverWrites(t *testing.T) {
	cert := mint(t, newKey(t), 1, "x.lab.invalid", time.Now().Add(time.Hour)).cert
	for name, v := range map[string]inventory.PropertyValue{
		"a key pin": devicetls.KeyFingerprint(cert),
		"a list":    []any{devicetls.Fingerprint(cert)},
		"two pins":  devicetls.Fingerprint(cert) + "," + devicetls.Fingerprint(cert),
		"no pin":    "trust me",
		"not text":  true,
	} {
		if _, err := parse(map[string]inventory.PropertyValue{devicetls.FirstUseProperty: v}); err == nil || !strings.Contains(err.Error(), devicetls.FirstUseProperty) {
			t.Errorf("%s: %v, want a refusal naming %s", name, err, devicetls.FirstUseProperty)
		}
	}
}

// TestFirstUse_IsTheReservedKeyAndTravelsWithTheTLSSettings: the property
// devicetls reads is the one pkg/inventory reserves for onboarding, so no
// write path but onboarding's reaches it, and it is among Properties, so
// every device type dispatches it to a Runner and binds its discovery to it.
func TestFirstUse_IsTheReservedKeyAndTravelsWithTheTLSSettings(t *testing.T) {
	if devicetls.FirstUseProperty != inventory.TLSFirstUseProperty {
		t.Fatalf("devicetls reads %q and pkg/inventory reserves %q", devicetls.FirstUseProperty, inventory.TLSFirstUseProperty)
	}
	// The keys whose write drops a first-use pin (record.Base) are the ones
	// devicetls reads a person's trust from.
	if devicetls.FingerprintProperty != inventory.TLSPinProperty || devicetls.CAPEMProperty != inventory.TLSAuthorityProperty {
		t.Fatalf("devicetls reads %q and %q, pkg/inventory names %q and %q", devicetls.FingerprintProperty, devicetls.CAPEMProperty, inventory.TLSPinProperty, inventory.TLSAuthorityProperty)
	}
	if !inventory.IsReservedProperty(devicetls.FirstUseProperty) {
		t.Error("the first-use pin is not reserved, so a person or a sync could write one")
	}
	if !slices.Contains(devicetls.Properties(), devicetls.FirstUseProperty) {
		t.Error("Properties() leaves out the first-use pin, so a Runner would refuse what the Controller trusted")
	}
}
