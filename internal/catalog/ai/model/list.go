// ai.model.list: what an Ollama host has on disk and in memory.
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
		Name:     fqcnList,
		Manifest: manifest(listDoc(), collection.Reversibility{ReadOnly: true, Notes: "It asks the host which models it has on disk and in memory, and changes nothing."}),
		Invoke:   List,
		// It only reads, so a check of it is the task itself.
		Check: List,
	})
}

// List implements ai.model.list.
func List(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	h, err := hostFor(rc, device, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}
	disk, err := aiprovider.OllamaModels(ctx, h.ep)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: listing models: %w", fqcnList, err)
	}
	memory, err := aiprovider.OllamaResident(ctx, h.ep)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: listing models in memory: %w", fqcnList, err)
	}
	models := make([]any, len(disk))
	for i, m := range disk {
		models[i] = map[string]any{"name": Normalize(m.Name), "size": m.Size, "digest": m.Digest}
	}
	if err := setStats(rc, map[string]any{"models": models, "resident": strs(names(memory))}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnList, err)
	}
	return collection.Result{}, nil
}
