// Tests that the Runner masks a registry account's password and the header a
// method builds from it.
package native

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRegistrySecretValues_MasksTheEncodedHeader: a child's error can echo
// the X-Registry-Auth value, which carries the password encoded, so the
// Runner masks that value beside the password itself.
func TestRegistrySecretValues_MasksTheEncodedHeader(t *testing.T) {
	reg := &wire.RegistryCredential{Host: "GHCR.io", Username: "deployer", Password: "regsentinel-7Hq2xW9p"}
	header, err := containerengine.EncodeRegistryAuth(reg.Username, reg.Password, "ghcr.io")
	if err != nil {
		t.Fatal(err)
	}
	got := registrySecretValues(reg)
	for _, want := range []string{reg.Password, header} {
		if !slices.Contains(got, want) {
			t.Errorf("registrySecretValues = %v, missing %q", got, want)
		}
	}
	if registrySecretValues(nil) != nil || registrySecretValues(&wire.RegistryCredential{Host: "ghcr.io"}) != nil {
		t.Error("a credential with no password produced values to mask")
	}
}
