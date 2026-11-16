// Package yang: one task of net.yang.schema, from its parameters to the
// files it writes and the stats it records.
package yang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fileMode is a saved source's mode: the owner's alone, as a capture's file
// is (virt.cml.capture.stop), since a source a vendor serves only to accounts
// that may read its library is the vendor's to share, not the project's.
const fileMode fs.FileMode = 0o600

// openers are each alternative's source, by name: the alternative
// collection.ChooseAlternative answers selects which one runs, so what was
// admitted and what runs cannot disagree. A test swaps them.
var openers = map[string]func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (source, error){
	viaNETCONF: openNETCONF,
	viaRESTCONF: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (source, error) {
		return openRESTCONF(ctx, rc, device)
	},
}

// run is one task, a real run or its check.
func run(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if device == nil {
		return fail(errors.New("no target device: set the task's target or the runbook's hosts"))
	}
	req, err := readRequest(params, device)
	if err != nil {
		return fail(err)
	}
	writer, ok := rc.(sdk.ProjectFileWriter)
	if !ok {
		return fail(sdk.ErrNoProjectFiles)
	}
	// The protocol, chosen as validate and admission chose it: the first
	// alternative the device fits, or the one via names.
	alt, err := collection.ChooseAlternative(device, fqcn, manifest().For(params))
	if err != nil {
		if device.HasCapability(capability.NameGNMI) {
			err = fmt.Errorf("%w; gNMI defines no way to fetch a module's source (its Capabilities name the device's models only), so a device served only by gNMI has none to fetch", err)
		}
		return fail(err)
	}
	src, err := openers[alt.Name](ctx, rc, device, params)
	if err != nil {
		return fail(fmt.Errorf("over %s: %w", alt.Name, err))
	}
	defer src.close()
	forms, shorts := maskForms(rc)
	files, err := gather(ctx, src, req, forms, shorts)
	if err != nil {
		return fail(fmt.Errorf("over %s: %w", alt.Name, err))
	}

	// Every file is held to the project writer's rules (no link, no guarded
	// path, a regular file or none) before any is written, so a refusal
	// leaves the project as it was. The same answer is a check's prediction.
	predicted := make([]bool, len(files))
	for i, f := range files {
		if predicted[i], err = writer.WriteProjectFile(f.path, f.data, fileMode, false); err != nil {
			return fail(fmt.Errorf("writing %s: %w, so none of the %d files is written", f.path, err, len(files)))
		}
	}
	written, err := write(writer, files, predicted, mode)
	if statErr := record(rc, alt.Name, req.dest, files[:len(written)], written); statErr != nil {
		return collection.Result{}, statErr
	}
	if err != nil {
		return fail(err)
	}
	if warnings := src.warnings(); len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return fail(err)
		}
	}
	return collection.Result{Changed: countTrue(written) > 0}, nil
}

// write writes each file in a real run and returns whether each changed, a
// check's being its prediction. A write that fails after the dry run passed
// (the disk filled, a file appeared) ends the task with the files before it
// written, which the error says and the stats name.
func write(writer sdk.ProjectFileWriter, files []file, predicted []bool, mode collection.Mode) ([]bool, error) {
	if mode == collection.ModeCheck {
		return predicted, nil
	}
	written := make([]bool, 0, len(files))
	for _, f := range files {
		changed, err := writer.WriteProjectFile(f.path, f.data, fileMode, true)
		if err != nil {
			return written, fmt.Errorf("writing %s: %w, after %d of the %d files were written (the modules stat names them)", f.path, err, len(written), len(files))
		}
		written = append(written, changed)
	}
	return written, nil
}

// record sets the task's stats for files, each with whether it changed.
func record(rc sdk.RunbookContext, via, dest string, files []file, changed []bool) error {
	records := make([]any, 0, len(files))
	total := 0
	for i, f := range files {
		total += len(f.data)
		sum := sha256.Sum256(f.data)
		records = append(records, map[string]any{
			"name": f.entry.Name, "revision": f.entry.Revision, "file": f.path, "bytes": len(f.data),
			"sha256": hex.EncodeToString(sum[:]), "submodule": f.submodule, "import_only": f.importOnly,
			"masked": f.masked, "changed": changed[i],
		})
	}
	for _, stat := range []struct {
		key   string
		value any
	}{{"via", via}, {"dest", dest}, {"modules", records}, {"files_changed", countTrue(changed)}, {"bytes", total}} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return err
		}
	}
	return nil
}

// countTrue counts the true values in flags.
func countTrue(flags []bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}
