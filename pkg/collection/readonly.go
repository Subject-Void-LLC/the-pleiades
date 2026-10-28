// Package collection: whether one call of a method only reads, decided by
// the method's manifest or, for a method only some of whose calls read, by
// its ReadOnlyCall.
package collection

import "fmt"

// ReadsOnly reports whether a call of d with params changes nothing: d's
// manifest declares the whole method read-only (Reversibility.ReadOnly), or
// its ReadOnlyCall answers true for params. It reads nothing but the
// parameters, so validation can ask it before a run, and the engine asks it
// again of the parameters the call ran with.
func (d Descriptor) ReadsOnly(params map[string]any) bool {
	if d.Manifest.Reversibility.ReadOnly {
		return true
	}
	return d.ReadOnlyCall != nil && d.ReadOnlyCall(params)
}

// checkReadOnlyCall refuses a ReadOnlyCall on a method whose manifest
// already declares it read-only, where every call reads and the function
// would answer a question nothing asks.
func checkReadOnlyCall(d Descriptor) error {
	if d.ReadOnlyCall != nil && d.Manifest.Reversibility.ReadOnly {
		return fmt.Errorf("collection: %q carries a ReadOnlyCall function but its manifest already declares every call read-only", d.Name)
	}
	return nil
}
