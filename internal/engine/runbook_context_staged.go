// A run's permission for a check to stage a change in a device's own staging
// area and discard it (sdk.StagedChecks), which only the command line grants.
package engine

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// WithStagedChecks makes every context next builds answer sdk.StagedChecks
// with true. Only the command line wraps its contexts so: a Walk-tier check
// job promises to send no write, and a Runner's contexts answer false until a
// Runner holds such a check to its staging area's RPCs.
func WithStagedChecks(next RunbookContextFunc) RunbookContextFunc {
	return func(ctx context.Context, device inventory.InventoryItem) (sdk.RunbookContext, error) {
		rc, err := next(ctx, device)
		if err != nil {
			return nil, err
		}
		if c, ok := rc.(*runbookContext); ok {
			c.stagedChecks = true
		}
		return rc, nil
	}
}

// StagedChecksAllowed implements sdk.StagedChecks.
func (c *runbookContext) StagedChecksAllowed() bool { return c.stagedChecks }
