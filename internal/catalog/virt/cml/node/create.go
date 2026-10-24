// Package node: virt.cml.node.create, a node made in a CML lab, optionally
// seeded with an inventory device's login and address. Its registration's
// Doc is generated from internal/forge/catalogdata.
package node

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.node.create",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"https",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CMLAPICapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that made a node emits virt.cml.node.delete naming its lab's id and its own, so a rollback removes exactly that node.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.node.delete", Record: []string{paramLab, paramNode}, MayKeepOnFailure: true},
				},
			},

			SupportsCheck:       true,
			SeedsLogin:          paramDevice,
			SeedsLoginIOSSecret: true,
			Doc: collection.Doc{
				Summary:     "Creates a node in a CML lab, optionally seeded with an inventory device's login and address.",
				Description: "Makes a node from a node definition, with the definition's interfaces, stopped. With device, the node stands for that inventory device (a cisco_router or cisco_switch; only an IOS or IOS XE definition): its day-0 configuration sets the hostname to the label, puts the device's host on management_interface in the Mgmt-intf VRF with a default route to management_gateway, enables SSH, and creates the device's login as \"username <user> privilege 15 secret 9 <hash>\" (and \"enable secret 9\" when the credential has an enable password), from the device's stored credential. The passwords never reach the runbook or the task's output: the method is handed type 9 hashes only. The hashes are in the node's configuration, which the controller keeps and anyone who can read the lab can read, so seed a node only from a device whose credential exists only in the lab. The console line logs an idle session out after five minutes. configuration adds lines after those. A label the lab already uses is refused, so a second run is a refusal rather than a second node. Its undo is virt.cml.node.delete of the node's id. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads the lab, the definition and the label and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "label", Type: "string", Required: true, Description: "The node's label in the lab, also its hostname when device is set."},
					{Name: "definition", Type: "string", Required: true, Description: "The node definition's id, e.g. iol-xe."},
					{Name: "device", Type: "string", Description: "The inventory device the node stands for: its host becomes the management address and its stored login is seeded as IOS secrets."},
					{Name: "management_interface", Type: "string", Default: "Ethernet0/0", Description: "The interface that carries the management address."},
					{Name: "management_prefix", Type: "int", Default: "24", Description: "The management address's prefix length."},
					{Name: "management_gateway", Type: "string", Description: "The management network's gateway, the Mgmt-intf VRF's default route. Required with device."},
					{Name: "configuration", Type: "string", Description: "Day-0 configuration lines added after the seeded ones, or the whole day-0 configuration without device. Never put a password here: use device."},
					{Name: "interfaces", Type: "int", Description: "The least number of physical interfaces the node has. A definition gives a few (iol-xe four, Ethernet0/0 to Ethernet0/3); more are added slot by slot, named as the definition names them (iol-xe's fifth is Ethernet1/0). Up to 128, and no more than the definition allows, which the controller enforces."},
					{Name: "x", Type: "int", Default: "0", Description: "The node's place on the lab's canvas, left to right."},
					{Name: "y", Type: "int", Default: "0", Description: "The node's place on the lab's canvas, top to bottom."},
				},
				Returns: []collection.ReturnField{
					{Name: "id", Type: "string", Returned: "on change", Description: "The new node's id."},
					{Name: "interfaces", Type: "list", Returned: "on change", Description: "Its interfaces' labels, in slot order."},
					{Name: "management_address", Type: "string", Returned: "when device is set", Description: "The address the node's management interface was given."},
				},
				Examples: []collection.Example{
					{Name: "A core router standing for an inventory device", RunbookYAML: "- name: Make CORE1\n  virt.cml.node.create:\n    lab: cvd\n    label: CORE1\n    definition: iol-xe\n    device: CORE1\n    management_gateway: 10.10.20.254\n    x: 0\n    y: 0\n"},
				},
				SeeAlso: []string{"virt.cml.link.create", "virt.cml.node.delete", "virt.cml.lab.start"},
			},
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

// Create implements "virt.cml.node.create".
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is "virt.cml.node.create"'s check: it reads the lab, the
// definition and the label, builds the seeded configuration, and makes
// nothing.
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeCheck)
}

