// Package link: virt.cml.link.stop and virt.cml.link.start, a link cut and
// restored in a CML lab with both nodes left running, so a failover test
// takes a path away without stopping a router, whose running configuration a
// stop would lose.
package link

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The two methods and the state each leaves a link in.
const (
	stopFQCN  = "virt.cml.link.stop"
	startFQCN = "virt.cml.link.start"
)

func init() {
	for _, m := range []struct {
		fqcn, inverse string
		doc           collection.Doc
		invoke, check collection.Method
	}{
		{stopFQCN, startFQCN, stopDoc(), Stop, CheckStop},
		{startFQCN, stopFQCN, startDoc(), Start, CheckStart},
	} {
		collection.MustRegister(collection.Descriptor{
			Name: m.fqcn,
			Manifest: collection.Manifest{
				SupportedTransports:  []string{"https"},
				RequiredCapabilities: []capability.Name{capability.Name("CMLAPICapable")},
				ExecutionContext: collection.ExecutionContext{
					RequiresElevation: false,
					Site:              collection.SiteController,
					Device:            collection.DeviceRequired,
				},
				PlatformTargets: nil,
				EngineVersion:   ">=0.2.0",
				Status:          collection.StatusImplemented,
				Reversibility: collection.Reversibility{
					Reversible: true,
					Notes:      fmt.Sprintf("A run that changed the link's state emits %s of the same link, by its lab's id and its own; one that found the link already so emits nothing.", m.inverse),
					Inverses: []sdk.InverseSpec{
						{FQCN: m.inverse, Record: []string{paramLab, paramLink}, MayKeepOnFailure: true},
					},
				},
				SupportsCheck: true,
				Doc:           m.doc,
			},
			Invoke: m.invoke,
			Check:  m.check,
		})
	}
}

// stateParams are the parameters both methods take.
func stateParams() []collection.Param {
	return []collection.Param{
		{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
		{Name: "link", Type: "string", Description: "The link's id."},
		{Name: "a", Type: "string", Description: "One node, by label or id, with a_interface, b and b_interface, naming the link by its ends."},
		{Name: "a_interface", Type: "string", Description: "Its interface, by label."},
		{Name: "b", Type: "string", Description: "The other node, by label or id."},
		{Name: "b_interface", Type: "string", Description: "Its interface, by label."},
	}
}

// stateReturns are what both methods return.
func stateReturns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: "id", Type: "string", Returned: "always", Description: "The link's id."},
		{Name: "state", Type: "string", Returned: "always", Description: "The link's state before the task: STARTED, STOPPED or DEFINED_ON_CORE."},
	}
}

// stopDoc is virt.cml.link.stop's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func stopDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Stops a link in a CML lab, as if unplugged, leaving both nodes running.",
		Description: "Stops the link named by link, or the one between a's a_interface and b's b_interface: it carries nothing until started again, while both nodes keep running with their running configurations. This is how to fail a path or a site for a test without stopping a router, which loses a configuration that was never saved. A link already stopped is unchanged. Its undo is virt.cml.link.start of the same link. The task's target is a cml_controller device; the method runs on this machine and talks to the controller's REST API. A check reads the link's state and changes nothing.",
		Params:      stateParams(),
		Returns:     stateReturns(),
		Examples: []collection.Example{
			{Name: "Fail a core's WAN link, then restore it", RunbookYAML: "- name: Cut CORE1's WAN link\n  virt.cml.link.stop:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n- name: Restore it\n  virt.cml.link.start:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n"},
		},
		SeeAlso: []string{"virt.cml.link.start", "virt.cml.capture.start", "virt.cml.lab.stop"},
	}
}

// startDoc is virt.cml.link.start's reference documentation, which
// internal/forge/catalogdata holds an identical copy of.
func startDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Starts a stopped link in a CML lab, as if plugged in again.",
		Description: "Starts the link named by link, or the one between a's a_interface and b's b_interface, so it carries traffic again. A link already started is unchanged. Its undo is virt.cml.link.stop of the same link. The task's target is a cml_controller device; the method runs on this machine and talks to the controller's REST API. A check reads the link's state and changes nothing.",
		Params:      stateParams(),
		Returns:     stateReturns(),
		Examples:    []collection.Example{{Name: "Restore a link a test cut", RunbookYAML: "- name: Restore CORE1's WAN link\n  virt.cml.link.start:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: WAN1\n    b_interface: Ethernet0/0\n"}},
		SeeAlso:     []string{"virt.cml.link.stop", "virt.cml.lab.start"},
	}
}

// Stop implements "virt.cml.link.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runState(ctx, rc, device, params, stopFQCN, collection.ModeExecute)
}

// CheckStop is "virt.cml.link.stop"'s check.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runState(ctx, rc, device, params, stopFQCN, collection.ModeCheck)
}

// Start implements "virt.cml.link.start".
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runState(ctx, rc, device, params, startFQCN, collection.ModeExecute)
}

// CheckStart is "virt.cml.link.start"'s check.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runState(ctx, rc, device, params, startFQCN, collection.ModeCheck)
}

// runState moves a link to the state fqcn leaves it in, when it is not
// there already, and records the other method as the undo.
func runState(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	labRef, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return fail(err)
	}
	ref, err := cml.ReadLinkRef(params)
	if err != nil {
		return fail(err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return fail(err)
	}
	lab, link, err := client.LabAndLink(ctx, labRef, ref)
	if err != nil {
		// In a check, a lab or link an earlier task of the run makes is not
		// there yet, which only the run can answer.
		return fail(cml.Unchecked(mode, err))
	}
	if err := rc.SetStat("id", link.ID); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("state", link.State); err != nil {
		return collection.Result{}, err
	}
	stopping := fqcn == stopFQCN
	switch {
	case stopping && link.State == cml.StateStopped, !stopping && link.State == cml.StateStarted:
		return collection.Result{}, nil
	case stopping && link.State == cml.StateDefined:
		// A link that never ran carries nothing already. Stopping it would
		// record a start as its undo, starting a link that never ran.
		return collection.Result{}, nil
	case !stopping && link.State == cml.StateDefined:
		return fail(fmt.Errorf("%s has never run: start the lab (virt.cml.lab.start), which starts its links", ref))
	case mode == collection.ModeCheck:
		return collection.Result{Changed: true}, nil
	}
	inverse := startFQCN
	if stopping {
		err = client.StopLink(ctx, lab.ID, link.ID)
	} else {
		inverse = stopFQCN
		err = client.StartLink(ctx, lab.ID, link.ID)
	}
	if err != nil {
		return fail(err)
	}
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          inverse,
		Params:        map[string]any{paramLab: lab.ID, paramLink: link.ID},
		Description:   fmt.Sprintf("Put %s back as it was before this task.", ref),
		KeepOnFailure: true,
	}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
