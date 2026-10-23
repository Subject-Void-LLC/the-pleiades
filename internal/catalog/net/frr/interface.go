// Package frr: net.frr.interface, an interface's administrative state.
package frr

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// interfaceFQCN is net.frr.interface's name.
const interfaceFQCN = "net.frr.interface"

// interfaceName is what an interface name may be: what Linux and FRR
// name one, and nothing a vtysh line could read as more than a name.
var interfaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,31}$`)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: interfaceFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo sets the administrative state the interface had before the run, recorded from the router, never simply the opposite of the change.",
				Inverses: []sdk.InverseSpec{
					{FQCN: interfaceFQCN, Record: []string{"name", "enabled"}, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Sets an FRRouting interface administratively up or down, recording the state it found.",
				Description: "Reads the interface's administrative state through vtysh, and changes it only when it differs (shutdown or no shutdown in the running configuration, not frr.conf). Its undo sets the state it found, never simply the opposite, so undoing a run against an interface that was already down leaves it down.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The interface, as FRR names it (eth1, ens3)."},
					{Name: "enabled", Type: "bool", Required: true, Description: "true for up (no shutdown), false for down (shutdown)."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "enabled", Type: "bool", Returned: "always", Description: "Whether the interface is administratively up now."},
					{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo: this method with the state found."},
				},
				Examples: []collection.Example{
					{Name: "Take a link down", RunbookYAML: "- name: Shut r1's link to r3\n  net.frr.interface:\n    name: eth2\n    enabled: false\n"},
				},
				SeeAlso: []string{"net.frr.config", "wait.routes"},
			},
		},
		Invoke: Interface,
		Check:  CheckInterface,
	})
}

// Interface sets an interface administratively up or down.
func Interface(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return setInterface(ctx, rc, device, params, collection.ModeExecute)
}

// CheckInterface predicts Interface: the same read, no change.
func CheckInterface(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return setInterface(ctx, rc, device, params, collection.ModeCheck)
}

// setInterface is both modes' one body.
func setInterface(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	name, _ := params["name"].(string)
	if !interfaceName.MatchString(name) {
		return collection.Result{}, fmt.Errorf("%s: name must be an interface name, got %q", interfaceFQCN, name)
	}
	want, ok := params["enabled"].(bool)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: enabled must be true or false", interfaceFQCN)
	}
	s, err := open(ctx, rc, device, params, interfaceFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()

	was, err := s.adminUp(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	if was == want || mode == collection.ModeCheck {
		// Converged, it is want already; in a check, want is what a real
		// run leaves.
		if err := rc.SetStat("enabled", want); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: was != want}, nil
	}
	line := "shutdown"
	if want {
		line = "no shutdown"
	}
	// Recorded first and kept through a failure: an undo setting the state
	// that was there changes nothing if the change never happened.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          interfaceFQCN,
		Params:        map[string]any{"name": name, "enabled": was},
		Description:   fmt.Sprintf("Sets %s administratively %s again, as it was before this run.", name, upDown(was)),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{}, err
	}
	if _, err := s.vtysh(ctx, "configure terminal", "interface "+name, line); err != nil {
		return collection.Result{}, err
	}
	now, err := s.adminUp(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	if now != want {
		return collection.Result{}, fmt.Errorf("%s: %s is still administratively %s after %q", interfaceFQCN, name, upDown(now), line)
	}
	if err := rc.SetStat("enabled", now); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, nil
}

// adminUp reports whether FRR holds name administratively up.
func (s *session) adminUp(ctx context.Context, name string) (bool, error) {
	data, err := s.vtyshJSON(ctx, "show interface "+name+" json")
	if err != nil {
		return false, err
	}
	entry, ok := data[name].(map[string]any)
	if !ok {
		return false, fmt.Errorf("%s: the router has no interface %s", s.fqcn, name)
	}
	status, _ := entry["administrativeStatus"].(string)
	switch status {
	case "up":
		return true, nil
	case "down":
		return false, nil
	}
	return false, fmt.Errorf("%s: %s's administrative state is %q, neither up nor down", s.fqcn, name, status)
}

// upDown is "up" or "down".
func upDown(up bool) string {
	if up {
		return "up"
	}
	return "down"
}
