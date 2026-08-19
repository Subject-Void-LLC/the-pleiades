// Package svc implements the five generic "svc.*" Collection methods:
// start, stop, restart, enable and disable.
//
// # What "generic" means here, and what it does not
//
// These do not reimplement service management. Each one resolves the
// device's service manager and hands the call to the concrete method for
// it, so svc.start against a systemd host runs svc.systemd.start and
// nothing about starting a service is written twice. A runbook that names
// svc.start is saying "make sure this service is running" without also
// saying which init system the fleet runs, which is the whole reason the
// generic form exists.
//
// # Why this dispatches through the registry rather than calling a function
//
// internal/archtest forbids a Collection package from importing anything
// outside pkg/, and internal/catalog/svc/systemd is a sibling under
// internal/, so this package cannot call systemd.Start directly no matter
// how much it would like to. It looks the concrete FQCN up in the
// pkg/collection registry and invokes the descriptor it finds.
//
// That constraint turns out to produce the better design anyway. The
// registry is what the dispatcher and the runbook author both see, so a
// generic call and a direct call to the same concrete method cannot
// diverge in behavior. And a service manager whose concrete methods are
// registered but not yet implemented, which is exactly where
// svc.windows.* stands today, produces an accurate "declared but not
// implemented" refusal naming the method that is missing, rather than a
// silent success or a confusing error from a systemctl that is not there.
//
// # capability.ServiceManagerCapable is the requirement, and it resolves downward
//
// These methods require the broad ServiceManagerCapable rather than any
// concrete one, which is what that capability's own doc comment says it
// exists for. A device declaring SystemdCapable satisfies it, because
// record.Base.Declares runs capability.Resolves and SystemdCapable is a
// child of ServiceManagerCapable. Requiring a concrete capability here
// would defeat the point of the generic method entirely.
package svc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramName is the service to act on, ansible.builtin.service's own
// spelling and the same one the concrete methods take, so a task
// converted from svc.start to svc.systemd.start needs no parameter edit.
const paramName = "name"

// managerNamespace maps what a device says its service manager is onto
// the Collection namespace that implements it.
//
// It is data rather than a type switch for the reason the rest of this
// codebase gives for the same choice: supporting a new init system is an
// entry here plus a namespace of concrete methods, never a change to the
// dispatch below. The keys are the values a device type's
// ServiceManagerName returns.
var managerNamespace = map[string]string{
	"systemd":     "svc.systemd",
	"windows_scm": "svc.windows",
}

// knownManagers lists the managers this dispatcher knows, sorted, for an
// error message that tells an operator what the alternatives are.
func knownManagers() string {
	names := make([]string, 0, len(managerNamespace))
	for name := range managerNamespace {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// dispatch resolves the concrete method for this device's service
// manager and invokes it.
//
// verb is the last segment of the FQCN ("start"), which is identical
// between the generic method and every concrete one, so the concrete name
// is the namespace plus the verb rather than a second lookup table that
// could disagree with the first.
func dispatch(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, verb string) (collection.Result, error) {
	fqcn := "svc." + verb

	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device to resolve a service manager from", fqcn)
	}

	// The device says which manager it runs. This is a property rather
	// than a capability because "has a service manager" and "runs
	// systemd specifically" are different claims, and a Linux device type
	// can honestly make the first while an operator overrides the second
	// for an OpenRC or Alpine host.
	manager, ok := device.(capability.ServiceManagerCapable)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q does not report a service manager, so there is nothing to dispatch to",
			fqcn, device.Name())
	}
	name := manager.ServiceManagerName()

	namespace, known := managerNamespace[name]
	if !known {
		return collection.Result{}, fmt.Errorf("%s: device %q reports service manager %q, which this platform has no methods for (it knows: %s)",
			fqcn, device.Name(), name, knownManagers())
	}

	target := namespace + "." + verb
	desc, found := collection.Lookup(target)
	if !found {
		return collection.Result{}, fmt.Errorf("%s: device %q runs %s, and %s is not registered",
			fqcn, device.Name(), name, target)
	}
	if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
		return collection.Result{}, fmt.Errorf("%s: device %q runs %s, and %s is declared but not implemented yet, so this cannot be done on this device",
			fqcn, device.Name(), name, target)
	}

	// The concrete method's own capability requirement is checked here
	// too, and skipping it would be a real hole rather than a
	// duplication. engine.checkMethodCapabilities validated THIS method's
	// requirement, ServiceManagerCapable, before dispatch; invoking the
	// descriptor directly bypasses the executor, so nothing else would
	// ever check that the device satisfies the concrete method's stricter
	// one.
	for _, required := range desc.Manifest.RequiredCapabilities {
		if !device.HasCapability(required) {
			return collection.Result{}, fmt.Errorf("%s: device %q reports service manager %s, but does not have %s, which %s requires",
				fqcn, device.Name(), name, required, target)
		}
	}

	return desc.Invoke(ctx, rc, device, params)
}

// genericDoc builds the reference documentation shared by all five
// generic methods.
func genericDoc(summary, description string, examples []collection.Example, seeAlso []string) collection.Doc {
	return collection.Doc{
		Summary:     summary,
		Description: description,
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: paramName, Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
		},
		Examples: examples,
		SeeAlso:  seeAlso,
	}
}

// genericManifest builds the manifest shared by all five generic
// methods, which differ only in their documentation and reversibility.
func genericManifest(reversibility collection.Reversibility, doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		// Empty rather than "ssh": which transport this reaches the
		// device over is the concrete method's business, and a systemd
		// host and a Windows one do not agree on the answer.
		SupportedTransports:  nil,
		RequiredCapabilities: []capability.Name{capability.NameServiceManager},
		ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
		PlatformTargets:      nil,
		EngineVersion:        ">=1.0.0",
		Status:               collection.StatusImplemented,
		Reversibility:        reversibility,
		Doc:                  doc,
	}
}
