// container.create: a container made as declared, and left unstarted.
package container

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCreate,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that created the container records its removal, by ID; one that found it as declared records nothing.",
			Inverses:   convergeInverses[:1],
		}, createDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return converge(ctx, rc, d, p, collection.ModeExecute, fqcnCreate, false)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return converge(ctx, rc, d, p, collection.ModeCheck, fqcnCreate, false)
		},
	})
}
