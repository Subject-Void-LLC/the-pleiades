// Package collection: the shape of a built-in method's fact names.
//
// A fact is stored per device and key, so its key is the identity of its
// history, and baselines, targeting and conditions read it by that key. The
// built-in names follow two rules, held here for every built-in method:
//
//   - a key has a family: a dot that is neither first nor last, the family
//     being the text before the first dot (`os.version`, the family `package.`);
//   - no key is both a fact and the start of another of the same method's facts
//     (`os.version` beside `os.version.major`), so within a method a nested
//     reading of the names and the flat one agree. Across methods it is not
//     checked here; the built-in names are chosen so no such pair exists.
package collection

import (
	"fmt"
	"strings"
)

// checkFactNames holds a built-in method's fact declarations to the two rules
// above.
func checkFactNames(d Descriptor) error {
	for _, s := range d.Manifest.Facts {
		if !hasFamily(s) {
			return fmt.Errorf("collection: %q declares fact %s, which has no family: a built-in fact's name starts with its family and a dot (os.version, package.)", d.Name, factName(s))
		}
		if s.Prefix {
			continue
		}
		for _, o := range d.Manifest.Facts {
			if o.Key != s.Key && strings.HasPrefix(o.Key, s.Key+".") {
				return fmt.Errorf("collection: %q declares fact %s and also %s inside it; a fact is never also the start of another's name", d.Name, factName(s), factName(o))
			}
		}
	}
	return nil
}

// hasFamily reports whether s's name starts with a family: a dot that is not
// first, and for a single key not last (a family's own prefix ends in one).
func hasFamily(s FactSpec) bool {
	dot := strings.IndexByte(s.Key, '.')
	if dot <= 0 {
		return false
	}
	return s.Prefix || !strings.HasSuffix(s.Key, ".")
}
