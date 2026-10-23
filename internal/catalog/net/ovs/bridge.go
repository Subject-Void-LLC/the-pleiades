// Package ovs: net.ovs.bridge, a bridge and its datapath.
package ovs

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// bridgeFQCN is net.ovs.bridge's name.
const bridgeFQCN = "net.ovs.bridge"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: bridgeFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameOpenVSwitch, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo puts back what it found: a created bridge is removed, a changed datapath set back, and a removed bridge recreated without its ports or settings such as a fail mode or a controller, which that undo says by being partial.",
				Inverses: []sdk.InverseSpec{
					{FQCN: bridgeFQCN, Record: []string{"name", "state", "datapath_type"}, MayBePartial: true, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes an Open vSwitch bridge exist with the datapath given, or not exist.",
				Description: "Reads the bridge through ovs-vsctl and changes only what differs. Its undo puts back what it found: a bridge it created is removed, a datapath it changed is set back. Removing a bridge also removes its ports, which its undo does not recreate, so that undo is partial and says so.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The bridge."},
					{Name: "state", Type: "string", Default: "present", Description: "present or absent."},
					{Name: "datapath_type", Type: "string", Description: "system (the kernel datapath) or netdev (userspace). Left unset, an existing bridge keeps its own and a new one gets Open vSwitch's default."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the bridge exists now."},
					{Name: "datapath_type", Type: "string", Returned: "when it exists", Description: "Its datapath now."},
					{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo."},
				},
				Examples: []collection.Example{
					{Name: "A userspace bridge", RunbookYAML: "- name: br0 exists\n  net.ovs.bridge:\n    name: br0\n    datapath_type: netdev\n"},
				},
				SeeAlso: []string{"net.ovs.port"},
			},
		},
		Invoke: Bridge,
		Check:  CheckBridge,
	})
}

// Bridge makes a bridge exist with the datapath given, or not exist.
func Bridge(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return bridge(ctx, rc, device, params, collection.ModeExecute)
}

// CheckBridge predicts Bridge: the same reads, nothing changed.
func CheckBridge(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return bridge(ctx, rc, device, params, collection.ModeCheck)
}

// bridge is both modes' one body.
func bridge(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	name, _ := params["name"].(string)
	if err := checkName(bridgeFQCN, "name", name); err != nil {
		return collection.Result{}, err
	}
	present, err := stateParam(bridgeFQCN, params)
	if err != nil {
		return collection.Result{}, err
	}
	datapath, err := sdk.OptionalStringParam(params, "datapath_type")
	if err != nil {
		return collection.Result{}, err
	}
	if datapath != "" && datapath != "system" && datapath != "netdev" {
		return collection.Result{}, fmt.Errorf("%s: datapath_type must be system or netdev, got %q", bridgeFQCN, datapath)
	}
	if !present && datapath != "" {
		return collection.Result{}, fmt.Errorf("%s: datapath_type names a bridge that state absent removes", bridgeFQCN)
	}
	s, err := open(ctx, rc, device, params, bridgeFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()

	exists, was, ports, err := s.readBridge(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	var args []string
	var undo sdk.Inverse
	switch {
	case present && !exists:
		args = []string{"add-br", name}
		if datapath != "" {
			args = append(args, "--", "set", "Bridge", name, "datapath_type="+datapath)
		}
		undo = sdk.Inverse{FQCN: bridgeFQCN, Params: map[string]any{"name": name, "state": "absent"}, Description: "Removes the bridge " + name + ", which this run created."}
	case present && datapath != "" && datapath != was:
		args = []string{"set", "Bridge", name, "datapath_type=" + datapath}
		undo = sdk.Inverse{FQCN: bridgeFQCN, Params: map[string]any{"name": name, "datapath_type": was}, Description: fmt.Sprintf("Sets %s's datapath back to %s.", name, was)}
	case !present && exists:
		args = []string{"del-br", name}
		undo = sdk.Inverse{FQCN: bridgeFQCN, Params: map[string]any{"name": name, "state": "present", "datapath_type": was}, Description: "Creates the bridge " + name + " again, on the " + was + " datapath."}
		if len(ports) > 0 {
			undo.Partial = true
			undo.Description += " Its ports (" + strings.Join(ports, ", ") + ") are not added back."
		}
		set, err := s.nonDefault(ctx, "Bridge", name, map[string][]string{"fail_mode": {"[]"}, "protocols": {"[]"}, "other_config": {"{}"}, "controller": {"[]"}})
		if err != nil {
			return collection.Result{}, err
		}
		if len(set) > 0 {
			undo.Partial = true
			undo.Description += " Its " + strings.Join(set, ", ") + " are not put back."
		}
	}
	if args == nil || mode == collection.ModeCheck {
		if err := setBridgeStats(rc, present, datapathAfter(present, exists, datapath, was)); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: args != nil}, nil
	}
	if _, err := s.must(ctx, args...); err != nil {
		return collection.Result{}, err
	}
	// ovs-vsctl changes the database in one transaction, so once it exits
	// zero the change is there, and its undo stands even if the read-back
	// below fails.
	undo.KeepOnFailure = true
	if err := sdk.RecordInverse(rc, undo); err != nil {
		return collection.Result{}, err
	}
	nowExists, now, _, err := s.readBridge(ctx, name)
	if err != nil {
		return collection.Result{}, err
	}
	if nowExists != present || (present && datapath != "" && now != datapath) {
		return collection.Result{}, fmt.Errorf("%s: after ovs-vsctl %s the bridge %s is not as asked (exists %v, datapath %s)", bridgeFQCN, args[0], name, nowExists, now)
	}
	if err := setBridgeStats(rc, nowExists, now); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, nil
}

// datapathAfter is the datapath a run leaves, for a check's prediction.
func datapathAfter(present, exists bool, want, was string) string {
	switch {
	case !present:
		return ""
	case want != "":
		return want
	case exists:
		return was
	}
	// A new bridge with no datapath named gets Open vSwitch's default.
	return "system"
}

// setBridgeStats records exists and, when it does, the datapath.
func setBridgeStats(rc sdk.RunbookContext, exists bool, datapath string) error {
	if err := rc.SetStat("exists", exists); err != nil {
		return err
	}
	if exists {
		return rc.SetStat("datapath_type", datapath)
	}
	return nil
}

// readBridge reports whether the bridge exists, its datapath (an empty
// one, Open vSwitch's default, read as system) and its ports.
func (s *session) readBridge(ctx context.Context, name string) (bool, string, []string, error) {
	code, out, err := s.vsctl(ctx, "br-exists", name)
	if err != nil {
		return false, "", nil, err
	}
	switch code {
	case 2:
		return false, "", nil, nil
	case 0:
	default:
		return false, "", nil, fmt.Errorf("%s: ovs-vsctl br-exists exited %d: %s", bridgeFQCN, code, bounded(out))
	}
	dp, err := s.must(ctx, "get", "Bridge", name, "datapath_type")
	if err != nil {
		return false, "", nil, err
	}
	datapath := unquote(dp)
	if datapath == "" {
		datapath = "system"
	}
	list, err := s.must(ctx, "list-ports", name)
	if err != nil {
		return false, "", nil, err
	}
	return true, datapath, strings.Fields(list), nil
}
