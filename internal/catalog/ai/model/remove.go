// ai.model.remove: a model off an Ollama host's disk.
package model

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRemove,
		Manifest: manifest(removeDoc(), collection.Reversibility{
			Reversible: true, Inverses: undoes(fqcnPull),
			Notes: "The undo pulls the model's name again, which brings back what its tag names at that moment: the same model only while the tag has not moved.",
		}),
		Invoke: Remove,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, true)
		},
	})
}

// Remove implements ai.model.remove.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, false)
}

func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRemove, err)
	}
	h, model, ctx, cancel, disk, memory, err := begin(ctx, rc, device, params)
	if err != nil {
		return fail(err)
	}
	defer cancel()
	present := has(disk, model)
	if err := setStats(rc, map[string]any{"model": model, "present_before": present}); err != nil {
		return fail(err)
	}
	if check || !present {
		return collection.Result{Changed: present}, nil
	}
	if has(memory, model) {
		if err := aiprovider.OllamaUnload(ctx, h.ep, model); err != nil {
			return fail(fmt.Errorf("unloading %s first: %w", model, err))
		}
	}
	if err := aiprovider.OllamaDelete(ctx, h.ep, model); err != nil {
		return fail(err)
	}
	undo := inverse(fqcnPull, model)
	undo.Description = fmt.Sprintf("Pull %s onto %s again.", model, h.name)
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
