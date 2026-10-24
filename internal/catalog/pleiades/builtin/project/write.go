// Package project implements the "pleiades.builtin.project.write"
// Collection method: write text a run produced (a router's running
// configuration a task registered) to a file in the project directory, so
// it can be kept, edited and used as a candidate file.
//
// # Why a method and not a step outside the run
//
// The value to keep exists only inside the run: a registered result a
// template reads. Before this method an author copied it out of a --json
// report with jq, which is a step a Pleiades user should not need. Written
// as a method it gets validation, check mode, reference docs and the run's
// report like every other task.
//
// # Where it runs, and where it cannot
//
// It acts on no device (collection.DeviceNone): its task runs once, in the
// command line's own process, and writes through the run's
// sdk.ProjectFileWriter, which holds the path to the same containment
// rules sdk.ProjectFiles reads by. A Walk-tier Runner holds no project, so
// its contexts do not implement the writer and the method refuses by
// naming that, exactly as net.frr.config's src does.
//
// Because this package lives under internal/, it is reachable only from
// code inside this module or a fork of it: Go's internal/ visibility rule
// blocks any other module from importing it at all.
package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fqcn is this method's name, for its errors.
const fqcn = "pleiades.builtin.project.write"

// The method's parameters.
const (
	paramPath    = "path"
	paramContent = "content"
	paramMode    = "mode"
)

// defaultMode is the file's mode when the task names none: the owner's
// alone, since a configuration can hold more than its masked secrets.
const defaultMode fs.FileMode = 0o600

// octalMode is a mode as file.copy takes one: three or four octal digits,
// quoted so YAML does not read it as a decimal number.
var octalMode = regexp.MustCompile(`^0?[0-7]{3}$`)

// executeBits are the owner, group and other execute bits, none of which a
// written file may carry.
const executeBits fs.FileMode = 0o111

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcn,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{},
			RequiredCapabilities: []capability.Name{},
			// It runs in the command line's own process and touches no
			// device, so it needs no hosts, no credential and no privilege.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceNone,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "It overwrites a file in the project, whose earlier content is never journaled: the journal keeps names and counts, never file content. " +
					"The project is the author's own working copy, kept under version control, which is where an earlier version comes back from.",
			},
			// Its check runs every refusal a write makes and reads the file
			// to predict whether a write would change it.
			SupportsCheck: true,
			Doc:           writeDoc(),
		},
		Invoke: Write,
		Check:  Check,
	})
}

// writeDoc is the method's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func writeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Writes text a run produced, such as a router's running configuration, to a file in the project.",
		Description: "Writes content to path, a file in the project directory, so a value a task read (a registered result's running_config) can be kept, edited and used as a candidate: net.frr.config's src and net.batfish.check's src read such a file. The path is held to the rules a method reading the project is held to: relative, never leaving the project, never through a symbolic link, never into the project's .pleiades directory (matched by the directory itself, not only its name), and a regular file or none yet. It also never replaces the project's inventory (inventory.yaml and its generated state file), or an external Collections directory or anything in one (its programs and its approval list). The path is the author's choice: an expression in it may read only vars, never a registered result, so a device's output cannot choose which file is replaced. A missing directory on the way is created inside the project. The file is written whole to a temporary file beside it and renamed over it, so it is never seen half written, and the task reports changed only when the bytes or the mode differ. A check reads the file and predicts the same answer. It runs on the command line only (pleiades run and adhoc), which holds the project; a Walk-tier Runner refuses it. It acts on no device and needs no hosts. A configuration read by net.frr.facts or net.ios.* is masked: each secret in it is a pleiades-masked- placeholder, and net.frr.config refuses a candidate holding one, so put each secret back before applying the file. It cannot be undone by a rollback: the file's earlier content is never journaled, since the project is the author's working copy, kept under version control.",
		Params: []collection.Param{
			{Name: paramPath, Type: "string", Required: true, Description: "The file to write, a slash-separated path relative to the project directory, such as configs/r3.running.frr.conf. An expression in it may read only vars, such as configs/{{ vars.site }}.conf, never nodes, result or error.", Format: collection.ParamFormatProjectPath},
			{Name: paramContent, Type: "string", Required: true, Description: "The text to write, usually rendered from a registered result, such as \"{{ result.r3.running_config }}\". An empty string writes an empty file."},
			{Name: paramMode, Type: "string", Default: "0600", Description: "The file's permission bits as a quoted octal string, such as \"0640\". Only the owner can read the default, since a configuration can hold more than its masked secrets. An execute bit is refused: the file holds text a run read, which is never made a program."},
		},
		Returns: []collection.ReturnField{
			{Name: "path", Type: "string", Returned: "always", Description: "The file written, as given."},
			{Name: "sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the content, in hex: the digest net.batfish.check reports for the same bytes."},
			{Name: "size", Type: "int", Returned: "always", Description: "The content's length in bytes."},
		},
		Examples: []collection.Example{
			{Name: "Keep r3's running configuration as a candidate file", RunbookYAML: "- name: Read r3 as it runs\n  net.frr.facts:\n    target: r3\n    include_config: true\n  register: r3\n- name: Keep it in the project, to edit into a candidate\n  pleiades.builtin.project.write:\n    path: configs/r3.running.frr.conf\n    content: \"{{ result.r3.running_config }}\"\n"},
		},
		SeeAlso: []string{"net.frr.facts", "net.frr.config", "net.batfish.check", "file.copy"},
	}
}

