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
)

// ReservedFactFamilies are the fact families no external Collection may
// declare into, whether or not a built-in method declares them today. It is
// data the native fact names extend (os., host., hardware.). ansible_ is held
// because the playbook converter's mapping reads facts by those names.
var ReservedFactFamilies = []string{
	"ansible_", "file.", "group.", "package.", "port.", "service.", "service_state.", "user.",
}

// ReservedFact reports what reserves a key or family s declares: one of
// ReservedFactFamilies, or a built-in method's declaration that can name the
// same key. It reports false when nothing does. Built-in methods register at
// package init, before the loader registers any external one, so every
// built-in declaration is known when an external one is checked.
func ReservedFact(s FactSpec) (string, bool) {
	for _, family := range ReservedFactFamilies {
		if overlaps(s, FactSpec{Key: family, Prefix: true}) {
			return fmt.Sprintf("the reserved family %q*", family), true
		}
	}
	for name, d := range collections.All() {
		if d.Provider != nil {
			continue
		}
		for _, o := range d.Manifest.Facts {
			if overlaps(s, o) {
				return fmt.Sprintf("built-in method %q (its fact %s)", name, factName(o)), true
			}
		}
	}
	return "", false
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

// checkExternalFacts refuses an external method's declaration of a reserved
// fact (ReservedFact), naming what holds it and the namespace its facts
// belong in.
func checkExternalFacts(d Descriptor) error {
	ns, _, _ := strings.Cut(d.Name, ".")
	for _, s := range d.Manifest.Facts {
		if by, reserved := ReservedFact(s); reserved {
			return fmt.Errorf("collection: %q declares fact %s, which %s holds; an external Collection's facts go under its own namespace, such as %q",
				d.Name, factName(s), by, OwnFactName(ns, s))
		}
	}
	return nil
}
