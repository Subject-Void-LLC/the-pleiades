// Package frr: net.frr.command, one show command.
package frr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// commandFQCN is net.frr.command's name.
const commandFQCN = "net.frr.command"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: commandFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility:        collection.Reversibility{ReadOnly: true, Notes: "Runs only show commands, which change nothing."},
			SupportsCheck:        true,
			Doc: collection.Doc{
				Summary:     "Runs one FRRouting show command through vtysh and returns its output.",
				Description: "Only show commands are accepted, so it changes nothing. A command ending in json returns its output parsed as data too.",
				Params: []collection.Param{
					{Name: "command", Type: "string", Required: true, Description: "The show command, e.g. \"show ip ospf neighbor\".", Format: collection.ParamFormatCommand},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "stdout", Type: "string", Returned: "always", Description: "What vtysh printed."},
					{Name: "data", Type: "dict", Returned: "when the command ends in json", Description: "stdout parsed."},
				},
				Examples: []collection.Example{
					{Name: "Read OSPF neighbours", RunbookYAML: "- name: OSPF neighbours\n  net.frr.command:\n    command: show ip ospf neighbor\n"},
				},
				SeeAlso: []string{"net.frr.facts"},
			},
		},
		Invoke: Command,
		Check:  Command,
	})
}

// Command runs one show command through vtysh. Anything but a show
// command is refused before the router is reached.
func Command(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	command, _ := params["command"].(string)
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "show ") || strings.ContainsAny(command, "\n\r;|") {
		return collection.Result{}, fmt.Errorf("%s: only a single show command is accepted, got %q", commandFQCN, command)
	}
	s, err := open(ctx, rc, device, params, commandFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()
	out, err := s.vtysh(ctx, command)
	if err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("stdout", out); err != nil {
		return collection.Result{}, err
	}
	if strings.HasSuffix(command, " json") {
		var data map[string]any
		if err := json.Unmarshal([]byte(out), &data); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %q did not answer JSON: %w", commandFQCN, command, err)
		}
		if err := rc.SetStat("data", data); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{}, nil
}
