package fs

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "fs.unmount",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameLinux},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that unmounted a mounted path emits an fs.mount pinned to the exact src, fstype " +
					"and opts this run captured before unmounting, which is a real, restorable inverse. If this " +
					"run also removed the path's fstab entry, the inverse asks fs.mount to persist it again. A " +
					"run that found the path already unmounted and only removed a stale fstab entry emits no " +
					"inverse: undoing that without also mounting something this task did not mount needs a " +
					"capability this namespace does not expose. A run that found everything already absent " +
					"emits nothing.",
			},
			Doc: unmountDoc(),
		},
		Invoke: Unmount,
	})
}

func unmountDoc() collection.Doc {
	return collection.Doc{
		Summary: "Unmounts a filesystem on the target, and optionally removes it from fstab.",
		Description: "Makes sure path is not mounted, unmounting it if it is. This is close to " +
			"ansible.builtin.mount with state=unmounted, split so that removing the fstab entry (see the " +
			"persist parameter) is independent of unmounting: either can happen without the other. Mount " +
			"state is read from findmnt before anything is sent, so a path already unmounted reports no change " +
			"from the unmount itself.",
		Params: []collection.Param{
			{Name: paramPath, Type: "string", Required: true, Description: "The mountpoint to unmount."},
			{Name: paramPersist, Type: "bool", Default: "true", Description: "Also remove any matching entry from fstab, so it does not mount again on the next boot."},
			{Name: paramFstab, Type: "string", Default: "/etc/fstab", Description: "The fstab-format file to read and, if persist is true, write."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statPath, Type: "string", Returned: "always", Description: "The mountpoint this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. After always reports mounted: false on a successful unmount."},
		},
		Examples: []collection.Example{
			{
				Name:        "Unmount and forget a volume",
				RunbookYAML: "- name: Unmount the old data volume\n  fqcn: fs.unmount\n  params:\n    path: /data\n",
			},
			{
				Name:        "Unmount but leave the fstab entry",
				RunbookYAML: "- name: Unmount temporarily for maintenance\n  fqcn: fs.unmount\n  params:\n    path: /data\n    persist: false\n",
			},
		},
		SeeAlso: []string{"fs.mount"},
	}
}

// Unmount implements "fs.unmount".
//
// Unmounting and fstab removal are decided independently, the same way
// Mount decides mounting and fstab persistence independently: see this
// file's own inverse construction below for exactly what each half can
// and cannot undo.
func Unmount(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "fs.unmount"

	path, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	persist, err := sdk.BoolParamOr(params, paramPersist, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, paramPersist, err)
	}
	fstabPath := fstabParam(params)

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := queryState(ctx, conn, fstabPath, path)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	var mountChanged bool
	if before.mounted {
		if err := runUnmount(ctx, conn, path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		mountChanged = true
	}

	var fstabChanged bool
	if persist {
		if fstabChanged, err = syncFstab(ctx, conn, fstabPath, path, nil); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	changed := mountChanged || fstabChanged

	after := before
	if changed {
		if after, err = queryState(ctx, conn, fstabPath, path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := recordState(rc, path, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mountChanged {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN: "fs.mount",
			Params: map[string]any{
				paramPath:    path,
				paramSrc:     before.source,
				paramFSType:  before.fstype,
				paramOpts:    before.options,
				paramPersist: fstabChanged,
				paramFstab:   fstabPath,
			},
			Description: fmt.Sprintf("Remount %s from %s (%s) with its previous options. %s", path, before.source, before.fstype,
				fstabRestoreNote(fstabChanged)),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

// fstabRestoreNote mirrors mount.go's fstabInverseNote for the opposite
// direction: whether this run's inverse should ask fs.mount to persist
// the entry again.
func fstabRestoreNote(alsoRestoresFstabEntry bool) string {
	if alsoRestoresFstabEntry {
		return "This also restores the fstab entry this task removed."
	}
	return "This leaves fstab untouched, since this task did not change it."
}
