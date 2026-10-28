// Package ovs: net.ovs.port, a port on a bridge and its access VLAN.
package ovs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// portFQCN is net.ovs.port's name.
const portFQCN = "net.ovs.port"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: portFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameOpenVSwitch, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo puts back what it found: an added port is removed, a removed one added back on its bridge with its tag (partial when it carried more, such as an interface type or trunks), a moved one moved back, and a changed tag set back.",
				Inverses: []sdk.InverseSpec{
					{FQCN: portFQCN, Record: []string{"bridge", "name", "state", "tag"}, MayBePartial: true, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes a port exist on an Open vSwitch bridge with the VLAN tag given, or not exist.",
				Description: "Reads the port through ovs-vsctl and changes only what differs. Its undo puts back what it found: a port it added is removed, one it removed is added back on the bridge it was on, and a tag it changed is set back.",
				Params: []collection.Param{
					{Name: "bridge", Type: "string", Required: true, Description: "The bridge the port belongs on."},
					{Name: "name", Type: "string", Required: true, Description: "The port, usually an interface name (eth1)."},
					{Name: "state", Type: "string", Default: "present", Choices: []string{"present", "absent"}, Description: "present or absent."},
					{Name: "tag", Type: "int", Description: "An access VLAN, 1 to 4094; 0 removes the tag. Left unset, an existing port keeps its own."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the port exists now."},
					{Name: "tag", Type: "int", Returned: "when it exists", Description: "Its access VLAN now, 0 for none."},
					{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo."},
				},
				Examples: []collection.Example{
					{Name: "A port on br0", RunbookYAML: "- name: h1's port is on br0\n  net.ovs.port:\n    bridge: br0\n    name: eth2\n"},
				},
				SeeAlso: []string{"net.ovs.bridge"},
			},
		},
		Invoke: Port,
		Check:  CheckPort,
	})
}

// Port makes a port exist on a bridge with the tag given, or not exist.
func Port(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return port(ctx, rc, device, params, collection.ModeExecute)
}

// CheckPort predicts Port: the same reads, nothing changed.
func CheckPort(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return port(ctx, rc, device, params, collection.ModeCheck)
}

