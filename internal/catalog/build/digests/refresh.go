// build.digests.refresh: each reference rewritten to the digest its tag
// names now, or to exactly what set says.
package digests

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/imagepin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRefresh,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo writes back each reference it changed, as it was, from a record it keeps on the device before writing anything (the journal holds only the record's id); a file edited since is refused rather than overwritten.",
				Inverses:   []sdk.InverseSpec{{FQCN: fqcnRefresh, Record: []string{paramUndo}}},
			},
			Doc: refreshDoc(),
		},
		Invoke: Refresh,
		Check:  CheckRefresh,
	})
}

// change is one reference rewritten.
type change struct {
	file     *file
	ref      imagepin.Ref
	from, to string
}

// Refresh implements build.digests.refresh.
func Refresh(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return refresh(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRefresh is build.digests.refresh's check: every reference resolved
// exactly as a run resolves it, and each change it would make reported,
// with nothing written.
func CheckRefresh(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return refresh(ctx, rc, device, params, collection.ModeCheck)
}

// refresh is Refresh's and CheckRefresh's one body.
func refresh(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	undoID := sdk.StringParam(params, paramUndo)
	paths, err := pathList(params, paramFiles, undoID == "")
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	sets, err := setParam(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	if undoID != "" && sets != nil {
		return collection.Result{}, fmt.Errorf("%s: %s and %s cannot be combined", fqcnRefresh, paramUndo, paramSet)
	}
	conn, err := sdk.Connect(ctx, rc, device, params, fqcnRefresh)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	var replayed string
	if undoID != "" {
		if sets, replayed, err = loadUndo(ctx, conn, undoID); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
		}
		if len(paths) == 0 {
			paths = filesOf(sets)
		}
	}
	files, err := scan(ctx, conn, paths)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}

	var changes []change
	if sets != nil {
		changes, err = setChanges(files, sets)
	} else {
		changes, err = resolvedChanges(ctx, rc, device, params, files)
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	if err := rc.SetStat(statChanges, changeEntries(changes)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	if mode == collection.ModeCheck || len(changes) == 0 {
		return collection.Result{Changed: len(changes) > 0}, nil
	}
	// The undo is kept before anything changes, so a run that changed a
	// file always has one.
	id, record, err := saveUndo(ctx, conn, changes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: nothing was written: %w", fqcnRefresh, err)
	}
	inPlace, err := writeChanges(ctx, conn, changes)
	if err != nil {
		dropUndo(ctx, conn, record)
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	if len(inPlace) > 0 {
		warnings := make([]string, len(inPlace))
		for i, p := range inPlace {
			warnings[i] = p + " was written in place rather than replaced, since a new file could not be given its owner and group by this login; a reader at that instant could have seen it part written"
		}
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
		}
	}
	if err := recordUndo(rc, id, len(changes)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRefresh, err)
	}
	if replayed != "" {
		dropUndo(ctx, conn, replayed)
	}
	return collection.Result{Changed: true}, nil
}

// resolvedChanges resolves every reference's tag before anything is
// written, so a registry refusing one leaves every file as it was.
func resolvedChanges(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, files []*file) ([]change, error) {
	res, err := openResolver(ctx, rc, device, params, fqcnRefresh)
	if err != nil {
		return nil, err
	}
	defer res.session.Close()
	var changes []change
	for _, f := range files {
		for _, ref := range f.refs {
			if ref.Unresolvable != "" {
				return nil, fmt.Errorf("%s:%d %s cannot be refreshed: %s", f.path, ref.Line, ref.Text, ref.Unresolvable)
			}
			pin, err := imagepin.Parse(ref.Text)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", f.path, ref.Line, err)
			}
			if pin.Tag == "" {
				return nil, fmt.Errorf("%s:%d %s has no tag, so nothing says what to refresh it to", f.path, ref.Line, ref.Text)
			}
			current, err := res.digest(ctx, pin)
			if err != nil {
				return nil, fmt.Errorf("%s:%d %s: %w", f.path, ref.Line, ref.Text, err)
			}
			if pin.Digest != current {
				pin.Digest = current
				changes = append(changes, change{file: f, ref: ref, from: ref.Text, to: pin.Format()})
			}
		}
	}
	return changes, nil
}

// writeChanges rewrites each file once, refusing one edited since it was
// read, and keeping its mode and owner. A failure puts back every file
// already rewritten, since a failed task's undo is not journaled.
func writeChanges(ctx context.Context, conn *remoteexec.Conn, changes []change) ([]string, error) {
	byFile := map[*file][]imagepin.Edit{}
	var order []*file
	for _, c := range changes {
		if _, seen := byFile[c.file]; !seen {
			order = append(order, c.file)
		}
		byFile[c.file] = append(byFile[c.file], imagepin.Edit{Ref: c.ref, Text: c.to})
	}
	sort.Slice(order, func(i, j int) bool { return order[i].path < order[j].path })
	var written []*file
	var inPlace []string
	for _, f := range order {
		next, err := imagepin.Rewrite(f.content, byFile[f])
		var r remotefile.Replaced
		if err == nil {
			r, err = replace(ctx, conn, f, next)
		}
		// A file whose bytes may have moved is put back even when the
		// write then failed.
		if r.Touched {
			written = append(written, f)
		}
		if err != nil {
			return nil, fmt.Errorf("%w%s", err, putBack(ctx, conn, written))
		}
		if r.InPlace {
			inPlace = append(inPlace, f.path)
		}
	}
	return inPlace, nil
}

// replace writes content over f, only if f still holds what was read,
// keeping its mode, owner and group (remotefile.Replace).
func replace(ctx context.Context, conn *remoteexec.Conn, f *file, content []byte) (remotefile.Replaced, error) {
	sum, exists, err := remotefile.Checksum(ctx, conn, f.path)
	if err != nil {
		return remotefile.Replaced{}, err
	}
	if !exists || sum != remotefile.ChecksumOf(f.content) {
		return remotefile.Replaced{}, fmt.Errorf("%s changed on the device after it was read, so it was not overwritten", f.path)
	}
	return remotefile.Replace(ctx, conn, f.path, content, f.info)
}

// putBack restores files already rewritten to what they held, saying
// which could not be.
func putBack(ctx context.Context, conn *remoteexec.Conn, written []*file) string {
	var failed []string
	for _, f := range written {
		if _, err := remotefile.Replace(ctx, conn, f.path, f.content, f.info); err != nil {
			failed = append(failed, f.path)
		}
	}
	switch {
	case len(written) == 0:
		return ""
	case len(failed) > 0:
		return fmt.Sprintf("; the files it had rewritten were put back, except %v", failed)
	}
	return "; the files it had rewritten were put back"
}

// recordUndo records the refresh that writes every changed reference back
// as it was, through the record saved on the device.
func recordUndo(rc sdk.RunbookContext, id string, changed int) error {
	return sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        fqcnRefresh,
		Params:      map[string]any{paramUndo: id},
		Description: fmt.Sprintf("Write back the %d image reference(s) this task refreshed, as they were, from undo record %s on the device.", changed, id),
	})
}

// changeEntries is changes as a return.
func changeEntries(changes []change) []any {
	out := make([]any, len(changes))
	for i, c := range changes {
		out[i] = map[string]any{"file": c.file.path, "line": c.ref.Line, "from": c.from, "to": c.to}
	}
	return out
}
