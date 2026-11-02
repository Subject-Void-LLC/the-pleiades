// Package file: what this package's checks record about the paths they would
// make or remove (Phase 46a, pkg/sdk's predicted.go), so a later task in the
// same check reads the device as an earlier task leaves it. file.copy,
// file.template, file.directory, file.touch and file.remove declare
// collection.Manifest.PredictsPaths and record their path, located as the
// device resolves it (remotefile.Locate). A path that resolves anywhere but
// where it is written (a link along it), or that cannot be located (not
// absolute or not cleaned, or a read that failed), is recorded as unknown,
// which drops the device's predictions: where a link leads in the real run
// depends on what earlier tasks did to it. file.symlink does not declare it,
// since a link it makes changes where later paths lead.
package file

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// locate reads where target resolves, when this call runs in a check that
// keeps predictions; ok is false when it does not, when target cannot be
// located, or when it resolves anywhere but where it is written, and the
// caller then answers from the device as it is.
func locate(ctx context.Context, conn *remoteexec.Conn, rc sdk.RunbookContext, target string) (wire.Location, bool) {
	if !sdk.PredictionsWanted(rc) {
		return wire.Location{}, false
	}
	loc, err := remotefile.Locate(ctx, conn, target)
	return loc, err == nil && loc.Path == target
}

// recordAt records, in a check that keeps predictions, loc in state, or the
// device's predictions unknown when the path could not be located.
func recordAt(rc sdk.RunbookContext, loc wire.Location, located bool, state string) error {
	if !sdk.PredictionsWanted(rc) {
		return nil
	}
	if !located {
		return sdk.RecordPredictionUnknown(rc)
	}
	return sdk.RecordPredicted(rc, wire.PredictedPath{Path: loc.Path, State: state, Dirs: loc.Dirs})
}

// recordNone records, in a check that keeps predictions, that this call
// changes no path, or the device's predictions unknown when its path could
// not be located.
func recordNone(rc sdk.RunbookContext, located bool) error {
	if !sdk.PredictionsWanted(rc) {
		return nil
	}
	if !located {
		return sdk.RecordPredictionUnknown(rc)
	}
	return sdk.RecordPredicted(rc)
}

// predictMade wraps fqcn's check, whose real run makes the path its first
// present parameter among names names: once the check succeeds, in a check
// that keeps predictions, that path is located and recorded present, so a
// later task's check of the directory holding it does not read the directory
// as empty, nor the path as gone.
func predictMade(fqcn string, check collection.Method, names ...string) collection.Method {
	return func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		result, err := check(ctx, rc, device, params)
		if err != nil || !sdk.PredictionsWanted(rc) {
			return result, err
		}
		for _, name := range names {
			if target, ok := params[name].(string); ok && target != "" {
				conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
				if err != nil {
					return result, sdk.RecordPredictionUnknown(rc)
				}
				defer func() { _ = conn.Close() }()
				loc, located := locate(ctx, conn, rc, target)
				return result, recordAt(rc, loc, located, wire.PredictedPresent)
			}
		}
		return result, sdk.RecordPredictionUnknown(rc)
	}
}
