// ai.model.pull: a model onto an Ollama host when it is not there.
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
		Name: fqcnPull,
		Manifest: manifest(pullDoc(), collection.Reversibility{
			Reversible: true, Inverses: undoes(fqcnRemove),
			Notes: "The undo removes a model this pull added, and is recorded only then: a model already on the host is never removed by undoing a pull.",
		}),
		Invoke: Pull,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return pull(ctx, rc, d, p, true)
		},
	})
}

// Pull implements ai.model.pull.
func Pull(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return pull(ctx, rc, device, params, false)
}

func pull(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnPull, err)
	}
	h, model, ctx, cancel, disk, _, err := begin(ctx, rc, device, params)
	if err != nil {
		return fail(err)
	}
	defer cancel()
	present := has(disk, model)
	if err := setStats(rc, map[string]any{"model": model, "present_before": present}); err != nil {
		return fail(err)
	}
	if check || present {
		return collection.Result{Changed: !present}, nil
	}
	if err := aiprovider.OllamaPull(ctx, h.ep, model); err != nil {
		return fail(err)
	}
	// The undo is recorded once the model is known to be there, and only
	// because this pull put it there.
	after, err := aiprovider.OllamaModels(ctx, h.ep)
	if err != nil {
		return fail(fmt.Errorf("listing models after the pull: %w", err))
	}
	if !has(names(after), model) {
		return fail(fmt.Errorf("Ollama reported the pull of %s succeeded, and does not list it", model))
	}
	undo := inverse(fqcnRemove, model)
	undo.Description = fmt.Sprintf("Remove %s, which this pull added, from %s.", model, h.name)
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
