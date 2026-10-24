// net.ios.command: one exec-mode command on a Cisco IOS device, with the
// questions it asks answered as the task says (pkg/clidialog).
package ios

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramCommand is the line net.ios.command runs, named as
// ansible.netcommon.cli_command names its own.
const paramCommand = "command"

// statCommandStdout is the stat the device's output is recorded under.
const statCommandStdout = "stdout"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.ios.command",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh", "telnet"},
			RequiredCapabilities: []capability.Name{capability.NameCiscoIOS},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "An arbitrary IOS command's effect (a copy, a delete, a reload) is unknown to this platform, so no undo can be derived from it. Give the task a rollback: list naming what undoes it when one exists.",
			},
			NoCheckReason: "an arbitrary command can be anything the device accepts, and what it changes, if anything, is known only once the device has run it",
			Doc:           commandDoc(),
		},
		Invoke: Command,
	})
}

// commandDoc is net.ios.command's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func commandDoc() collection.Doc {
	params := []collection.Param{{Name: paramCommand, Type: "string", Required: true, Description: "The single exec-mode line to run, e.g. \"show version\" or \"copy http://192.0.2.5/x.bin flash:x.bin\".", Format: collection.ParamFormatCommand}}
	params = append(params, clidialog.Params()...)
	params = append(params, collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."})
	params = append(params, collection.Param{Name: sdk.ParamInsecureTelnet, Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."})
	returns := []collection.ReturnField{{Name: statCommandStdout, Type: "string", Returned: "always", Description: "Everything the device printed in response to the command, its questions and the echo of each answer included, with the echoed command line and the trailing prompt removed."}}
	return collection.Doc{
		Summary: "Runs one exec-mode command on a Cisco IOS device, answering the questions it asks as the task says.",
		Description: "Opens an interactive PTY session over SSH using netcli.IOS's own paging, prompt and error conventions, so it needs no cli_prompt property and long output is never paused by a pager, and runs one command. A line in IOS's error forms (\"% ...\" or \"%Error ...\") fails the task. " +
			"A command that asks questions before it finishes (copy asks for a destination filename, delete and reload ask to confirm) is answered from prompts, each answer written in the task. A question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses, and never types an answer of its own: on IOS one Enter both accepts a default filename and confirms a reload, so only the task's author knows what an answer does. A question nobody answers waits until the task's timeout, and the failure names it. With expect_disconnect, the device closing the session after an answer is the command's success, which is how reload ends. " +
			"It has no time limit of its own, so set the task's timeout for a long copy. The output is everything the device printed after the echoed command, its questions and the echo of each answer included, with the trailing prompt removed. A command that only reads (show, or IOS's abbreviation of it, and dir and more written in full) reports unchanged, unless its output is sent to a file with | redirect, | append or | tee; any other command cannot be inspected, so it reports changed every time it runs without error.",
		Params:   params,
		Returns:  append(returns, clidialog.Returns()...),
		Examples: commandExamples(),
		SeeAlso:  []string{"net.cli.command", "net.ios.config", "net.ios.save", "ai.chat"},
	}
}

// commandExamples are net.ios.command's examples.
func commandExamples() []collection.Example {
	return []collection.Example{
		{
			Name:        "Copy an image to flash, refusing to overwrite one already there",
			RunbookYAML: "- name: Pull the new image onto flash\n  net.ios.command:\n    command: copy http://192.0.2.5/images/c8000v-17.15.04c.SPA.bin flash:c8000v-17.15.04c.SPA.bin\n    prompts:\n      - match: 'Destination filename \\[[^\\]]*\\]\\?'\n        answer: \"\"\n      - match: 'over ?write\\? \\[confirm\\]'\n        refuse: true\n  timeout: 1800\n",
		},
		{
			Name:        "Reload, ending when the device drops the session",
			RunbookYAML: "- name: Reload the router\n  net.ios.command:\n    command: reload\n    prompts:\n      - match: 'Proceed with reload\\? \\[confirm\\]'\n        answer: \"\"\n      - match: 'Save\\? \\[yes/no\\]'\n        refuse: true\n    expect_disconnect: true\n",
		},
		{
			Name:        "Let a local model answer a question nobody listed",
			RunbookYAML: "- name: Delete the old image\n  net.ios.command:\n    command: delete flash:c8000v-17.12.01.SPA.bin\n    prompts:\n      - match: 'Delete filename \\[[^\\]]*\\]\\?'\n        answer: \"\"\n    ask:\n      provider: workstation\n      model: gemma4:e4b-it-qat\n      residency: local\n      choices:\n        - id: confirm\n          answer: \"\"\n          means: Press Enter to confirm deleting the file the question names.\n        - id: decline\n          answer: \"n\"\n          means: Answer no and keep the file.\n      review:\n        - field: confidence\n          below: 0.8\n",
		},
	}
}

// Command implements the "net.ios.command" collection method.
func Command(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.ios.command"

	line, err := sdk.RequiredStringParam(params, paramCommand)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// Read before the session opens: ask's provider is resolved here, so a
	// parameter that does not read never leaves a command half answered.
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

	res, err := session.Dialog(ctx, line, opts)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// A command the device refused is not a command that ran: IOS reports
	// it in its output, not as a transport failure.
	if netcli.IOS.ErrorPattern.MatchString(res.Output) {
		return collection.Result{}, fmt.Errorf("%s: device rejected %q: %s", fqcn, line, strings.TrimSpace(firstErrorLine(res.Output)))
	}
	if err := rc.SetStat(statCommandStdout, res.Output); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if dialog {
		for k, v := range clidialog.Stats(res) {
			if err := rc.SetStat(k, v); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
		}
	}
	// A show command reads; every other command's effect cannot be
	// inspected, so it reports changed, as net.cli.command and exec.command
	// do. A second run of a read-only task then proves nothing changed.
	return collection.Result{Changed: !iosShow(line)}, nil
}

// iosShow reports whether line is an IOS command that only reads and
// writes nothing: its first word is show or an abbreviation IOS accepts
// for it (sh, sho), or dir or more in full (their abbreviations are
// shared with commands that change things), and none of its output pipes
// writes a file (redirect, append, tee, or their abbreviations; re, ap
// and te name nothing else after a pipe).
func iosShow(line string) bool {
	pipes := strings.Split(strings.ToLower(line), "|")
	first := strings.Fields(pipes[0])
	if !iosAbbreviates(pipes[0], "show") && (len(first) == 0 || (first[0] != "dir" && first[0] != "more")) {
		return false
	}
	for _, pipe := range pipes[1:] {
		if iosAbbreviates(pipe, "redirect") || iosAbbreviates(pipe, "append") || iosAbbreviates(pipe, "tee") {
			return false
		}
	}
	return true
}

// iosAbbreviates reports whether text's first word is word or an IOS
// abbreviation of it, at least two letters long.
func iosAbbreviates(text, word string) bool {
	fields := strings.Fields(text)
	return len(fields) > 0 && len(fields[0]) >= 2 && strings.HasPrefix(word, fields[0])
}
