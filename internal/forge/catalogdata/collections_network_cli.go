// The reference documentation of net.cli.command and net.ios.command, the
// two methods that run one command and answer the questions it asks. Their
// dialog parameters are pkg/clidialog's, documented once there.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// netCLICommandDescription is net.cli.command's reference description.
const netCLICommandDescription = "Opens an interactive session over SSH (or over Telnet, with insecure_telnet) and runs one command, matched against the device's own declared cli_prompt property (a generic Dialect, built by netcli.FromPrompt, with no vendor-specific paging, configuration-mode or error convention of its own). Reports the command's own output, with its echoed input line and the trailing prompt both stripped. A command cannot be inspected, so this reports changed every time it reaches the device without error, the same convention exec.command established: pair it with when/when_or/when_cel when idempotence matters. Refuses outright when the target device's cli_prompt property is unset, rather than guessing at a prompt shape. Because this method has no known paging convention for a generic device, a command whose output is longer than the device's own terminal length can pause on a pager prompt this method cannot answer (verified directly against a real device); each command is bounded to 30 seconds so that failure is a clear, timely error rather than an indefinite hang. Pipe a long-output command through the device's own output filter (e.g. \"show running-config | include hostname\") to avoid triggering it, or use net.ios.config, whose vendor-specific dialect disables paging for real. " +
	"A command that asks questions before it finishes is answered from prompts, each answer written in the task, and a question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses; expect_disconnect counts the device closing the session after an answer as success. The 30 seconds cover the whole command, its questions included."

// netIOSCommandDescription is net.ios.command's reference description.
const netIOSCommandDescription = "Opens an interactive PTY session over SSH using netcli.IOS's own paging, prompt and error conventions, so it needs no cli_prompt property and long output is never paused by a pager, and runs one command. A line in IOS's error forms (\"% ...\" or \"%Error ...\") fails the task. " +
	"A command that asks questions before it finishes (copy asks for a destination filename, delete and reload ask to confirm) is answered from prompts, each answer written in the task. A question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses, and never types an answer of its own: on IOS one Enter both accepts a default filename and confirms a reload, so only the task's author knows what an answer does. A question nobody answers waits until the task's timeout, and the failure names it. With expect_disconnect, the device closing the session after an answer is the command's success, which is how reload ends. " +
	"It has no time limit of its own, so set the task's timeout for a long copy. The output is everything the device printed after the echoed command, its questions and the echo of each answer included, with the trailing prompt removed. A command cannot be inspected, so it reports changed every time it runs without error."

// insecureSkipHostKeyVerify is the host key parameter every SSH method
// here documents.
var insecureSkipHostKeyVerify = collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

// insecureTelnet is the opt-in every CLI method here documents for a
// device reached over Telnet.
var insecureTelnet = collection.Param{Name: "insecure_telnet", Type: "bool", Default: "false", Description: "Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust."}

// netCLICommandParams are net.cli.command's parameters.
func netCLICommandParams() []collection.Param {
	params := []collection.Param{{Name: "command", Type: "string", Required: true, Description: "The single CLI line to run, e.g. \"show version\".", Format: collection.ParamFormatCommand}}
	params = append(params, clidialog.Params()...)
	return append(params, insecureSkipHostKeyVerify, insecureTelnet)
}

// netIOSCommandParams are net.ios.command's parameters.
func netIOSCommandParams() []collection.Param {
	params := []collection.Param{{Name: "command", Type: "string", Required: true, Description: "The single exec-mode line to run, e.g. \"show version\" or \"copy http://192.0.2.5/x.bin flash:x.bin\".", Format: collection.ParamFormatCommand}}
	params = append(params, clidialog.Params()...)
	return append(params, insecureSkipHostKeyVerify, insecureTelnet)
}

// netCLICommandReturns are what both methods return.
func netCLICommandReturns() []collection.ReturnField {
	returns := []collection.ReturnField{{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the device printed in response to the command, its questions and the echo of each answer included, with the echoed command line and the trailing prompt removed."}}
	return append(returns, clidialog.Returns()...)
}

// netIOSCommandExamples are net.ios.command's examples.
func netIOSCommandExamples() []collection.Example {
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