// intParam reads an optional whole-number parameter, fallback when unset.
func intParam(params map[string]any, key string, fallback int) (int, error) {
	v, set, err := sdk.IntParam(params, key)
	if err != nil || !set {
		return fallback, err
	}
	return v, nil
}

// runCreate makes the node, refusing a label the lab already uses.
func runCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.node.create"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return fail(err)
	}
	label, err := sdk.RequiredStringParam(params, paramLabel)
	if err != nil {
		return fail(err)
	}
	definition, err := sdk.RequiredStringParam(params, paramDefinition)
	if err != nil {
		return fail(err)
	}
	if len(label) > 64 || strings.ContainsAny(label, "\r\n\t") {
		return fail(fmt.Errorf("%s %q is not a node label", paramLabel, label))
	}
	configuration := sdk.StringParam(params, paramConfig)
	x, err := intParam(params, paramX, 0)
	if err != nil {
		return fail(err)
	}
	y, err := intParam(params, paramY, 0)
	if err != nil {
		return fail(err)
	}
	ifaces, err := intParam(params, paramIfaces, 0)
	switch {
	case err != nil:
		return fail(err)
	case ifaces < 0 || ifaces > cml.MaxSlots:
		return fail(fmt.Errorf("%s must be from 1 to %d", paramIfaces, cml.MaxSlots))
	}
	seeded := sdk.StringParam(params, paramDevice) != ""
	var managementAddress string
	if seeded {
		iface := sdk.StringParam(params, paramInterface)
		if iface == "" {
			iface = "Ethernet0/0"
		}
		prefix, err := intParam(params, paramPrefix, 24)
		if err != nil {
			return fail(err)
		}
		s, err := readSeed(rc.InjectSecrets(), definition, label, iface, prefix, sdk.StringParam(params, paramGateway), configuration)
		if err != nil {
			return fail(err)
		}
		configuration = s.config()
		if s.host.IsValid() {
			managementAddress = s.host.String()
		}
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return fail(err)
	}
	lab, err := client.FindLab(ctx, ref)
	if err != nil {
		return fail(cml.Unchecked(mode, err))
	}
	defs, err := client.NodeDefinitions(ctx)
	if err != nil {
		return fail(err)
	}
	if !slices.Contains(defs, definition) {
		return fail(fmt.Errorf("the controller has no node definition %q", definition))
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return fail(err)
	}
	if _, err := cml.FindNode(nodes, label); !errors.Is(err, cml.ErrNotFound) {
		return fail(fmt.Errorf("lab %q already has a node labelled %q: remove it with virt.cml.node.delete, or choose another label", lab.Title, label))
	}
	if managementAddress != "" {
		if err := rc.SetStat("management_address", managementAddress); err != nil {
			return collection.Result{}, err
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	id, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: definition, X: x, Y: y, Configuration: configuration})
	if err != nil {
		return fail(err)
	}
	// The node exists from here on, so its undo stands even if reading it
	// back then fails: otherwise a rollback could not remove it, and a
	// second run would be refused on its label.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          "virt.cml.node.delete",
		Params:        map[string]any{paramLab: lab.ID, paramNode: id},
		Description:   fmt.Sprintf("Remove the node %s (%s) this task made in %q.", label, id, lab.Title),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{Changed: true}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if ifaces > 0 {
		// Slots below the last already made are left as they are.
		if err := client.CreateInterfaces(ctx, lab.ID, id, ifaces-1); err != nil {
			return collection.Result{Changed: true}, fmt.Errorf("%s: the node was made, and adding its interfaces failed: %w", fqcn, err)
		}
	}
	made, err := client.NodeInterfaces(ctx, lab.ID, id)
	if err != nil {
		return collection.Result{Changed: true}, fmt.Errorf("%s: the node was made, and reading its interfaces failed: %w", fqcn, err)
	}
	labels := make([]string, 0, len(made))
	for _, i := range made {
		labels = append(labels, i.Label)
	}
	if err := rc.SetStat("id", id); err != nil {
		return collection.Result{Changed: true}, err
	}
	return collection.Result{Changed: true}, rc.SetStat("interfaces", labels)
}