// request is a task's parameters, read and checked before anything is
// written.
type request struct {
	path    string
	content []byte
	mode    fs.FileMode
}

// readRequest reads path, content and mode, refusing an absent path, a
// content that is not text, and a mode that is not a quoted octal string
// of permission bits.
func readRequest(params map[string]any) (request, error) {
	var r request
	path, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return r, err
	}
	r.path = path
	// content may be empty (an empty file), so it is read as present and
	// text rather than through RequiredStringParam, which refuses "".
	raw, ok := params[paramContent]
	if !ok || raw == nil {
		return r, fmt.Errorf("%s is required", paramContent)
	}
	text, ok := raw.(string)
	if !ok {
		return r, fmt.Errorf("%s must be text, got %s", paramContent, collection.ValueKind(raw))
	}
	r.content = []byte(text)
	r.mode = defaultMode
	mode, err := sdk.OptionalStringParam(params, paramMode)
	if err != nil {
		return r, fmt.Errorf("%s: %w; quote it, such as \"0640\"", paramMode, err)
	}
	if mode != "" {
		if !octalMode.MatchString(mode) {
			return r, fmt.Errorf("%s %q is not an octal mode of permission bits such as \"0640\"", paramMode, mode)
		}
		bits, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			return r, fmt.Errorf("%s %q: %w", paramMode, mode, err)
		}
		r.mode = fs.FileMode(bits)
	}
	// What a run writes here is text a device returned, so it is never
	// made a program: an execute bit would let that text run as one.
	if r.mode&executeBits != 0 {
		return r, fmt.Errorf("%s %04o sets an execute bit; a file this method writes holds text a run read, which is never made executable", paramMode, uint32(r.mode))
	}
	return r, nil
}

// Write implements the "pleiades.builtin.project.write" collection method.
func Write(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return write(rc, params, true)
}

// Check is the method's check: every refusal Write makes, and whether a
// write would change the file, read without writing.
func Check(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return write(rc, params, false)
}

// write is Write and Check, which differ only in whether the file is
// written.
func write(rc sdk.RunbookContext, params map[string]any, apply bool) (collection.Result, error) {
	req, err := readRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	writer, ok := rc.(sdk.ProjectFileWriter)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, sdk.ErrNoProjectFiles)
	}
	changed, err := writer.WriteProjectFile(req.path, req.content, req.mode, apply)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	sum := sha256.Sum256(req.content)
	for k, v := range map[string]any{"path": req.path, "sha256": hex.EncodeToString(sum[:]), "size": len(req.content)} {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, err
		}
	}
	// A masked configuration is fine to keep and to model, and wrong to
	// apply as it is: say so where the person running the work reads it.
	if strings.Contains(string(req.content), netcfg.MaskPrefix) {
		if err := sdk.RecordWarnings(rc, []string{fmt.Sprintf("%s holds masked secrets (%s... placeholders); put each secret back before applying it as a candidate, since net.frr.config refuses a candidate holding one", req.path, netcfg.MaskPrefix)}); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: changed}, nil
}
