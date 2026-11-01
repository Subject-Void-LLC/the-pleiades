package cli

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramCommand is Ansible's own name for the single line
// ansible.netcommon.cli_command sends, kept here for the same reason
// paramChdir/paramCreates/paramRemoves/paramStdin keep Ansible's names
// in internal/catalog/exec: this platform is a superset of Ansible, not
// a new vocabulary, so a person converting a playbook renames nothing.
const paramCommand = "command"

// statStdout matches ansible.netcommon.cli_command's own return key.
const statStdout = "stdout"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.cli.command",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
				"telnet",
				"console",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetworkCLI,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			NoBecomeReason:  sdk.NoBecomeCLISession,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// Mirrors exec.command's own reasoning verbatim: an
			// arbitrary CLI line's effect on a device is unknown to
			// this platform, so no undo can be derived from it.
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "An arbitrary CLI line's effect on a device is unknown to this platform, so no undo can be derived from it.",
			},
			NoCheckReason: "an arbitrary CLI line can be anything the device accepts, and what it changes, if anything, is known " +
				"only once the device has run it",
			Doc: commandDoc(),
		},
		Invoke: Command,
	})
}

func commandDoc() collection.Doc {
	params := []collection.Param{{Name: paramCommand, Type: "string", Required: true, Description: "The single CLI line to run, e.g. \"show version\".", Format: collection.ParamFormatCommand}}
	params = append(params, clidialog.Params()...)
	params = append(params, collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."})
	params = append(params, collection.Param{Name: sdk.ParamInsecureTelnet, Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."})
	returns := []collection.ReturnField{{Name: statStdout, Type: "string", Returned: "always", Description: "Everything the device printed in response to the command, its questions and the echo of each answer included, with the echoed command line and the trailing prompt removed."}}
	return collection.Doc{
		Summary: "Runs one show/exec-mode command against a network device's CLI.",
		Description: "Opens an interactive session over SSH (or over Telnet, with insecure_telnet) and runs one command, matched against the device's own declared cli_prompt property (a generic Dialect, built by netcli.FromPrompt, with no vendor-specific paging, configuration-mode or error convention of its own). Reports the command's own output, with its echoed input line and the trailing prompt both stripped. A command cannot be inspected, so this reports changed every time it reaches the device without error, the same convention exec.command established: pair it with when/when_or/when_cel when idempotence matters. Refuses outright when the target device's cli_prompt property is unset, rather than guessing at a prompt shape. Because this method has no known paging convention for a generic device, a command whose output is longer than the device's own terminal length can pause on a pager prompt this method cannot answer (verified directly against a real device); each command is bounded to 30 seconds so that failure is a clear, timely error rather than an indefinite hang. Pipe a long-output command through the device's own output filter (e.g. \"show running-config | include hostname\") to avoid triggering it, or use net.ios.config, whose vendor-specific dialect disables paging for real. " +
			"A command that asks questions before it finishes is answered from prompts, each answer written in the task, and a question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses; expect_disconnect counts the device closing the session after an answer as success. The 30 seconds cover the whole command, its questions included.",
		Params:  params,
		Returns: append(returns, clidialog.Returns()...),
		Examples: []collection.Example{
			{
				Name:        "Read a device's software version",
				RunbookYAML: "- name: Check the running version\n  net.cli.command:\n    command: show version\n  register: version\n",
			},
			{
				Name:        "Answer the question a command asks",
				RunbookYAML: "- name: Copy the configuration to a file\n  net.cli.command:\n    command: copy running-config flash:before-change.cfg\n    prompts:\n      - match: 'Destination filename \\[[^\\]]*\\]\\?'\n        answer: \"\"\n      - match: 'over ?write\\? \\[confirm\\]'\n        refuse: true\n",
			},
		},
		SeeAlso: []string{"net.cli.config", "net.ios.command", "net.ios.config"},
	}
}

// Command implements the "net.cli.command" collection method.
func Command(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.cli.command"

	line, err := sdk.RequiredStringParam(params, paramCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// Read before the session opens, so a parameter that does not read
	// never leaves a command half answered.
	dialog := clidialog.Uses(params)
	opts, err := clidialog.Read(ctx, rc, fqcn, params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	if !dialog {
		out, err := runCommand(ctx, session, line)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := rc.SetStat(statStdout, maskOutput(rc, out)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	res, err := runDialog(ctx, session, line, opts)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	stats := clidialog.Stats(res)
	stats[statStdout] = maskOutput(rc, res.Output)
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// maskOutput masks the secrets a device printed (a show of its
// configuration), as net.ios.command masks its own, and adds each one long
// enough to the run's masking set, so it is masked wherever else the run
// prints it.
func maskOutput(rc sdk.RunbookContext, out string) string {
	masked, found := netcfg.MaskFound(out)
	sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
	return masked
}
