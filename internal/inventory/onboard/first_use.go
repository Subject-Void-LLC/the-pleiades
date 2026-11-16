// Recording, on an explicit opt-in, the certificate a device first presents
// as the pin it is trusted by, the way SSH's accept-new records a host key.
//
// It is off unless one onboarding asks for it (WithFirstUsePins: pleiades
// onboard --tls-first-use, or the Controller's onboard route with
// tls_first_use), and it applies only there: no run writes a pin. It records
// only for a device whose record names nothing it trusts (no tls_fingerprint,
// no tls_ca_pem, no pin recorded on first use before), so it never replaces a
// pin and a changed certificate is still refused. The pin goes into
// tls_first_use_fingerprint, never tls_fingerprint, and every use of it says
// nobody confirmed it until a person moves it there. It is recorded only with
// the onboarding that proved the device through it: a probe that then fails
// records nothing.
//
// What it costs is what accept-new costs: a machine in the middle at the one
// onboarding that records the pin would be the device that was pinned, and
// would be sent the device's credential. The pin's review (the onboarding's
// result lists it, with both of its forms) is what finds that out.
package onboard

import (
	"errors"
	"fmt"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// WithFirstUsePins makes one onboarding record the certificate a device
// presents as its pin (tls_first_use_fingerprint) when nothing in its record
// trusts it, instead of refusing. Only an onboarding a person asked for this
// way passes it.
func WithFirstUsePins() Option {
	return func(o *options) { o.firstUse = true }
}

// FirstUsePin is a pin one onboarding recorded on first use, as a person
// reviews it before confirming it: both of the forms a pin takes, the
// certificate's names and last day, and why nothing trusted it before.
type FirstUsePin struct {
	// Fingerprint is the certificate pin recorded.
	Fingerprint string `json:"fingerprint"`
	// KeyFingerprint is the certificate's key, as a key pin would hold it.
	KeyFingerprint string `json:"key_fingerprint"`
	// Subject and Issuer are the certificate's names.
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"`
	// NotAfter is its last day, YYYY-MM-DD in UTC.
	NotAfter string `json:"not_after"`
	// Untrusted is why the certificate was not trusted before it was
	// pinned: an unknown authority, its dates, or another name.
	Untrusted string `json:"untrusted"`
}

// firstUseWriter is the writers of a first-use pin every item the factory
// builds has, through record.Base; asserted structurally, as the other
// mutators are, so pkg/inventory's item contract gains no setters.
type firstUseWriter interface {
	RecordFirstUsePin(pin string)
	RemoveFirstUsePin() bool
}

// probeFunc runs the device's probe against item.
type probeFunc func(item inventory.InventoryItem) (Probed, error)

// probeWithFirstUse runs probe against item and, when it is refused over a
// certificate nothing in item's record trusts and o asks for first-use pins,
// runs it again against item trusting the certificate presented, recording
// nothing yet. It returns what the probe proved, the pin to record (nil for
// none), and the item whose TLS settings the probe that succeeded used.
func probeWithFirstUse(probe probeFunc, item inventory.InventoryItem, o options) (Probed, *FirstUsePin, inventory.InventoryItem, error) {
	probed, err := probe(item)
	var untrusted *devicetls.UntrustedError
	if err == nil || !o.firstUse || !errors.As(err, &untrusted) {
		return probed, nil, item, err
	}
	if own := ownTrust(devicetls.For(item)); own != "" {
		return Probed{}, nil, item, fmt.Errorf("%w; a certificate is recorded on first use only for a device whose record names nothing it trusts, and this one names %s, "+
			"which a first-use pin never replaces, so a changed certificate is still refused: check the one it presents with the device's owner, then pin it yourself", err, own)
	}
	pinned, buildErr := withFirstUsePin(item, untrusted.Fingerprint)
	if buildErr != nil {
		return Probed{}, nil, item, fmt.Errorf("%w; and no pin can be recorded for it: %w", err, buildErr)
	}
	if probed, err = probe(pinned); err != nil {
		return Probed{}, nil, item, err
	}
	return probed, &FirstUsePin{
		Fingerprint: untrusted.Fingerprint, KeyFingerprint: untrusted.KeyFingerprint,
		Subject: factText(untrusted.Subject), Issuer: factText(untrusted.Issuer),
		NotAfter: untrusted.NotAfter, Untrusted: factText(untrusted.Cause.Error()),
	}, pinned, nil
}

// ownTrust names what s's record trusts of its own, "" when it names nothing.
func ownTrust(s devicetls.Settings) string {
	switch {
	case s.FirstUsePin() != "":
		return devicetls.FirstUseProperty + " (a pin recorded on first use before)"
	case len(s.Pins()) > 0:
		return devicetls.FingerprintProperty
	case s.PinnedCA():
		return devicetls.CAPEMProperty
	}
	return ""
}

// withFirstUsePin returns item rebuilt by the factory with pin as its
// first-use pin, for a probe only: it is never saved, so it carries no
// revision. A type whose TLS is not read from its record, or that refuses a
// pin (a Kubernetes cluster, verified against its authority alone), cannot
// have one recorded.
func withFirstUsePin(item inventory.InventoryItem, pin string) (inventory.InventoryItem, error) {
	typed, ok := item.(deviceTyped)
	if !ok {
		return nil, fmt.Errorf("device %s reports no type", item.Name())
	}
	props := item.Properties().Raw()
	props[devicetls.FirstUseProperty] = pin
	built, err := inv.NewItemFactory().Build(record.Record{
		ID: item.ID(), Name: item.Name(), Type: typed.DeviceType(), Properties: props,
		Tags: item.Tags(), State: item.State(), Source: item.Source(),
	})
	if err != nil {
		return nil, err
	}
	if devicetls.For(built).FirstUsePin() != pin {
		return nil, fmt.Errorf("a %s does not read a TLS pin from its record", typed.DeviceType())
	}
	return built, nil
}

// recordPinChange writes what this onboarding changes about item's
// first-use pin: the one it recorded (firstUse, nil for none), or the removal
// of one the record's own trust has made unused. It reports whether anything
// changed and what the onboarding says about it.
func recordPinChange(item inventory.InventoryItem, firstUse *FirstUsePin) (bool, string, error) {
	unused := devicetls.For(item).UnusedFirstUsePin()
	if firstUse == nil && unused == "" {
		return false, "", nil
	}
	writer, ok := item.(firstUseWriter)
	if !ok {
		return false, "", fmt.Errorf("device %s cannot record a pin", item.Name())
	}
	if firstUse != nil {
		writer.RecordFirstUsePin(firstUse.Fingerprint)
		return true, firstUseWarning(item.Name(), firstUse), nil
	}
	writer.RemoveFirstUsePin()
	return true, fmt.Sprintf("device %q: removed the pin recorded on first use (%s %s), which the record's own trust overrides", item.Name(), devicetls.FirstUseProperty, unused), nil
}

// firstUseWarning is what the onboarding that recorded pin says about it.
func firstUseWarning(device string, pin *FirstUsePin) string {
	return fmt.Sprintf("device %q: recorded the certificate it presented as its pin on first use (%s %s, key %s), because nothing trusted it (%s); "+
		"nothing proves it is the device's: check both fingerprints with its owner, then confirm the pin, and until then every run says it is unconfirmed",
		device, devicetls.FirstUseProperty, pin.Fingerprint, pin.KeyFingerprint, pin.Untrusted)
}
