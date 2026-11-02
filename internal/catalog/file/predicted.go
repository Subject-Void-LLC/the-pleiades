// Package file: what file.remove's check records about the paths it would
// remove (Phase 46a, pkg/sdk's predicted.go), so a later task in the same
// check reads the device as an earlier removal leaves it. file.remove
// declares collection.Manifest.PredictsPaths and records its path, located as
// the device resolves it (remotefile.Locate). A path that resolves anywhere
// but where it is written (a link along it), or that cannot be located (not
// absolute or not cleaned, or a read that failed), is recorded as unknown,
// which drops the run's predictions: where a link leads in the real run
// depends on what earlier tasks did to it. No method that makes a path
// declares it, since a claim that a path is gone is exactly what making one
// can make false.
package file

import (
	"context"

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

// recordRemoved records, in a check that keeps predictions, loc removed, or
// the run's predictions unknown when the path could not be located.
func recordRemoved(rc sdk.RunbookContext, loc wire.Location, located bool) error {
	if !sdk.PredictionsWanted(rc) {
		return nil
	}
	if !located {
		return sdk.RecordPredictionUnknown(rc)
	}
	return sdk.RecordPredicted(rc, wire.PredictedPath{Path: loc.Path, State: wire.PredictedAbsent, Dirs: loc.Dirs})
}

// recordNone records, in a check that keeps predictions, that this call
// removes nothing, or the run's predictions unknown when its path could not
// be located.
func recordNone(rc sdk.RunbookContext, located bool) error {
	if !sdk.PredictionsWanted(rc) {
		return nil
	}
	if !located {
		return sdk.RecordPredictionUnknown(rc)
	}
	return sdk.RecordPredicted(rc)
}
