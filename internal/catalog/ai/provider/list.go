// ai.provider.list: what a provider is and which models it serves.
package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnList,
		Manifest: manifest(listDoc(), collection.Reversibility{
			ReadOnly: true, Notes: "It reads the provider's record and asks it which models it serves, which no provider counts as a call.",
		}),
		Invoke: List,
		// It only reads, so a check of it is the task itself.
		Check: List,
	})
}

// List implements ai.provider.list.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}
	t, err := aitask.TargetFor(device, rc.InjectSecrets())
	if err != nil {
		return fail(err)
	}
	if err := sdk.RecordWarnings(rc, t.Provider.Warnings); err != nil {
		return fail(err)
	}
	models, err := t.Provider.Kind.Models(ctx, t.Provider.Endpoint)
	if err != nil {
		return fail(fmt.Errorf("listing models: %w", err))
	}
	slices.Sort(models)
	models = slices.Compact(models)
	prices := map[string]any{}
	for _, m := range t.Prices.Models() {
		p := t.Prices[m]
		prices[m] = map[string]any{"input": p.Input, "output": p.Output, "currency": p.Currency}
	}
	guard := ""
	if dev, ok := device.(capability.AIProviderCapable); ok {
		guard = dev.AIHostGuard()
	}
	stats := map[string]any{
		"kind": t.Provider.Kind.Name(), "api_base": t.Provider.Endpoint.Base.String(), "residency": t.Residency,
		"models": strs(models), "prices": prices, "host_guard": guard,
	}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	return collection.Result{}, nil
}
