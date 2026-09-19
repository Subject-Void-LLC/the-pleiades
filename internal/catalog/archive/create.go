package archive

import (
	"context"
	"fmt"
	pathpkg "path"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. path, src and remove are community.general.archive's
// own names; format is too, though that module spells the compressed
// choice "gz" where this spells it "tar.gz" (see archive.go's own doc
// comment).
const (
	createParamPath   = "path"
	createParamSrc    = "src"
	createParamFormat = "format"
	createParamRemove = "remove"
)

const createStatPath = "path"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "archive.create",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePOSIXFileSystem},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that created an absent archive emits a file.remove naming it, which is a real, " +
					"restorable inverse for the archive itself. A run that found the archive already present " +
					"emits nothing. What no inverse here can restore is whatever remove=true deleted once the " +
					"archive was written: those source paths are gone, and the archive holding their bytes is " +
					"exactly what the inverse would just have deleted.",
			},
			Doc: createDoc(),
			// A check reads what a real run reads and runs no tar (checkCreate).
			SupportsCheck: true,
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary: "Creates an archive (tar or tar.gz) from files on the target.",
		Description: "Creates path as a tar archive of src, entirely from files already on the target -- this " +
			"is community.general.archive without a zip option. Idempotency here is existence-only: a run " +
			"finding path already there reports no change and reads none of src, the same way file.copy's " +
			"checksum comparison decides on bytes rather than a name but simpler still, since this does not " +
			"even open the archive to compare. remove, when true, deletes src once the archive has been " +
			"written; the archive itself is not touched a second time to verify it. A check reads what a real run " +
			"reads and runs no tar. A src, or the directory the archive would go in, that is missing when a check " +
			"runs makes the call unchecked rather than failed, since an earlier task in the same run may be what " +
			"creates it.",
		Params: []collection.Param{
			{Name: createParamPath, Type: "string", Required: true, Description: "The archive file to create."},
			{Name: createParamSrc, Type: "list", Required: true, Description: "The paths on the target to include, at least one."},
			{Name: createParamFormat, Type: "string", Default: "tar.gz", Choices: []string{"tar", "tar.gz"}, Description: "The archive format. tgz is accepted as a synonym for tar.gz."},
			{Name: createParamRemove, Type: "bool", Default: "false", Description: "Delete src once the archive has been written."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: createStatPath, Type: "string", Returned: "always", Description: "The archive this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What path looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Archive a directory",
				RunbookYAML: "- name: Archive the release build\n  archive.create:\n    path: /tmp/release.tar.gz\n    src:\n      - /opt/app/dist\n",
			},
			{
				Name:        "Archive and remove the originals",
				RunbookYAML: "- name: Archive old logs and delete them\n  archive.create:\n    path: /var/backups/logs-2026-08.tar.gz\n    src:\n      - /var/log/app/2026-08\n    remove: true\n",
			},
		},
		SeeAlso: []string{"archive.extract", "file.remove"},
	}
}

// Create implements "archive.create".
//
// An archive already at path is left alone and the task reports no
// change, without reading src at all: this is existence-only
// idempotency, not a content comparison.
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is "archive.create"'s check: it reads the archive's path
// and says whether Create would write it, running nothing (checkCreate).
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return create(ctx, rc, device, params, collection.ModeCheck)
}

// create is Create's and CheckCreate's one body; mode says which.
func create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "archive.create"

	path, err := sdk.RequiredStringParam(params, createParamPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	src, present, err := sdk.StringSlice(params, createParamSrc)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, createParamSrc, err)
	}
	if !present || len(src) == 0 {
		return collection.Result{}, fmt.Errorf("%s: %s is required and must name at least one path", fqcn, createParamSrc)
	}
	format, err := normalizeFormat(sdk.StringParam(params, createParamFormat))
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	remove, err := sdk.BoolParamOr(params, createParamRemove, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, createParamRemove, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := remotefile.Stat(ctx, conn, path)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		return checkCreate(ctx, rc, conn, path, src, before)
	}

	changed := false
	after := before
	if !before.Exists() {
		if err := runArchiveCmd(ctx, conn, tarCreateArgs(path, src, format)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if remove {
			if err := removePaths(ctx, conn, src...); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
		changed = true
		if after, err = remotefile.Stat(ctx, conn, path); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := rc.SetStat(createStatPath, path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if changed {
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:   "file.remove",
			Params: map[string]any{"path": path},
			Description: fmt.Sprintf("Remove %s, which this task created. Anything remove=true deleted to "+
				"build it is not restored.", path),
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

// checkCreate is a check's answer for an archive path found as before.
// An archive already there is left alone, as a real run leaves it. An
// absent one would be written, and the prediction says only that a file
// would exist: its size, mode and owner are tar's and the device's to
// decide.
//
// Before predicting a write it reads what tar needs: every src path and
// the directory that would hold the archive. One that is missing now
// makes the call unchecked (collection.CannotCheck) rather than failed or
// predicted, since an earlier task in the same run may be what creates
// it, and a check cannot tell.
func checkCreate(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, path string, src []string, before remotefile.Info) (collection.Result, error) {
	const fqcn = "archive.create"
	changed := !before.Exists()
	after := before.Map()
	if changed {
		if err := needExisting(ctx, conn, pathpkg.Dir(path), "the directory that would hold the archive,", remotefile.KindDirectory); err != nil {
			return collection.Result{}, err
		}
		for _, p := range src {
			if err := needExisting(ctx, conn, p, "src", ""); err != nil {
				return collection.Result{}, err
			}
		}
		after = remotefile.PredictCreate(remotefile.KindFile, remotefile.Attributes{}).Map()
	}
	if err := rc.SetStat(createStatPath, path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}
