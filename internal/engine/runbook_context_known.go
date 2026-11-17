// The values a run masks from its start, handed to a method that asks
// (sdk.KnownSecrets), which only the command line's contexts carry.
package engine

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// WithKnownSecretValues makes every context next builds answer
// sdk.KnownSecrets with values: the command line passes the project's named
// secrets, the same values it gives the executor through WithKnownSecrets,
// so a method masking text it writes into the project masks by what the run
// masks its own output by. A Runner's contexts carry none.
func WithKnownSecretValues(next RunbookContextFunc, values []string) RunbookContextFunc {
	known := append([]string(nil), values...)
	return func(ctx context.Context, device inventory.InventoryItem) (sdk.RunbookContext, error) {
		rc, err := next(ctx, device)
		if err != nil {
			return nil, err
		}
		if c, ok := rc.(*runbookContext); ok {
			c.known = known
		}
		return rc, nil
	}
}

// KnownSecretValues implements sdk.KnownSecrets.
func (c *runbookContext) KnownSecretValues() []string {
	return append([]string(nil), c.known...)
}
