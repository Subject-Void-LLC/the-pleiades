// A method that serves more than one kind of device: what each kind needs,
// chosen by the device itself, and the one check every tier makes of it.
//
// A manifest's RequiredCapabilities are all required at once, so a method
// that reads an FRR router through vtysh or a Cisco IOS router through its
// CLI cannot say what it needs in one list: FRR needs FRRCapable and
// CommandExecCapable together, over SSH only, and IOS needs CiscoIOSCapable,
// over SSH, Telnet or a console. A flat any-of list would admit an FRR
// device without command execution, or one reached only by a console. A
// shared parent capability, the way svc.* and pkg.* get theirs, does not fit
// either: FRRCapable has no parent by design. So each kind is an
// Alternative, with its own capabilities and transports, and a device is
// admitted when it fits one of them whole.
//
// Shapes (shapes.go) are a near cousin and a different question: there the
// call's parameters choose the shape, here the device chooses the
// alternative. A manifest declares one or the other, never both.
//
// Admits is the one check validate's capability and transport rules, the
// engine before a method runs and a Controller's admission all make, and
// ChooseAlternative is what the method itself calls to pick its code, so
// what was admitted and what runs cannot disagree. internal/archtest holds
// every other package to reading a manifest's requirements through here.
package collection

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Alternative is one kind of device a method serves, when it serves more
// than one: the capabilities a device of that kind holds and the transports
// the method reaches it over. A device fits it when it holds every one of
// the capabilities and reaches at least one of the transports.
type Alternative struct {
	// Name is a short lowercase label for the kind (frr, ios), unique within
	// the method. Refusals and the reference page name the kind by it, and
	// the method selects its code by it.
	Name string `json:"name"`
	// SupportedTransports are this kind's, as Manifest's are. Each one is
	// also in the manifest's own SupportedTransports, which is their union.
	SupportedTransports []string `json:"supportedTransports,omitempty"`
	// RequiredCapabilities are this kind's, all required at once. At least
	// one, since a kind no capability marks is not a kind a device can be.
	RequiredCapabilities []capability.Name `json:"requiredCapabilities"`
}

// TransportError is CheckTransports' refusal: a device that reaches none of
// the transports a method declares. It is its own type so validate can tell
// a transport refusal from a capability one while making the one check.
type TransportError struct {
	// message is the whole refusal, as CheckTransports words it.
	message string
}

// Error implements error.
func (e *TransportError) Error() string { return e.message }

// Admits refuses a device a call held to m cannot run on: a capability m
// requires that the device lacks (CheckCapabilities), then a transport it
// does not reach (CheckTransports, a *TransportError), then, when m
// declares alternatives, a device that fits none of them whole
// (ChooseAlternative). m is a manifest For has already shaped. A nil device
// is refused when m requires anything of one.
//
// It is the one check validate's capability and transport rules, the engine
// before a method runs and a Controller's admission (AdmitsAnyShape) make,
// so plan time, admission and run time answer the same question the same
// way.
func Admits(device inventory.InventoryItem, fqcn string, m Manifest) error {
	if err := CheckCapabilities(device, fqcn, m); err != nil {
		return err
	}
	if err := CheckTransports(device, fqcn, m); err != nil {
		return err
	}
	_, err := ChooseAlternative(device, fqcn, m)
	return err
}

// ChooseAlternative returns the first of m's alternatives, in the order m
// declares them, that device fits whole, or a refusal naming every kind m
// serves. A manifest declaring no alternative returns the zero Alternative
// and no error, since there is nothing to choose between.
func ChooseAlternative(device inventory.InventoryItem, fqcn string, m Manifest) (Alternative, error) {
	if len(m.Alternatives) == 0 {
		return Alternative{}, nil
	}
	if device == nil {
		return Alternative{}, fmt.Errorf("collection method %q runs on a device that is %s, but the task has no target device",
			fqcn, describeAlternatives(m.Alternatives))
	}
	for _, a := range m.Alternatives {
		if fits(device, a) {
			return a, nil
		}
	}
	return Alternative{}, fmt.Errorf("collection method %q runs on a device that is %s, and device %q is none of them (it reaches %s)",
		fqcn, describeAlternatives(m.Alternatives), device.Name(), describeReach(device))
}

// fits reports whether device holds every capability a requires and reaches
// one of its transports (any, when a declares none).
func fits(device inventory.InventoryItem, a Alternative) bool {
	for _, name := range a.RequiredCapabilities {
		if !device.HasCapability(name) {
			return false
		}
	}
	if len(a.SupportedTransports) == 0 {
		return true
	}
	for _, t := range a.SupportedTransports {
		if reaches(device, t) {
			return true
		}
	}
	return false
}

