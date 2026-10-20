// What file.json and file.yaml share: one value at a key path in a
// document on the device, set or removed, the rest of the document kept
// (pkg/docedit), written through remotefile.Replace so the file keeps its
// owner, group and mode.
package file

import (
	"context"
	"fmt"
	"path"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/docedit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The parameters both methods take.
const (
	structuredParamPath   = "path"
	structuredParamKey    = "key"
	structuredParamValue  = "value"
	structuredParamState  = "state"
	structuredParamCreate = "create"
)

// createdMode is the mode a file either method creates is given.
const createdMode = "0644"

// structuredFormat is one document format: how its value at a path is
// read for a comparison and a diff, and how an edit is made.
type structuredFormat interface {
	// edit applies the change to data (nil for a file being created) and
	// returns the new document, the value at the path before and after as
	// text ("" for none), and whether it changed.
	edit(data []byte, keyPath []docedit.Segment, value any, present bool) (out []byte, before, after string, changed bool, err error)
}

// structuredRequest is one call's parameters.
type structuredRequest struct {
	path    string
	keyPath []docedit.Segment
	value   any
	present bool
	create  bool
}

// structuredManifest is what both methods declare.
func structuredManifest(doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{"ssh"},
		RequiredCapabilities: []capability.Name{capability.NamePOSIXFileSystem},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		SupportsCheck:        true,
		Reversibility: collection.Reversibility{
			Reversible: true,
			Notes: "A run that changed an existing file emits a file.copy writing back the whole text it held, with its mode, owner and group; " +
				"that text is withheld from the journal, so a rollback replays it only from the task's own rollback: list. A run that created " +
				"the file emits a file.remove. A run that changed nothing emits nothing. The modification time is not restored.",
			Inverses: []sdk.InverseSpec{
				{FQCN: "file.copy", Record: []string{"dest", "mode", "owner", "group"}, Withhold: []string{"content"}},
				{FQCN: "file.remove", Record: []string{"path"}},
			},
		},
		Doc: doc,
	}
}

// structuredParse reads a call's parameters.
func structuredParse(params map[string]any) (structuredRequest, error) {
	var req structuredRequest
	var err error
	if req.path, err = sdk.RequiredStringParam(params, structuredParamPath); err != nil {
		return req, err
	}
	if !path.IsAbs(req.path) || path.Clean(req.path) != req.path {
		return req, fmt.Errorf("%s %q must be an absolute, clean path", structuredParamPath, req.path)
	}
	if req.keyPath, err = docedit.ParsePath(params[structuredParamKey]); err != nil {
		return req, fmt.Errorf("%s: %w", structuredParamKey, err)
	}
	switch state := sdk.StringParam(params, structuredParamState); state {
	case "", "present":
		req.present = true
		v, set := params[structuredParamValue]
		if !set {
			return req, fmt.Errorf("%s is required when state is present (null sets a null)", structuredParamValue)
		}
		req.value = v
	case "absent":
		if _, set := params[structuredParamValue]; set {
			return req, fmt.Errorf("%s goes with state present, not absent", structuredParamValue)
		}
	default:
		return req, fmt.Errorf("%s %q is not present or absent", structuredParamState, state)
	}
	if req.create, err = sdk.BoolParamOr(params, structuredParamCreate, false); err != nil {
		return req, err
	}
	return req, nil
}

// structuredRun reads the document, makes the edit, and, unless checking
// or nothing changed, writes it back and records its undo.
func structuredRun(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, format structuredFormat, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	req, err := structuredParse(params)
	if err != nil {
		return fail(err)
	}
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = conn.Close() }()
	info, err := remotefile.Stat(ctx, conn, req.path)
	if err != nil {
		return fail(err)
	}
	var data []byte
	switch {
	case info.Exists():
		if data, info, err = remotefile.ReadText(ctx, conn, req.path, docedit.MaxBytes); err != nil {
			return fail(err)
		}
	case !req.create:
		return fail(fmt.Errorf("%s does not exist on the device; set %s to start it from an empty document", req.path, structuredParamCreate))
	}
	out, before, after, changed, err := format.edit(data, req.keyPath, req.value, req.present)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", req.path, err))
	}
	key := docedit.PathText(req.keyPath)
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{key: before}, After: map[string]any{key: after}}); err != nil {
		return fail(err)
	}
	stats := map[string]any{"path": req.path, "key": key, "created": changed && !info.Exists()}
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	if check || !changed {
		return collection.Result{Changed: changed}, nil
	}
	if !info.Exists() {
		if err := remotefile.Write(ctx, conn, req.path, out); err != nil {
			return fail(err)
		}
		// Write's temporary is 0600, so a file it leaves is the login's alone;
		// a configuration file is created readable, as a new file usually is.
		created, err := remotefile.Stat(ctx, conn, req.path)
		if err != nil {
			return fail(err)
		}
		if _, err := remotefile.Apply(ctx, conn, req.path, remotefile.Attributes{Mode: createdMode}, created); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: true}, sdk.RecordInverse(rc, sdk.Inverse{
			FQCN: "file.remove", Params: map[string]any{"path": req.path},
			Description: "Remove " + req.path + ", which this run created.",
		})
	}
	if _, err := remotefile.Replace(ctx, conn, req.path, out, info); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, sdk.RecordInverse(rc, sdk.Inverse{
		FQCN: "file.copy",
		Params: map[string]any{
			"dest": req.path, "content": string(data), "mode": info.Mode, "owner": info.Owner, "group": info.Group,
		},
		Description: fmt.Sprintf("Write %s back to the %d bytes it held before this task set %s, owned by %s:%s with mode %s.",
			req.path, len(data), key, info.Owner, info.Group, info.Mode),
	})
}

// structuredParams documents the parameters both methods take; what
// names the format.
func structuredParams(what string) []collection.Param {
	return []collection.Param{
		{Name: structuredParamPath, Type: "string", Required: true, Description: "The " + what + " file on the device, an absolute path."},
		{Name: structuredParamKey, Type: "scalar or list", Required: true, Description: "The key path: dotted (server.port, items.0.name, a whole number indexing a list), or a list of segments ([server, \"a.b\", 0]) for a key holding a dot. A missing key is created, with a mapping for each missing step; an index one past a list's end appends."},
		{Name: structuredParamValue, Type: "any", Description: "The value to set, any YAML value (a mapping, a list, a scalar, null). Required with state present."},
		{Name: structuredParamState, Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present sets the value; absent removes the key, which is no change when it is not there."},
		{Name: structuredParamCreate, Type: "bool", Default: "false", Description: "Start from an empty document when the file does not exist; without it a missing file is refused. A file it creates is mode 0644, owned by the account the device is reached as (file.attrs changes either)."},
		{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
	}
}

// structuredReturns documents what both methods return.
func structuredReturns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: "path", Type: "string", Returned: "always", Description: "The file."},
		{Name: "key", Type: "string", Returned: "always", Description: "The key path, dotted."},
		{Name: "created", Type: "bool", Returned: "always", Description: "Whether the run created the file (or, in a check, would)."},
		{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The key's value before and after, as the format writes it; empty where there is none. Masked as every diff is."},
	}
}
