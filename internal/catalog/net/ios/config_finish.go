// Package ios: how net.ios.config ends a change: with a checkpoint, the
// device's own differences and an undo returning to it; without one, the
// lines that would undo it, shown for the task's rollback: list. Both read
// what the lines did to a switch's VLAN database (config_vlans_run.go).
package ios

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// finishCheckpoint ends a change made with a checkpoint: it asks the device
// what the lines changed, deletes the checkpoint when nothing did, and
// otherwise saves the configuration the change left and records the undo
// that returns to the checkpoint. A change that failed part way keeps an
// undo with no second checkpoint, since whatever reached the device is
// on it. What the lines did to the VLAN database (watch) is reported with
// the differences; a change to it alone is undone by vlanUndo's lines when
// they are exact, and beside a configuration change makes the return to
// the checkpoint partial, since configure replace leaves the database as
// it is.
func finishCheckpoint(ctx context.Context, rc sdk.RunbookContext, session iosSession, params map[string]any, files checkpointPair, keys bool, watch vlanWatch, applyErr error) (collection.Result, error) {
	const fqcn = configFQCN
	before, after := files.before, files.after
	diffs, diffErr := differences(ctx, session, before, runningConfig)
	var vlans []vlanChange
	var vlanWhy string
	if applyErr == nil && diffErr == nil {
		vlans, vlanWhy, diffErr = watch.changes(ctx, session, managedInDifferences(diffs))
	}
	if applyErr != nil || diffErr != nil {
		if recErr := recordReplace(rc, params, before, "", keys || watch.on, true); recErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, recErr)
		}
		if applyErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
		}
		return collection.Result{}, fmt.Errorf("%s: after the change: %w", fqcn, diffErr)
	}
	if err := rc.SetStat(statDifferences, append(slices.Clone(diffs), maskLines(vlanDifferences(vlans))...)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	var warnings []string
	if watch.on && (vlanWhy != "" || len(diffs) > 0 && len(vlans) > 0) {
		warnings = append(warnings, vlanWarning(vlans, vlanWhy))
	}
	if len(diffs) == 0 {
		if err := deleteFile(ctx, session, before); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if keys {
			// The configuration reads the same, and a key pair changed.
			warnings = append(warnings, keysWarning)
		}
		if err := finishVLANsOnly(rc, params, vlans, keys); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: keys || len(vlans) > 0}, nil
	}
	if err := rc.SetStat(statCheckpoint, before); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := saveRunning(ctx, session, after); err != nil {
		// The change is on the device and its checkpoint is kept: the
		// undo stands, only without the refusal a later change would get.
		if recErr := recordReplace(rc, params, before, "", keys || watch.on, true); recErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, recErr)
		}
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if keys {
		warnings = append(warnings, keysWarning)
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The VLANs the lines changed, or could not be read, are not returned by
	// configure replace.
	partial := keys || len(vlans) > 0 || vlanWhy != ""
	if err := recordReplace(rc, params, before, after, partial, true); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

// finishVLANsOnly records the undo of a change whose configuration reads
// the same: vlanUndo's lines, when the VLAN changes have an exact undo and
// no key pair changed (which nothing returns). Otherwise it records none,
// and the warning says why.
func finishVLANsOnly(rc sdk.RunbookContext, params map[string]any, vlans []vlanChange, keys bool) error {
	if len(vlans) == 0 {
		return nil
	}
	undo, why := vlanUndo(vlans)
	switch {
	case why != "":
		return sdk.RecordWarnings(rc, []string{"no undo was recorded: " + why})
	case keys:
		return sdk.RecordWarnings(rc, []string{"no undo was recorded: a key pair changed beside the VLANs, and no undo returns it"})
	}
	return recordVLANUndo(rc, params, undo)
}

// recordReplace records the undo that returns the device to before,
// refusing if it no longer matches after when after is not empty. It
// reaches the device the way the change did.
func recordReplace(rc sdk.RunbookContext, params map[string]any, before, after string, partial, keepOnFailure bool) error {
	undo := map[string]any{"checkpoint": before}
	if after != "" {
		undo["expect"] = after
	}
	carryTransport(params, undo)
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          replaceFQCN,
		Params:        undo,
		Description:   fmt.Sprintf("Returns the device's configuration to the checkpoint %s, kept before this change.", before),
		KeepOnFailure: keepOnFailure,
		// A key pair the change made or removed is not in the checkpoint,
		// so returning to it puts back the configuration and not the key.
		Partial: partial,
	})
}

// carryTransport copies the change's Telnet opt-in into its undo: a device
// reached only over Telnet has no other way in, and the exposure is the
// one the change's author already accepted for that device. Skipping host
// key verification is never carried (pkg/collection refuses it): an undo
// checks the host key like any task.
func carryTransport(params, undo map[string]any) {
	if sdk.BoolParam(params, sdk.ParamInsecureTelnet) {
		undo[sdk.ParamInsecureTelnet] = true
	}
}

// finishLines ends a change made without a checkpoint: it reads the
// configuration again and writes, masked, the lines that would undo the
// difference, for the task's rollback: list. It records no undo: the
// lines are read off the device, which the journal never holds.
func finishLines(ctx context.Context, rc sdk.RunbookContext, session iosSession, before string, keys bool, watch vlanWatch, applyErr error, warnings []string) (collection.Result, error) {
	const fqcn = configFQCN
	after, err := session.Command(ctx, "show running-config")
	if err != nil {
		if applyErr != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
		}
		return collection.Result{}, fmt.Errorf("%s: reading the configuration after the change: %w", fqcn, err)
	}
	beforeRoots, afterRoots := iosconfig.Parse(before), iosconfig.Parse(after)
	undo, invErr := iosconfig.Inverse(beforeRoots, afterRoots)
	// The VLANs the configuration itself holds are in undo already.
	vlans, vlanWhy, err := watch.changes(ctx, session, func(id int) bool {
		line := fmt.Sprintf("vlan %d", id)
		has := func(n *iosconfig.Node) bool { return n.Line == line }
		return slices.ContainsFunc(beforeRoots, has) || slices.ContainsFunc(afterRoots, has)
	})
	if err != nil {
		return collection.Result{}, err
	}
	changed := invErr != nil || len(undo) > 0 || keys || len(vlans) > 0
	if keys {
		warnings = append(warnings, keysWarning)
	}
	if vlanWhy != "" {
		warnings = append(warnings, vlanWarning(nil, vlanWhy))
	}
	if len(vlans) > 0 && invErr == nil {
		vlanLines, why := vlanUndo(vlans)
		if why != "" {
			invErr = fmt.Errorf("%s", why)
		}
		undo = append(undo, vlanLines...)
	}
	switch {
	case invErr != nil:
		warnings = append(warnings, fmt.Sprintf("no undo was recorded, and none can be written: %v; give the task a rollback: list", invErr))
	case len(undo) > 0:
		if err := rc.SetStat(statUndoLines, maskLines(undo)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		warnings = append(warnings, "no undo was recorded: the lines that undo this change are under undo_lines (masked); copy them into the task's rollback: list")
	}
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if applyErr != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, applyErr)
	}
	return collection.Result{Changed: changed}, nil
}
