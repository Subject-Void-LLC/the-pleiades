// Package collection: the fact names The Pleiades keeps for itself.
//
// Facts drive targeting, baselines and conditions, and a device's facts are
// kept by key whatever method wrote them. So an external Collection that
// declared a built-in key (`package.openssh-server`, `ansible_distribution`)
// could make a device read as something it is not and steer what runs on it.
// An external method may not declare a fact a built-in family or method holds;
// its facts go under its own namespace.
package collection

import (
	"fmt"
	"strings"
	"unicode"
)

// ReservedFactFamilies are the fact families no external Collection may
// declare into, whether or not a built-in method declares them today: the
// item families, the native host families the fact-name rename fills (os.,
// host., hardware., network., catalyst.), the management-plane families every
// NETCONF session fills (netconf., yang.; 2026-11-14), and ansible_, because the playbook
// converter's mapping reads facts by those names. A family is reserved in the
// release before any built-in fills it, since adding one later would refuse a
// program the release before loaded, which a patch release may not do.
var ReservedFactFamilies = []string{
	"ansible_", "catalyst.", "file.", "group.", "hardware.", "host.", "netconf.", "network.", "os.",
	"package.", "port.", "service.", "service_state.", "user.", "yang.",
}

// ReservedFact reports what reserves a key or family s declares: the fact
// floor (FactFloor: ReservedFactFamilies and every first-party namespace as a
// prefix, whether or not this binary links its methods), or a built-in
// method's declaration that can name the same key. It reports false when nothing does. Names are compared without
// case, and a key equal to a family's own name (package.) counts as inside
// it, so neither Package.x nor the bare family name slips past. Built-in
// methods register at package init, before the loader registers any external
// one, so every built-in declaration is known when an external one is checked.
func ReservedFact(s FactSpec) (string, bool) {
	folded := FactSpec{Key: strings.ToLower(s.Key), Prefix: s.Prefix}
	for _, family := range FactFloor() {
		if folded.Key == family || overlaps(folded, FactSpec{Key: family, Prefix: true}) {
			return fmt.Sprintf("the reserved family or first-party namespace %q*", family), true
		}
	}
	for name, d := range collections.All() {
		if d.Provider != nil {
			continue
		}
		for _, o := range d.Manifest.Facts {
			if overlaps(folded, FactSpec{Key: strings.ToLower(o.Key), Prefix: o.Prefix}) {
				return fmt.Sprintf("built-in method %q (its fact %s)", name, factName(o)), true
			}
		}
	}
	return "", false
}

// CheckExternalFact is the one rule an external method's fact declaration is
// held to beyond ValidateFacts, called by Register and by the loader's pass
// one alike: its key is ASCII (so no lookalike letter can pass for a reserved
// name), and nothing reserves it (ReservedFact). method is the method's
// fully-qualified name.
func CheckExternalFact(method string, s FactSpec) error {
	ns, _, _ := strings.Cut(method, ".")
	for _, r := range s.Key {
		if r > unicode.MaxASCII {
			return fmt.Errorf("declares fact %q, whose name is not ASCII; an external Collection's fact names are ASCII, so none can pass for a reserved one", s.Key)
		}
	}
	if by, reserved := ReservedFact(s); reserved {
		return fmt.Errorf("declares fact %s, which %s holds; an external Collection's facts go under its own namespace, such as %q",
			factName(s), by, OwnFactName(ns, s))
	}
	return nil
}

// OwnFactName suggests where s belongs under namespace ns: the key after
// "ns.", or for a family the namespace itself, since "acme.package." names no
// family an author would mean.
func OwnFactName(ns string, s FactSpec) string {
	if s.Prefix {
		return ns + ".*"
	}
	return ns + "." + strings.TrimPrefix(s.Key, ".")
}

// checkBuiltinAfterExternal refuses a built-in method declaring a fact an
// external method registered before it already declares. Built-ins register
// at package init, before any loader runs, so this never fires in a binary
// built as this repository builds one; it keeps the rule from resting on
// that order, since an external fact registered first would otherwise pass
// the agreement check below with the built-in's own kind and type.
func checkBuiltinAfterExternal(d Descriptor) error {
	for name, other := range collections.All() {
		if other.Provider == nil {
			continue
		}
		for _, o := range other.Manifest.Facts {
			for _, s := range d.Manifest.Facts {
				if overlaps(s, o) {
					return fmt.Errorf("collection: built-in %q declares fact %s, which external %q (registered first) declares as %s; built-in methods register before external ones",
						d.Name, factName(s), name, factName(o))
				}
			}
		}
	}
	return nil
}

// checkExternalFacts holds each of an external method's fact declarations
// to CheckExternalFact.
func checkExternalFacts(d Descriptor) error {
	for _, s := range d.Manifest.Facts {
		if err := CheckExternalFact(d.Name, s); err != nil {
			return fmt.Errorf("collection: %q %w", d.Name, err)
		}
	}
	return nil
}
