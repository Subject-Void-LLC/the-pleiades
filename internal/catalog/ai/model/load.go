// ai.model.load: one model resident, every other unloaded first.
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
		Name: fqcnLoad,
		Manifest: manifest(loadDoc(), collection.Reversibility{
			Reversible: true, Inverses: undoes(fqcnLoad, fqcnUnload),
			Notes: "The undo loads the model that was in memory before, which unloads this one; when none was, it unloads this one. It restores which model is resident, not how long the host had kept it.",
		}),
		Invoke: Load,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return load(ctx, rc, d, p, true)
		},
	})
}

// Load implements ai.model.load.
func Load(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return load(ctx, rc, device, params, false)
}

func load(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnLoad, err)
	}
	h, model, ctx, cancel, disk, memory, err := begin(ctx, rc, device, params)
	if err != nil {
		return fail(err)
	}
	defer cancel()
	if !has(disk, model) {
		if check {
			// An earlier task in the run may pull it; a check cannot know that.
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnLoad, collection.CannotCheck(fmt.Sprintf("%s is not on %s yet; a real run fails on it unless an earlier task pulls it (ai.model.pull)", model, h.name)))
		}
		return fail(fmt.Errorf("%s is not on %s: pull it first (ai.model.pull)", model, h.name))
	}
	var others []string
	for _, m := range memory {
		if m != model {
			others = append(others, m)
		}
	}
	loading := !has(memory, model)
	if err := setStats(rc, map[string]any{"model": model, "resident_before": strs(memory), "unloaded": strs(others), "loaded": loading}); err != nil {
		return fail(err)
	}
	changed := loading || len(others) > 0
	if check || !changed {
		return collection.Result{Changed: changed}, nil
	}
	// Every other model leaves memory first, so the host never holds two.
	var unloaded []string
	for _, m := range others {
		if err := aiprovider.OllamaUnload(ctx, h.ep, m); err != nil {
			return fail(restore(ctx, h, unloaded, fmt.Errorf("unloading %s: %w", m, err)))
		}
		unloaded = append(unloaded, m)
	}
	if loading {
		if err := aiprovider.OllamaLoad(ctx, h.ep, model); err != nil {
			return fail(restore(ctx, h, unloaded, fmt.Errorf("loading %s: %w", model, err)))
		}
	}
	undo := inverse(fqcnUnload, model)
	undo.Description = fmt.Sprintf("Unload %s from %s.", model, h.name)
	if len(others) > 0 {
		undo = inverse(fqcnLoad, others[0])
		undo.Description = fmt.Sprintf("Load %s on %s again, which unloads %s.", others[0], h.name, model)
	}
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
