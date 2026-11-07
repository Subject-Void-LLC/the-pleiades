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

// checkExternalFacts refuses an external method's declaration of a reserved
// fact (ReservedFact), naming what holds it and the namespace its facts
// belong in.
func checkExternalFacts(d Descriptor) error {
	ns, _, _ := strings.Cut(d.Name, ".")
	for _, s := range d.Manifest.Facts {
		if by, reserved := ReservedFact(s); reserved {
			return fmt.Errorf("collection: %q declares fact %s, which %s holds; an external Collection's facts go under its own namespace, such as %q",
				d.Name, factName(s), by, ns+"."+strings.TrimPrefix(s.Key, "."))
		}
	}
	return nil
}
