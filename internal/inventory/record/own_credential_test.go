// Tests of the registry of device types that authenticate only with their
// own credential.
package record_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
)

// TestUsesOwnCredential: a registered type uses its own credential; any
// other type, registered as nothing or unknown, does not.
func TestUsesOwnCredential(t *testing.T) {
	record.RegisterOwnCredentialType("record_test_own_credential")
	if !record.UsesOwnCredential("record_test_own_credential") {
		t.Error("a registered type does not use its own credential")
	}
	for _, typ := range []string{"record_test_unregistered", ""} {
		if record.UsesOwnCredential(typ) {
			t.Errorf("%q uses its own credential, never having been registered", typ)
		}
	}
}
