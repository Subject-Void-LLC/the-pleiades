// Tests that an external Collection's child hands a method the registry
// account its request carried.
package external

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRunbookContext_RegistryOnlyForItsHost: the child's run answers a
// method's request for a registry account only for the account's own host,
// and not at all when the request carried none, which is every request an
// external program receives.
func TestRunbookContext_RegistryOnlyForItsHost(t *testing.T) {
	rc := NewRunbookContext(nil)
	if _, _, ok := rc.RegistryCredential("ghcr.io"); ok {
		t.Error("a run with no registry account answered")
	}
	rc.registry = &wire.RegistryCredential{Host: "ghcr.io", Username: "u", Password: "p"}
	if u, p, ok := rc.RegistryCredential("ghcr.io"); !ok || u != "u" || p != "p" {
		t.Errorf("own host = %q, %q, %v", u, p, ok)
	}
	if _, _, ok := rc.RegistryCredential("docker.io"); ok {
		t.Error("another host got the account")
	}
	if secrets := rc.InjectSecrets(); len(secrets) != 0 {
		t.Errorf("the registry account reached InjectSecrets: %v", secrets)
	}
}
