//go:build unix

// Package loader: tests that a program cannot claim a namespace Pleiades
// reserves.
package loader

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerBuiltin registers a method with no provider, as a package
// compiled into the binary would, so its namespace becomes one of the
// catalog's.
func registerBuiltin(t *testing.T, name string) {
	t.Helper()
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture"},
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestLoad_ReservedNamespacesAreRefused proves an external method may not
// use a namespace that belongs to Pleiades: any a built-in method uses,
// read from the registry when Load runs, so registering a built-in in a
// brand-new namespace reserves it with no other change, plus pleiades and
// ansible always. A namespace of the program's own is accepted, and an
// external method does not reserve its namespace for others.
func TestLoad_ReservedNamespacesAreRefused(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	registerBuiltin(t, "brandnew.builtin.method")

	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "brandnew.external.method", want: `the "brandnew" namespace`},
		{name: "pleiades.custom.thing", want: `the "pleiades" namespace`},
		{name: "ansible.builtin.copy", want: `the "ansible" namespace`},
		{name: "acme.motd.set"},
		{name: "example.note.write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			out := describeJSON(t, 0, external.DescribedMethod{Name: tc.name, Manifest: implemented(false)})
			writeProgram(t, dir, "prog", script(out, `printf '{}' >&3`))
			err := tryLoad(t, dir, testOptions())
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Load refused a namespace of the program's own: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("Load = %v, want a refusal naming %s", err, tc.want)
			}
		})
	}

	// An external method's namespace is not reserved: a second program may
	// use it (a different name, since names are never shared).
	t.Run("external namespaces stay open", func(t *testing.T) {
		restore := collection.SnapshotForTest()
		defer restore()
		first := programDir(t)
		writeProgram(t, first, "prog", script(describeJSON(t, 0, external.DescribedMethod{Name: "shared.first.run", Manifest: implemented(false)}), `printf '{}' >&3`))
		if _, err := Load(t.Context(), first, testOptions()); err != nil {
			t.Fatal(err)
		}
		second := programDir(t)
		writeProgram(t, second, "prog", script(describeJSON(t, 0, external.DescribedMethod{Name: "shared.second.run", Manifest: implemented(false)}), `printf '{}' >&3`))
		if _, err := Load(t.Context(), second, testOptions()); err != nil {
			t.Errorf("an external method reserved its namespace: %v", err)
		}
	})
}
