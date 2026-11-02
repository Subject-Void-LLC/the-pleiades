// Package file: what this package's checks record about the paths they would
// make or remove (Phase 46a, pkg/sdk's predicted.go), so a later task in the
// same check reads the device as an earlier task leaves it. Every method here
// that makes or removes a path declares collection.Manifest.PredictsPaths and
// records its path; a path the ledger cannot key as written (not absolute, or
// not cleaned, whose real target only the device knows) makes the device's
// predictions unknown instead.
package file

import (
	"context"
	pathpkg "path"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// recordRemoved records, in a check, that target is predicted gone.
func recordRemoved(rc sdk.RunbookContext, target string) error {
	return recordPath(rc, target, wire.PredictedAbsent)
}

// recordPath records target in state, or the device's predictions unknown
// when target is not a path the ledger can key as written.
func recordPath(rc sdk.RunbookContext, target, state string) error {
	if !pathpkg.IsAbs(target) || pathpkg.Clean(target) != target {
		return sdk.RecordPredictionUnknown(rc)
	}
	return sdk.RecordPredicted(rc, wire.PredictedPath{Path: target, State: state})
}

// predictMade wraps a check whose real run makes the path its first present
// parameter among names names: once the check succeeds, that path is
// recorded present, so a later task's check of the directory holding it does
// not read the directory as empty.
func predictMade(check collection.Method, names ...string) collection.Method {
	return func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		result, err := check(ctx, rc, device, params)
		if err != nil {
			return result, err
		}
		for _, name := range names {
			if target, ok := params[name].(string); ok && target != "" {
				return result, recordPath(rc, target, wire.PredictedPresent)
			}
		}
		return result, sdk.RecordPredictionUnknown(rc)
	}
}
