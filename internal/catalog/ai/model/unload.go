// ai.model.unload: a model out of an Ollama host's memory.
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
		Name: fqcnUnload,
		Manifest: manifest(unloadDoc(), collection.Reversibility{
			Reversible: true, Inverses: undoes(fqcnLoad),
			Notes: "The undo loads the model again, which unloads any other loaded since.",
		}),
		Invoke: Unload,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return unload(ctx, rc, d, p, true)
		},
	})
}

// Unload implements ai.model.unload.
func Unload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return unload(ctx, rc, device, params, false)
}

func unload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnUnload, err)
	}
	h, model, ctx, cancel, _, memory, err := begin(ctx, rc, device, params)
	if err != nil {
		return fail(err)
	}
	defer cancel()
	resident := has(memory, model)
	if err := setStats(rc, map[string]any{"model": model, "resident_before": strs(memory)}); err != nil {
		return fail(err)
	}
	if check || !resident {
		return collection.Result{Changed: resident}, nil
	}
	if err := aiprovider.OllamaUnload(ctx, h.ep, model); err != nil {
		return fail(err)
	}
	undo := inverse(fqcnLoad, model)
	undo.Description = fmt.Sprintf("Load %s on %s again.", model, h.name)
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
