// Tests that Register holds a method's absence declaration together
// (Phase 131, R4): a NoExistsReason beside an exists return, or on a method
// that is not read-only, is refused, and either one alone is registered.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// reader is an implemented read-only method, declaring exists when exists
// is set and carrying reason as its NoExistsReason.
func reader(name string, exists bool, reason string) collection.Descriptor {
	d := withCheck(name, true, checkBody)
	d.Manifest.Reversibility = collection.Reversibility{ReadOnly: true, Notes: "It only reads."}
	d.Manifest.NoExistsReason = reason
	if exists {
		d.Manifest.Doc.Returns = []collection.ReturnField{{Name: collection.ExistsReturn, Type: "bool", Returned: "always", Description: "Whether it is there."}}
	}
	return d
}

// TestRegister_RefusesAContradictoryAbsenceDeclaration: a reason beside an
// exists return, or on a method that is not read-only, is refused and not
// registered.
func TestRegister_RefusesAContradictoryAbsenceDeclaration(t *testing.T) {
	notReadOnly := withCheck("test.absence_writer", true, checkBody)
	notReadOnly.Manifest.NoExistsReason = collection.NoExistsLists
	for name, tc := range map[string]struct {
		d    collection.Descriptor
		want string
	}{
		"an exists return and a reason":            {reader("test.absence_both", true, collection.NoExistsLists), "also a reason it answers none"},
		"a reason on a method that changes things": {notReadOnly, "is not read-only"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			if err := collection.Register(tc.d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Register = %v, want an error containing %q", err, tc.want)
			}
			if _, ok := collection.Lookup(tc.d.Name); ok {
				t.Error("the contradictory descriptor was registered despite the refusal")
			}
		})
	}
}

// TestRegister_AcceptsEitherAbsenceDeclaration: an exists return alone, a
// reason alone, or neither is registered, and the reason is kept.
func TestRegister_AcceptsEitherAbsenceDeclaration(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	for _, d := range []collection.Descriptor{
		reader("test.absence_lookup", true, ""),
		reader("test.absence_list", false, collection.NoExistsLists),
		// Neither is Register's to refuse: the catalog test holds built-ins
		// to one of the two, and an external method may give neither.
		reader("test.absence_neither", false, ""),
	} {
		if err := collection.Register(d); err != nil {
			t.Errorf("%s: %v", d.Name, err)
		}
	}
	got, _ := collection.Lookup("test.absence_list")
	if got.Manifest.NoExistsReason != collection.NoExistsLists {
		t.Errorf("the reason did not survive registration: %q", got.Manifest.NoExistsReason)
	}
	if m := (collection.Manifest{}).WithNoExistsReason("why"); m.NoExistsReason != "why" {
		t.Errorf("WithNoExistsReason set %q", m.NoExistsReason)
	}
}