// port is both modes' one body.
func port(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	br, _ := params["bridge"].(string)
	if err := checkName(portFQCN, "bridge", br); err != nil {
		return collection.Result{}, err
	}
	name, _ := params["name"].(string)
	if err := checkName(portFQCN, "name", name); err != nil {
		return collection.Result{}, err
	}
	present, err := stateParam(portFQCN, params)
	if err != nil {
		return collection.Result{}, err
	}
	tag, hasTag, err := sdk.IntParam(params, "tag")
	if err != nil {
		return collection.Result{}, err
	}
	if hasTag && (tag < 0 || tag > 4094) {
		return collection.Result{}, fmt.Errorf("%s: tag must be 0 (none) to 4094, got %d", portFQCN, tag)
	}
	if !present && hasTag {
		return collection.Result{}, fmt.Errorf("%s: tag names a port that state absent removes", portFQCN)
	}
	s, err := open(ctx, rc, device, params, portFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()

	onBridge, wasTag, err := s.readPort(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	if exists, err := s.bridgeExists(ctx, br); err != nil {
		return collection.Result{}, err
	} else if !exists && present {
		return collection.Result{}, fmt.Errorf("%s: there is no bridge %s to put %s on", portFQCN, br, name)
	}
	var args []string
	var undo sdk.Inverse
	wantTag := wasTag
	if hasTag {
		wantTag = tag
	}
	switch {
	case present && onBridge == "":
		args = []string{"add-port", br, name}
		args = append(args, setTag(name, wantTag)...)
		undo = sdk.Inverse{FQCN: portFQCN, Params: map[string]any{"bridge": br, "name": name, "state": "absent"}, Description: fmt.Sprintf("Removes the port %s from %s, which this run added.", name, br)}
	case present && onBridge != br:
		// A move removes the port and adds it again, which drops whatever
		// it carries beyond its tag.
		if set, err := s.portSettings(ctx, name); err != nil {
			return collection.Result{}, err
		} else if len(set) > 0 {
			return collection.Result{}, fmt.Errorf("%s: moving %s from %s would drop its %s; nothing was changed", portFQCN, name, onBridge, strings.Join(set, ", "))
		}
		args = append([]string{"del-port", onBridge, name, "--", "add-port", br, name}, setTag(name, wantTag)...)
		undo = sdk.Inverse{FQCN: portFQCN, Params: map[string]any{"bridge": onBridge, "name": name, "tag": wasTag}, Description: fmt.Sprintf("Moves %s back to %s with tag %d.", name, onBridge, wasTag)}
	case present && wantTag != wasTag:
		args = setTag(name, wantTag)[1:]
		undo = sdk.Inverse{FQCN: portFQCN, Params: map[string]any{"bridge": br, "name": name, "tag": wasTag}, Description: fmt.Sprintf("Sets %s's tag back to %d.", name, wasTag)}
	case !present && onBridge == br:
		args = []string{"del-port", br, name}
		undo = sdk.Inverse{FQCN: portFQCN, Params: map[string]any{"bridge": br, "name": name, "state": "present", "tag": wasTag}, Description: fmt.Sprintf("Adds %s back to %s with tag %d.", name, br, wasTag)}
		set, err := s.portSettings(ctx, name)
		if err != nil {
			return collection.Result{}, err
		}
		if len(set) > 0 {
			undo.Partial = true
			undo.Description += " Its " + strings.Join(set, ", ") + " are not put back."
		}
	}
	if args == nil || mode == collection.ModeCheck {
		if err := setPortStats(rc, present, wantTag); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: args != nil}, nil
	}
	if _, err := s.must(ctx, args...); err != nil {
		return collection.Result{}, err
	}
	// One transaction: once ovs-vsctl exits zero the change is there.
	undo.KeepOnFailure = true
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return collection.Result{}, err
	}
	nowOn, nowTag, err := s.readPort(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	if present != (nowOn == br) || (present && nowTag != wantTag) {
		return collection.Result{}, fmt.Errorf("%s: after ovs-vsctl %s the port %s is on %q with tag %d, not as asked", portFQCN, args[0], name, nowOn, nowTag)
	}
	if err := setPortStats(rc, present, nowTag); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, nil
}

// portSettings returns what a port and its interface carry beyond a plain
// port with a tag, which re-adding it would not restore.
func (s *session) portSettings(ctx context.Context, name string) ([]string, error) {
	set, err := s.nonDefault(ctx, "Interface", name, map[string][]string{"type": {`""`, "system"}, "options": {"{}"}})
	if err != nil {
		return nil, err
	}
	more, err := s.nonDefault(ctx, "Port", name, map[string][]string{"trunks": {"[]"}, "vlan_mode": {"[]"}})
	if err != nil {
		return nil, err
	}
	return append(set, more...), nil
}

// setTag returns the ovs-vsctl arguments, after a "--", that give name's
// port tag (0 removing it).
func setTag(name string, tag int) []string {
	if tag == 0 {
		return []string{"--", "clear", "Port", name, "tag"}
	}
	return []string{"--", "set", "Port", name, "tag=" + strconv.Itoa(tag)}
}

// setPortStats records exists and, when it does, the tag.
func setPortStats(rc sdk.RunbookContext, exists bool, tag int) error {
	if err := rc.SetStat("exists", exists); err != nil {
		return err
	}
	if exists {
		return rc.SetStat("tag", tag)
	}
	return nil
}

// readPort returns the bridge the port is on ("" when there is none) and
// its tag (0 for none).
func (s *session) readPort(ctx context.Context, name string) (string, int, error) {
	code, out, err := s.vsctl(ctx, "port-to-br", name)
	if err != nil {
		return "", 0, err
	}
	if code != 0 {
		// ovs-vsctl exits 1 for a port it has never heard of, and for every
		// other fatal error too (a database it cannot reach, permission
		// refused), so only its own words make an absence.
		if code == 1 && strings.Contains(out, "no port named") {
			return "", 0, nil
		}
		return "", 0, fmt.Errorf("%s: ovs-vsctl port-to-br exited %d: %s", portFQCN, code, bounded(out))
	}
	t, err := s.must(ctx, "get", "Port", name, "tag")
	if err != nil {
		return "", 0, err
	}
	if t == "[]" {
		return out, 0, nil
	}
	tag, err := strconv.Atoi(t)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %s's tag is %q, not a number", portFQCN, name, t)
	}
	return out, tag, nil
}

// bridgeExists reports whether br exists.
func (s *session) bridgeExists(ctx context.Context, br string) (bool, error) {
	code, out, err := s.vsctl(ctx, "br-exists", br)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil
	case 2:
		return false, nil
	}
	return false, fmt.Errorf("%s: ovs-vsctl br-exists exited %d: %s", portFQCN, code, bounded(out))
}
