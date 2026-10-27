package file

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file holds "file.template".
//
// # What it is
//
// ansible.builtin.template is ansible.builtin.copy with the content run
// through a renderer first. This method is exactly that, and the renderer is
// NOT here: a Collection may import pkg/, the standard library and
// third-party modules only (internal/archtest), and the one template engine
// this platform has is internal/render. So the ENGINE renders, and this
// method is handed rendered bytes.
//
//   - content is a template written in the runbook. Like every string
//     parameter it renders when the task dispatches (internal/engine's
//     render_params.go), so by the time this runs it is plain text.
//   - src names a template file in the project. Its Param.Format is
//     collection.ParamFormatTemplateFile, which makes the engine read the
//     file, render it, remove src and set content to the result. Reaching this
//     method with src still set means that did not happen (a caller that
//     bypassed the engine), and the method refuses rather than write an
//     unrendered template.
//
// Everything after that is file.copy's own body (copyFile): the checksum
// comparison that decides whether to write, the atomic write, the attribute
// pass, the check-mode prediction, the diff (checksums, never content, so a
// rendered secret never reaches the journal) and the emitted inverse. A
// file.template therefore works on every device file.copy does: the POSIX
// file system over SSH (Linux, macOS, FreeBSD). Windows has win.file.copy and
// no template method.

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "file.template",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			SupportsCheck:   true,
			// Reversibility is file.copy's, because the write is: see the
			// Notes there for what is and is not restored.
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that created the file emits a file.remove that deletes it again, which is a complete undo. A run that " +
					"OVERWROTE an existing file emits a file.permissions restoring the mode, owner and group it found, and the previous " +
					"CONTENT is not restored by it: those bytes are not read before the write and are not journaled anywhere, so once " +
					"the new content lands the old content is gone. The emitted instruction says so in its own description rather than " +
					"leaving a rollback to discover it. A run that changed nothing emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "file.remove", Record: []string{"path"}, MayKeepOnFailure: true},
					{FQCN: "file.permissions", Record: []string{"path", "mode", "owner", "group"}, MayBePartial: true, MayKeepOnFailure: true},
				},
			},
			Doc: templateDoc(),
		},
		Invoke: Template,
		Check:  CheckTemplate,
	})
}

// templateDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, which is the source
// the scaffolder is driven from, and internal/archtest's
// TestCatalogDataDocsMatchTheRegistry compares the two for equality so
// the copies cannot drift.
func templateDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Renders a template with the run's variables and results and writes the result to a file on the target, only when the bytes there differ.",
		Description: "Renders a template and writes the result to a path on the device exactly as file.copy writes content: a SHA-256 of the rendered bytes is compared with one the device computes of what is already there, so a run that finds the same bytes sends no write and reports no change; a write goes to a temporary file in the destination's directory with the final mode, owner and group and is renamed over it; an existing file keeps its mode, owner and group unless the task names them; a new file without a mode gets 0600; anything at dest that is not a regular file is refused. The template is rendered by the platform's one template engine on the machine running the work, never on the device, with the same syntax as every other parameter: {{ vars.x }} reads a run variable, {{ result.name }} and {{ nodes.name }} read earlier tasks' registered results, and {{ device.name }} the device the task runs on. A variable or result that does not exist is an error, not an empty string, so a misspelled name never writes a half-filled file. The template is either content, written in the runbook, or src, a file in the project directory (relative, inside it, through no symbolic link, never .pleiades, a regular file of at most 64 MiB) that the command line reads; a Walk-tier Runner holds no project, so there src is refused and content is the way. The rendered text can hold a value a bound credential or a variable carried: it is sent to the device and is never written to the journal, the diff or the result, which hold the file's checksum and size only. In check mode it makes the same reads and refusals and predicts the write and its diff without sending one. Writing a file this way is undone like file.copy's: a created file is removed, and an overwritten file gets its former mode, owner and group back, while its former content is not restored.",
		Params: []collection.Param{
			{Name: copyParamDest, Type: "string", Required: true, Description: "The path to write on the device. Its parent directory must already exist: this method writes a file and never creates the directories above it, which is file.directory's job."},
			{Name: copyParamContent, Type: "string", Description: "The template, written in the runbook (set content or src, not both). It renders like any parameter holding {{ }}; text without one is written verbatim. Nothing is appended, so a file that should end in a newline needs one in the value."},
			{Name: copyParamSrc, Type: "string", Description: "A template file in the project directory, as a path relative to it that is written out in the runbook, not computed: it must stay inside the project, pass through no symbolic link, not be in .pleiades, and be a regular file of at most 64 MiB. The command line reads and renders it; a Walk-tier Runner cannot see the project, so use content there. Set content or src, not both.", Format: collection.ParamFormatTemplateFile},
			{Name: copyParamMode, Type: "string", Description: "The permission bits as one to four octal digits, quoted, for example \"0644\". A symbolic mode such as u+x is refused. Left unset, an existing file keeps its mode and a newly created one gets 0600."},
			{Name: copyParamOwner, Type: "string", Description: "The user name to give the file. A numeric id is refused. Left unset, an existing file keeps its owner and a new one belongs to the connecting account."},
			{Name: copyParamGroup, Type: "string", Description: "The group name to give the file, refused as a numeric id for the same reason owner is."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: copyParamDest, Type: "string", Returned: "always", Description: "The path this task wrote."},
			{Name: copyStatChecksum, Type: "string", Returned: "always", Description: "The SHA-256 of the file's contents in hex, read back from the device after any write."},
			{Name: copyParamMode, Type: "string", Returned: "always", Description: "The permission bits the file carries now, as four octal digits."},
			{Name: copyParamOwner, Type: "string", Returned: "always", Description: "The user name owning the file now."},
			{Name: copyParamGroup, Type: "string", Returned: "always", Description: "The group owning the file now."},
			{Name: copyStatSize, Type: "int", Returned: "always", Description: "The file's size in bytes now."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The before and after state of the path, each holding its kind, mode, owner, group, size, modification time and the checksum of its contents, never the content itself."},
			{Name: sdk.StatWarnings, Type: "list of string", Returned: "when the write was not atomic", Description: "Says the new bytes were copied into the existing file rather than renamed over it, because a new file could not be given its owner and group."},
			{Name: sdk.StatInverse, Type: "dict", Returned: "when something changed", Description: "The concrete task that undoes this run: a file.remove when this run created the file, or a file.permissions restoring the attributes it found when the file already existed. Absent from a run that changed nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Render a configuration file from a template in the project",
				RunbookYAML: "- name: Install the NTP configuration\n  file.template:\n    src: templates/chrony.conf\n    dest: /etc/chrony/chrony.conf\n    mode: \"0644\"\n",
			},
			{
				Name:        "Render a short template written in the runbook",
				RunbookYAML: "- name: Write the node label\n  file.template:\n    dest: /etc/node-label\n    content: \"role={{ vars.role }}\\nhost={{ device.name }}\\n\"\n    mode: \"0644\"\n",
			},
		},
		SeeAlso: []string{"file.copy", "file.touch", "file.permissions", "file.remove"},
	}
}

// Template implements the "file.template" collection method: it writes the
// text the engine rendered, through file.copy's one body.
//
// A src still in params is refused: the engine replaces src with the rendered
// content before dispatch, so a src that arrives is a template nobody
// rendered, and writing it would put "{{ ... }}" on a device in place of its
// values.
func Template(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := templateUnrendered(params); err != nil {
		return collection.Result{}, err
	}
	return copyFile(ctx, rc, device, params, collection.ModeExecute, "file.template")
}

// CheckTemplate is file.template's check: file.copy's check over the rendered
// content, with the same refusal of an unrendered src.
func CheckTemplate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := templateUnrendered(params); err != nil {
		return collection.Result{}, err
	}
	return copyFile(ctx, rc, device, params, collection.ModeCheck, "file.template")
}

// templateUnrendered refuses a call whose src reached the method, which means
// the engine did not render it.
func templateUnrendered(params map[string]any) error {
	if v, set := params[copyParamSrc]; set && v != nil {
		return fmt.Errorf("file.template: %s reached the method unrendered: the engine reads a template file and hands the method its rendered %s, so this call did not come through the engine and writing it would put the template's own text on the device", copyParamSrc, copyParamContent)
	}
	return nil
}