// describeAlternatives words every alternative for a refusal: its name,
// the capabilities it needs and the transports it takes.
func describeAlternatives(alts []Alternative) string {
	parts := make([]string, 0, len(alts))
	for _, a := range alts {
		caps := make([]string, len(a.RequiredCapabilities))
		for i, c := range a.RequiredCapabilities {
			caps[i] = string(c)
		}
		part := fmt.Sprintf("%s (%s", a.Name, strings.Join(caps, " and "))
		if len(a.SupportedTransports) > 0 {
			part += ", over " + strings.Join(a.SupportedTransports, ", ")
		}
		parts = append(parts, part+")")
	}
	return "one of: " + strings.Join(parts, "; ")
}

// Requires reports whether every call held to m requires capability name:
// m's own RequiredCapabilities hold it, or m declares alternatives and every
// one of them does. It answers a planner's question (which engine a task
// reaches) without the planner reading the fields itself.
func (m Manifest) Requires(name capability.Name) bool {
	if slices.Contains(m.RequiredCapabilities, name) {
		return true
	}
	if len(m.Alternatives) == 0 {
		return false
	}
	for _, a := range m.Alternatives {
		if !slices.Contains(a.RequiredCapabilities, name) {
			return false
		}
	}
	return true
}

// NamedCapabilities returns every capability m names anywhere, once each, in
// the order it first appears: its own RequiredCapabilities, then each call
// shape's, then each alternative's. A rule about which capabilities a method
// may name at all (the loader's rule that an external method names only its
// own program's and the built-in ones) reads this, so no part of a manifest
// can name one the rule never sees.
func (m Manifest) NamedCapabilities() []capability.Name {
	var names []capability.Name
	add := func(list []capability.Name) {
		for _, n := range list {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	add(m.RequiredCapabilities)
	for _, s := range m.Shapes {
		add(s.RequiredCapabilities)
	}
	for _, a := range m.Alternatives {
		add(a.RequiredCapabilities)
	}
	return names
}

// ValidateAlternatives refuses a manifest whose alternatives cannot be
// chosen between: one declared beside call shapes, or alone, or unnamed, or
// named twice, or with no capability, an unknown capability or transport, a
// transport outside the manifest's own, or a manifest transport no
// alternative takes. Register calls it, and so does the loader before it
// registers anything, so a program's directory does not load halfway.
func ValidateAlternatives(fqcn string, m Manifest) error {
	if len(m.Alternatives) == 0 {
		return nil
	}
	switch {
	case len(m.Shapes) > 0:
		return fmt.Errorf("collection: %q declares both call shapes and alternatives; a call is chosen by its parameters or by its device, not both", fqcn)
	case len(m.Alternatives) == 1:
		return fmt.Errorf("collection: %q declares one alternative; declare its capabilities and transports as the manifest's own", fqcn)
	}
	seen := map[string]bool{}
	taken := map[string]bool{}
	for _, a := range m.Alternatives {
		switch {
		case a.Name == "" || strings.ToLower(a.Name) != a.Name || strings.ContainsAny(a.Name, " \t\r\n"):
			return fmt.Errorf("collection: %q declares an alternative named %q; a name is a short lowercase word", fqcn, a.Name)
		case seen[a.Name]:
			return fmt.Errorf("collection: %q declares two alternatives named %q", fqcn, a.Name)
		case len(a.RequiredCapabilities) == 0:
			return fmt.Errorf("collection: %q's alternative %q requires no capability, so no device is of its kind rather than another's", fqcn, a.Name)
		}
		seen[a.Name] = true
		for _, name := range a.RequiredCapabilities {
			if _, known := capability.Lookup(name); !known {
				return fmt.Errorf("collection: %q's alternative %q requires unknown capability %q", fqcn, a.Name, name)
			}
		}
		if err := checkTransportNames(Descriptor{Name: fqcn, Manifest: Manifest{SupportedTransports: a.SupportedTransports}}); err != nil {
			return err
		}
		if len(a.SupportedTransports) == 0 && len(m.SupportedTransports) > 0 {
			return fmt.Errorf("collection: %q's alternative %q declares no transport, and the method declares %s", fqcn, a.Name, strings.Join(m.SupportedTransports, ", "))
		}
		for _, t := range a.SupportedTransports {
			if !slices.Contains(m.SupportedTransports, t) {
				return fmt.Errorf("collection: %q's alternative %q takes transport %q, which the method's own transports do not list", fqcn, a.Name, t)
			}
			taken[t] = true
		}
	}
	for _, t := range m.SupportedTransports {
		if !taken[t] {
			return fmt.Errorf("collection: %q declares transport %q, which none of its alternatives takes", fqcn, t)
		}
	}
	return nil
}
