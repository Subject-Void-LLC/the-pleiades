// Tests for Register's rule on Manifest.SeedsLogin.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestRegister_SeedsLogin(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	descriptor := func(name string, params []collection.Param, provider *collection.Provider) collection.Descriptor {
		return collection.Descriptor{
			Name: name,
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: collection.Reversibility{Notes: "a test fixture"},
				SeedsLogin:    "login",
				Doc:           collection.Doc{Params: params},
			},
			Invoke:   noop,
			Provider: provider,
		}
	}
	login := collection.Param{Name: "login", Type: "string", Required: true}
	if err := collection.Register(descriptor("seedtest.ok", []collection.Param{login}, nil)); err != nil {
		t.Fatalf("a declared required string: %v", err)
	}
	for name, tt := range map[string]struct {
		params   []collection.Param
		provider *collection.Provider
		want     string
	}{
		"no such parameter":  {nil, nil, "does not declare"},
		"an optional one":    {[]collection.Param{{Name: "login", Type: "string"}}, nil, "does not declare"},
		"not a string":       {[]collection.Param{{Name: "login", Type: "list", Required: true}}, nil, "does not declare"},
		"an external method": {[]collection.Param{login}, &collection.Provider{}, "external Collection"},
	} {
		err := collection.Register(descriptor("seedtest."+strings.ReplaceAll(name, " ", "_"), tt.params, tt.provider))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
	}
}
