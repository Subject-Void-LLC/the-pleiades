// FuzzDnfParams is the pkg.dnf parameter fuzz target, which Phase 38
// requires before a status flips, in the shape of exec's FuzzCommandParams.
//
// The boundary is the package name and version, out of a runbook file and
// parsed before anything connects. The seeds include a name beginning with
// a dash, one holding a wildcard, a group, a file and a version holding a
// wildcard, the holes FAILURE_PATTERNS 1174 named for dnf. The device is
// nil, so any input that parses stops at the connection, which keeps every
// iteration off the network. The properties are no panic, no change claimed
// beside an error, and refusedBeforeConnecting: a name or version the RPM
// rules refuse never gets as far as the connection, which with no device
// fails saying there is none, so a refusal removed from a method fails here
// rather than passing on the nil device's error.
package dnf_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// refusedBeforeConnecting fails t when a value the RPM rules refuse got as
// far as the connection. readsVersion says whether the method reads the
// version (install does, remove and upgrade do not).
func refusedBeforeConnecting(t *testing.T, err error, name, version string, readsVersion bool) {
	t.Helper()
	refused := operand.RPMPackage.Problem(name) != "" || (readsVersion && operand.PackageVersion.Problem(version) != "")
	if refused && strings.Contains(err.Error(), "no target device") {
		t.Fatalf("name %q or version %q, which the RPM rules refuse, reached the connection: %v", name, version, err)
	}
}

func FuzzDnfParams(f *testing.F) {
	f.Add("curl", "")
	f.Add("", "")
	f.Add("nginx", "1.24.0-1.el9")
	f.Add("--setopt=tsflags=noscripts", "")
	f.Add("openssh*", "")
	f.Add("kernel", "\x00")
	f.Add("libstdc++", "; rm -rf /")
	// Each refusal alone, beside params that otherwise reach the connection.
	f.Add("@core", "")
	f.Add("/usr/bin/jq", "")
	f.Add("perl(strict)", "")
	f.Add("nginx", "*")
	f.Add("nginx", "1.0-")

	methods := []struct {
		fn           func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)
		readsVersion bool
	}{
		{dnf.Install, true}, {dnf.CheckInstall, true}, {dnf.Remove, false}, {dnf.CheckRemove, false}, {dnf.Upgrade, false}, {dnf.CheckUpgrade, false},
	}
	f.Fuzz(func(t *testing.T, name, version string) {
		params := map[string]any{"name": name, "version": version, "insecure_skip_host_key_verify": true}
		for _, m := range methods {
			result, err := m.fn(context.Background(), &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("a pkg.dnf method with no target device returned no error for name %q version %q", name, version)
			}
			if result.Changed {
				t.Fatalf("a pkg.dnf method reported a change alongside an error for name %q version %q", name, version)
			}
			refusedBeforeConnecting(t, err, name, version, m.readsVersion)
		}
	})
}
