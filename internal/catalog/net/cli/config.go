package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramConfig matches ansible.netcommon.cli_config's own name for the
// configuration block a task supplies, ordinarily written as a YAML "|"
// block scalar so multiple lines survive as one string.
const paramConfig = "config"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "net.cli.config",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameNetworkCLI,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "An arbitrary CLI line's effect on a device is unknown to this platform, so no undo can be derived from it.",
			},
			NoCheckReason: "what a configuration line changes is decided by the device's own parser as it applies the line, and " +
				"a CLI offers no way to ask without applying it",
			Doc: configDoc(),
		},
		Invoke: Config,
	})
}

func configDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Applies configuration lines to a network device over its CLI.",
		Description: "Splits config into non-blank lines and sends each one through the same generic session net.cli.command uses, one line at a time, matched against the device's own declared cli_prompt property. It carries no vendor-specific configuration-mode knowledge of its own: it never enters or leaves a configuration mode on the caller's behalf, so a device that needs one (Cisco IOS's \"configure terminal\"/\"end\", for instance) must have those lines included in config itself. net.ios.config is the Cisco-specific sibling that does drive configuration mode, and is what a runbook targeting Cisco IOS should use instead. Reports changed every time every line reaches the device without error, the same convention net.cli.command and exec.command both establish: a CLI line's effect cannot be inspected, so this platform does not guess at one. Each line is bounded to 30 seconds for the same reason net.cli.command's own line is: a generic device has no known paging convention, so an unexpectedly long response can pause on a pager prompt this method cannot answer, and a bounded, clear error is preferable to an indefinite hang.",
		Params: []collection.Param{
			{Name: paramConfig, Type: "string", Required: true, Description: "The configuration lines to send, one per line. Blank lines are skipped. Include any vendor-specific mode commands (e.g. \"configure terminal\" and \"end\") this device needs, since this method sends none on its own."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Examples: []collection.Example{
			{
				Name:        "Apply configuration on a device with no vendor-specific method yet",
				RunbookYAML: "- name: Set a banner\n  net.cli.config:\n    config: |\n      configure terminal\n      banner motd ^Cauthorized access only^C\n      end\n",
			},
		},
		SeeAlso: []string{"net.cli.command", "net.ios.config"},
	}
}

// Config implements the "net.cli.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.cli.config"

	lines, err := configLines(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := openSession(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()

	for _, line := range lines {
		if _, err := runCommand(ctx, session, line); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: true}, nil
}

// configLines splits the config param into non-blank lines, each with
// its own trailing "\r" (a YAML block scalar never carries one, but a
// value pasted from elsewhere might) trimmed.
func configLines(params map[string]any) ([]string, error) {
	raw, err := sdk.RequiredStringParam(params, paramConfig)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s has no non-blank lines to send", paramConfig)
	}
	return lines, nil
}
