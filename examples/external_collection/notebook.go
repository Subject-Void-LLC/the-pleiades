// Package main: a capability this program defines, example.notebook, its
// probe, and a method that requires it.
package main

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The capability, its probe and the method that needs it. The capability
// is data: The Pleiades has no compiled notion of a notebook, and needs
// none, because this program says what one is and how to find it.
const (
	notebookCapability capability.Name = "example.notebook"
	notebookProbeFQCN                  = "example.notebook.probe"
	notebookWriteFQCN                  = "example.notebook.write"
	// notebookDirProperty is the grant's one property: the directory.
	notebookDirProperty = "dir"
)

// noteName is what a note's file name may be: a name, never a path.
var noteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// notebookDefinition is example.notebook: a device where the connecting
// account keeps notes in a directory of its own, found by the probe.
func notebookDefinition() capability.DataDefinition {
	return capability.DataDefinition{
		Name:     notebookCapability,
		Doc:      "The connecting account keeps notes in a directory of its own, ~/notes, which it may write.",
		Requires: []capability.Name{capability.NamePOSIXFileSystem},
		Properties: []capability.DataProperty{
			{Name: notebookDirProperty, Type: "string", Required: true, Description: "The notes directory, as an absolute path."},
		},
		Probe: notebookProbeFQCN,
	}
}

// notebookProbeDescriptor is the probe: it only reads, as a probe must,
// and grants the capability when ~/notes is a directory the account may
// write.
func notebookProbeDescriptor() collection.Descriptor {
	return collection.Descriptor{
		Name: notebookProbeFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePOSIXFileSystem},
			Status:               collection.StatusImplemented,
			Reversibility:        collection.Reversibility{ReadOnly: true, Notes: "It tests a directory and changes nothing."},
			SupportsCheck:        true,
			Doc: collection.Doc{
				Summary: "Finds the notes directory of the connecting account: example.notebook's probe.",
				Params: []collection.Param{
					{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task."},
				},
				Returns: []collection.ReturnField{
					{Name: "granted", Type: "bool", Returned: "always", Description: "Whether ~/notes is a directory the account may write."},
					{Name: "properties", Type: "dict", Returned: "when granted", Description: "The grant's properties: dir."},
				},
			},
		},
		Invoke: Probe,
		Check:  Probe,
	}
}

// Probe reports whether ~/notes is a directory the account may write, and
// where it is.
func Probe(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, notebookProbeFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	res, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"sh", "-c", `test -d "$HOME/notes" && test -w "$HOME/notes" && printf '%s' "$HOME/notes"`}))
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", notebookProbeFQCN, err)
	}
	dir := strings.TrimSpace(res.Stdout)
	granted := res.ExitCode == 0 && path.IsAbs(dir)
	if err := rc.SetStat("granted", granted); err != nil {
		return collection.Result{}, err
	}
	if granted {
		if err := rc.SetStat("properties", map[string]any{notebookDirProperty: dir}); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{}, nil
}

// notebookWriteDescriptor is example.notebook.write: a note, by name, in
// the device's notebook, which the method finds in the grant rather than
// a parameter.
func notebookWriteDescriptor() collection.Descriptor {
	return collection.Descriptor{
		Name: notebookWriteFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{notebookCapability, capability.NamePOSIXFileSystem},
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "Like example.note.write, it keeps no copy of a note's previous content.",
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes sure a note in the device's notebook holds exactly the given text.",
				Description: "Writes <dir>/<name>, where dir is the notebook directory the device's example.notebook grant names, so a runbook never says where the notebook is.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The note's file name."},
					{Name: noteParamContent, Type: "string", Required: true, Description: "The exact text the note should hold."},
					{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task."},
				},
				Returns: []collection.ReturnField{
					{Name: noteStatPath, Type: "string", Returned: "always", Description: "The note this task acted on."},
					{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The note before and after."},
				},
			},
		},
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			return writeNotebook(ctx, rc, device, params, false)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			return writeNotebook(ctx, rc, device, params, true)
		},
	}
}

// writeNotebook finds the notebook in the grant and writes the note there
// through example.note.write's own path.
func writeNotebook(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	name, err := sdk.RequiredStringParam(params, "name")
	if err != nil || !noteName.MatchString(name) {
		return collection.Result{}, fmt.Errorf("%s: name must be a file name, got %q", notebookWriteFQCN, name)
	}
	grant, ok := external.Granted(device, notebookCapability)
	dir, _ := grant[notebookDirProperty].(string)
	if !ok || !path.IsAbs(dir) {
		return collection.Result{}, fmt.Errorf("%s: %s holds no %s grant with a directory", notebookWriteFQCN, device.Name(), notebookCapability)
	}
	inner := map[string]any{noteParamPath: path.Join(dir, name), noteParamContent: params[noteParamContent]}
	if v, ok := params[sdk.ParamInsecureSkipHostKeyVerify]; ok {
		inner[sdk.ParamInsecureSkipHostKeyVerify] = v
	}
	return writeNote(ctx, rc, device, inner, check)
}
