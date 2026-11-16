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

// fileMode is a saved source's mode: YANG source is schema text the device
// serves to any account that may read its library, and an author's tools
// (pyang, an editor) read it.
const fileMode fs.FileMode = 0o644

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
	files, err := gather(ctx, src, req)
	if err != nil {
		return fail(fmt.Errorf("over %s: %w", alt.Name, err))
	}

	forms, short := maskForms(rc)
	warnings := src.warnings()
	if short {
		warnings = append(warnings, fmt.Sprintf("a secret value of this run is shorter than %d bytes, which YANG source is not masked for, since such a value cannot be told from a word of the schema: a source holding one is saved as the device sent it", minMaskLength))
	}
	records := make([]any, 0, len(files))
	changed, total := 0, 0
	for i := range files {
		f := &files[i]
		f.data, f.masked = maskKnown(f.data, forms)
		wrote, err := writer.WriteProjectFile(f.path, f.data, fileMode, mode != collection.ModeCheck)
		if err != nil {
			return fail(fmt.Errorf("writing %s: %w", f.path, err))
		}
		if wrote {
			changed++
		}
		total += len(f.data)
		sum := sha256.Sum256(f.data)
		records = append(records, map[string]any{
			"name": f.entry.Name, "revision": f.entry.Revision, "file": f.path, "bytes": len(f.data),
			"sha256": hex.EncodeToString(sum[:]), "submodule": f.submodule, "import_only": f.importOnly,
			"masked": f.masked, "changed": wrote,
		})
	}
	for _, stat := range []struct {
		key   string
		value any
	}{{"via", alt.Name}, {"dest", req.dest}, {"modules", records}, {"files_changed", changed}, {"bytes", total}} {
		if err := rc.SetStat(stat.key, stat.value); err != nil {
			return collection.Result{}, err
		}
	}
	if len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return fail(err)
		}
	}
	return collection.Result{Changed: changed > 0}, nil
}
