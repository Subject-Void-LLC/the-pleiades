package archive

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameter names. src, dest and remove are
// community.general.unarchive's own names; creates is exec.command's own
// name for the identical idiom, reused here rather than invented.
const (
	extractParamSrc     = "src"
	extractParamDest    = "dest"
	extractParamRemove  = "remove"
	extractParamCreates = "creates"
)

const extractStatDest = "dest"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "archive.extract",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePOSIXFileSystem},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: false},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Extracting an archive can create any number of files and directories under dest, and " +
					"this method does not enumerate them as it goes. A safe inverse would have to delete exactly " +
					"what was created and nothing dest already held, which needs that enumeration; without it, " +
					"the only honest choices are refusing to record an inverse or recording one that might " +
					"delete more than this run added. This method refuses, the same call pkg.upgrade makes for " +
					"an installed build a repository may no longer offer.",
			},
			Doc: extractDoc(),
			// A check reads what a real run reads and extracts nothing
			// (checkExtract).
			SupportsCheck: true,
		},
		Invoke: Extract,
		Check:  CheckExtract,
	})
}

func extractDoc() collection.Doc {
	return collection.Doc{
		Summary: "Extracts an archive (tar or tar.gz) on the target.",
		Description: "Extracts src into dest, where src is an archive already present on the target -- this " +
			"is community.general.unarchive with remote_src implied true always; nothing in this platform can " +
			"transfer a file from wherever a runbook runs to the target (file.copy explicitly refuses that " +
			"too), so a src living anywhere else is out of scope. Compression is auto-detected by tar itself, " +
			"so there is no format parameter here the way archive.create has one. Idempotency is opt-in: naming " +
			"creates skips extraction when that path is already there, and naming none means every run " +
			"extracts again, the same honesty exec.command already has for a command with no built-in " +
			"idempotency of its own. A check reads what a real run reads and extracts nothing. A src missing when " +
			"a check runs, or a dest that is there but is not a directory, makes the call unchecked rather than " +
			"failed, since an earlier task in the same run may be what fixes it.",
		Params: []collection.Param{
			{Name: extractParamSrc, Type: "string", Required: true, Description: "The archive on the target to extract. Never a path on the machine running this task."},
			{Name: extractParamDest, Type: "string", Required: true, Description: "The directory to extract into, created if it does not exist."},
			{Name: extractParamRemove, Type: "bool", Default: "false", Description: "Delete src once it has been extracted."},
			{Name: extractParamCreates, Type: "string", Description: "A path whose existence means extraction already happened; when it is already there, this task reports no change and does not extract again. Left unset, every run extracts."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: extractStatDest, Type: "string", Returned: "always", Description: "The directory this task extracted into."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What dest looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that skipped extraction because creates was already there."},
		},
		Examples: []collection.Example{
			{
				Name:        "Extract a build already staged on the target",
				RunbookYAML: "- name: Unpack the release\n  archive.extract:\n    src: /tmp/release.tar.gz\n    dest: /opt/app\n",
			},
			{
				Name:        "Extract only once",
				RunbookYAML: "- name: Unpack the SDK if it is not already there\n  archive.extract:\n    src: /tmp/sdk.tar.gz\n    dest: /opt/sdk\n    creates: /opt/sdk/bin/sdk\n",
			},
		},
		SeeAlso: []string{"archive.create"},
	}
}

// Extract implements "archive.extract".
//
// A run naming creates skips entirely once that path exists, without
// touching dest or src at all. A run naming no creates always extracts,
// which is not a bug to fix later: without opening the archive there is
// nothing honest to compare against, and this method does not open it.
func Extract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return extract(ctx, rc, device, params, collection.ModeExecute)
}

// CheckExtract is "archive.extract"'s check: it reads what Extract reads
// and says whether it would extract, extracting nothing (checkExtract).
func CheckExtract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return extract(ctx, rc, device, params, collection.ModeCheck)
}

// extract is Extract's and CheckExtract's one body; mode says which.
func extract(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "archive.extract"

	src, err := sdk.RequiredStringParam(params, extractParamSrc)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	dest, err := sdk.RequiredStringParam(params, extractParamDest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	remove, err := sdk.BoolParamOr(params, extractParamRemove, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %s: %w", fqcn, extractParamRemove, err)
	}
	creates := sdk.StringParam(params, extractParamCreates)

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if creates != "" {
		markerInfo, err := remotefile.Stat(ctx, conn, creates)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if markerInfo.Exists() {
			before, err := remotefile.Stat(ctx, conn, dest)
			if err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			if err := recordExtractState(rc, dest, before, before); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			return collection.Result{Changed: false}, nil
		}
	}

	before, err := remotefile.Stat(ctx, conn, dest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if mode == collection.ModeCheck {
		return checkExtract(ctx, rc, conn, src, dest, before)
	}

	if err := remotefile.MakeDirectory(ctx, conn, dest, true); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := runArchiveCmd(ctx, conn, tarExtractArgs(src, dest)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if remove {
		if err := removePaths(ctx, conn, src); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	after, err := remotefile.Stat(ctx, conn, dest)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	if err := recordExtractState(rc, dest, before, after); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: true}, nil
}

// checkExtract is a check's answer for an extraction a creates guard did
// not settle: a real run always extracts then, so the check predicts a
// change. It reads what tar and mkdir -p need first, src and a dest that
// is absent or a directory, and anything else makes the call unchecked
// (needExisting). The prediction says only that dest would be a
// directory: what tar writes into it, and the mode and owner a new one
// gets, are for running it to decide.
func checkExtract(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, src, dest string, before remotefile.Info) (collection.Result, error) {
	const fqcn = "archive.extract"
	if before.Exists() {
		if err := needExisting(ctx, conn, dest, "dest", remotefile.KindDirectory); err != nil {
			return collection.Result{}, err
		}
	}
	if err := needExisting(ctx, conn, src, "src", ""); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(extractStatDest, dest); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	after := remotefile.PredictCreate(remotefile.KindDirectory, remotefile.Attributes{}).Map()
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

func recordExtractState(rc sdk.RunbookContext, dest string, before, after remotefile.Info) error {
	if err := rc.SetStat(extractStatDest, dest); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
