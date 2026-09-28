// Package main: the example.note.write method this example program provides.
package main

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// noteFQCN is the one method this program provides. Its namespace,
// "example", is not one this repository uses, which matters: two programs,
// or a program and a built-in, claiming the same name are refused at load
// rather than one silently winning.
const noteFQCN = "example.note.write"

// The method's parameter and stat names.
const (
	noteParamPath    = "path"
	noteParamContent = "content"
	noteStatPath     = "path"
)

// noteWriteDescriptor is example.note.write's registration: its manifest,
// its Invoke, and its Check. It is exactly the value a built-in method
// passes to collection.MustRegister; external.Main validates it through
// the same collection.Register before answering anything.
func noteWriteDescriptor() collection.Descriptor {
	return collection.Descriptor{
		Name: noteFQCN,
		Manifest: collection.Manifest{
			SupportedTransports: []string{"ssh"},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "This example keeps no copy of the file's previous content, so it cannot put it back. " +
					"A real method would read the old content before writing and record it with sdk.RecordInverse.",
			},
			// Check support is declared here and backed by CheckWrite below.
			// collection.Register refuses one without the other.
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes sure a file on the target holds exactly the given text.",
				Description: "Reads the file first and writes only when its content differs, so a converged run reports no change. In check mode it reads and compares and writes nothing.",
				Params: []collection.Param{
					{Name: noteParamPath, Type: "string", Required: true, Description: "The file to write."},
					{Name: noteParamContent, Type: "string", Required: true, Description: "The exact text the file should hold."},
					{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task."},
				},
				Returns: []collection.ReturnField{
					{Name: noteStatPath, Type: "string", Returned: "always", Description: "The file this task acted on."},
					{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "Whether the file existed and what it held, before and after. In check mode the after half is what a real run would leave."},
				},
			},
		},
		Invoke: Write,
		Check:  CheckWrite,
	}
}

// Write implements example.note.write: it makes the file hold the text the
// task asked for, writing only when it does not already.
func Write(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return writeNote(ctx, rc, device, params, false)
}

// CheckWrite is Write's check-mode counterpart: the same read and the same
// comparison, and no write. It never records an inverse, since it changed
// nothing and there is nothing to undo.
func CheckWrite(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return writeNote(ctx, rc, device, params, true)
}

// writeNote is the one code path Write and CheckWrite share, differing
// only in whether the write happens. Sharing it is what keeps a check's
// prediction honest: the check reaches its answer through exactly the
// read and comparison a real run would make.
func writeNote(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	path, err := sdk.RequiredStringParam(params, noteParamPath)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
	}
	content, err := sdk.RequiredStringParam(params, noteParamContent)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
	}

	// The credential arrives through rc, which The Pleiades filled from the
	// request on this program's stdin: the one its credential manager
	// resolved for this task, exactly as a built-in method would receive it.
	conn, err := sdk.Connect(ctx, rc, device, params, noteFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	current, exists, err := remotefile.Read(ctx, conn, path)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
	}
	changed := !exists || current != content

	if changed && !check {
		if err := remotefile.Write(ctx, conn, path, []byte(content)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
		}
	}

	if err := rc.SetStat(noteStatPath, path); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
	}
	// After is what the file holds once a real run is done: the requested
	// text whenever anything changed (written in a real run, predicted in
	// a check), and the text it already had otherwise.
	after := map[string]any{"exists": exists, "content": current}
	if changed {
		after = map[string]any{"exists": true, "content": content}
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{
		Before: map[string]any{"exists": exists, "content": current},
		After:  after,
	}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", noteFQCN, err)
	}

	return collection.Result{Changed: changed}, nil
}
