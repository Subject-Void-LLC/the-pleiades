package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "fs.mount",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameLinux},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			// A check reads findmnt and fstab and changes neither.
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that mounted an absent path emits an fs.unmount naming it, which also strips the " +
					"fstab entry this run added, if any. A run that found the path already mounted and only added " +
					"or updated its fstab entry emits no inverse: undoing that without also unmounting a mount " +
					"this task did not create needs a capability this namespace does not expose. A run that found " +
					"everything already as requested emits nothing.",
			},
			Doc: mountDoc(),
		},
		Invoke: Mount,
		Check:  CheckMount,
	})
}

func mountDoc() collection.Doc {
	return collection.Doc{
		Summary: "Mounts a filesystem on the target, and optionally persists it to fstab.",
		Description: "Makes sure path is mounted from src, creating the mount if it is not already there. This " +
			"is close to ansible.builtin.mount with state=mounted, split so that persisting to fstab (see the " +
			"persist parameter) is independent of mounting: either can be true without the other. Mount state " +
			"is read from findmnt before anything is sent, so a path already mounted from src with a matching " +
			"fstype reports no change; a path already mounted from a different src or fstype is refused rather " +
			"than silently remounted, since that is not something this method can do without first unmounting " +
			"it. opts is compared only when the task actually names it: a path already mounted with different " +
			"options than an unspecified opts is left alone rather than treated as drift. " +
			"A check reads findmnt and fstab, mounts nothing and writes nothing, and leaves a new mount's options " +
			"out of its prediction, since the kernel rewrites them.",
		Params: []collection.Param{
			{Name: paramPath, Type: "string", Required: true, Description: "The mountpoint to mount onto."},
			{Name: paramSrc, Type: "string", Required: true, Description: "The device, share or filesystem source to mount."},
			{Name: paramFSType, Type: "string", Required: true, Description: "The filesystem type, as mount's own -t takes it (e.g. ext4, nfs, xfs)."},
			{Name: paramOpts, Type: "string", Description: "Mount options, as mount's own -o takes them. Defaults to \"defaults\" for a fresh mount and a fresh fstab entry; an existing mount or fstab entry is only compared or rewritten against this when the task actually sets it."},
			{Name: paramPersist, Type: "bool", Default: "true", Description: "Also make sure path has a matching entry in fstab, so it mounts again on the next boot."},
			{Name: paramFstab, Type: "string", Default: "/etc/fstab", Description: "The fstab-format file to read and, if persist is true, write."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: statPath, Type: "string", Returned: "always", Description: "The mountpoint this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Mount and persist a data volume",
				RunbookYAML: "- name: Mount the data volume\n  fs.mount:\n    path: /data\n    src: /dev/sdb1\n    fstype: ext4\n",
			},
			{
				Name:        "Mount without touching fstab",
				RunbookYAML: "- name: Mount a scratch volume for this run only\n  fs.mount:\n    path: /mnt/scratch\n    src: /dev/sdb2\n    fstype: ext4\n    persist: false\n",
			},
		},
		SeeAlso: []string{"fs.unmount"},
	}
}

// Mount implements "fs.mount".
//
// Mounting and fstab persistence are decided independently: a path
// already mounted by hand can gain an fstab entry with no mount command
// sent, and a path already persisted can be mounted with no fstab write
// sent. Each is recorded and inverted on its own terms in the section
// below.
func Mount(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return mount(ctx, rc, device, params, collection.ModeExecute)
}

// CheckMount is "fs.mount"'s check: it reads the mountpoint and fstab and
// says whether Mount would mount the path or change its fstab entry, sending no mount and writing no fstab.
func CheckMount(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return mount(ctx, rc, device, params, collection.ModeCheck)
}

// mount is Mount's and CheckMount's one body; mode says which.
func mount(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "fs.mount"

	path, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	src, err := sdk.RequiredStringParam(params, paramSrc)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	fstype, err := sdk.RequiredStringParam(params, paramFSType)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	opts := sdk.StringParam(params, paramOpts)
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
		if before.source != src || before.fstype != fstype {
			return collection.Result{}, fmt.Errorf("%s: %s is already mounted from %s (%s), not %s (%s); unmount it first or point this task at the existing source and fstype",
				fqcn, path, before.source, before.fstype, src, fstype)
		}
		if opts != "" && before.options != opts {
			return collection.Result{}, fmt.Errorf("%s: %s is already mounted with options %q, not %q; unmount it first or point this task at the existing options",
				fqcn, path, before.options, opts)
		}
	} else if mode == collection.ModeCheck {
		mountChanged = true
	} else {
		mountOpts := opts
		if mountOpts == "" {
			mountOpts = defaultOpts
		}
		if err := runMount(ctx, conn, src, path, fstype, mountOpts); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		mountChanged = true
	}

	var fstabChanged bool
	if persist {
		entryOpts := opts
		if entryOpts == "" {
			entryOpts = defaultOpts
			if before.persisted {
				if fields := strings.Fields(before.fstabLine); len(fields) >= 4 {
					entryOpts = fields[3]
				}
			}
		}
		entry := &fstabEntry{source: src, mountpoint: path, fstype: fstype, options: entryOpts}
		if mode == collection.ModeCheck {
			fstabChanged, err = fstabWouldChange(ctx, conn, fstabPath, path, entry)
		} else {
			fstabChanged, err = syncFstab(ctx, conn, fstabPath, path, entry)
		}
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	changed := mountChanged || fstabChanged

	if mode == collection.ModeCheck {
		// A new mount's options are left out: the kernel rewrites them
		// (defaults reads back as rw,relatime), and only mounting says how.
		predicted := before.Map()
		if mountChanged {
			predicted["mounted"], predicted["source"], predicted["fstype"] = true, src, fstype
			delete(predicted, "options")
		}
		if persist {
			predicted["persisted"] = true
		}
		return predictState(rc, fqcn, path, before, changed, predicted)
	}

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
			FQCN: "fs.unmount",
			Params: map[string]any{
				paramPath:    path,
				paramPersist: fstabChanged,
				paramFstab:   fstabPath,
			},
			Description: fmt.Sprintf("Unmount %s, which this task mounted. %s", path,
				fstabInverseNote(fstabChanged)),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

// fstabInverseNote fills in fs.unmount's own recorded inverse
// description depending on whether this run also touched fstab, so the
// two halves of the sentence stay consistent with what persist actually
// carries in Params above.
func fstabInverseNote(alsoRemovesFstabEntry bool) string {
	if alsoRemovesFstabEntry {
		return "This also removes the fstab entry this task added."
	}
	return "This leaves fstab untouched, since this task did not change it."
}
