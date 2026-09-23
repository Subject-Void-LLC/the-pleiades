// Package pkg implements the three generic "pkg.*" Collection methods:
// install, remove and upgrade.
//
// # What "generic" means here, and what it does not
//
// These do not reimplement package management. Each one resolves the
// device's package manager and hands the call to the concrete method for
// it, so pkg.install against an APT host runs pkg.apt.install and
// nothing about installing a package is written twice. A runbook that
// names pkg.install is saying "make sure this package is present"
// without also saying which package manager the fleet runs, which is
// the whole reason the generic form exists. svc.start resolving to
// svc.systemd.start is the same shape, built the same session.
//
// # Why this dispatches through the registry rather than calling a function
//
// internal/archtest forbids a Collection package from importing anything
// outside pkg/, and internal/catalog/pkg/apt and internal/catalog/pkg/dnf
// are siblings under internal/, so this package cannot call apt.Install
// directly no matter how much it would like to. It looks the concrete
// FQCN up in the pkg/collection registry and invokes the descriptor it
// finds.
//
// That constraint turns out to produce the better design anyway. The
// registry is what the dispatcher and the runbook author both see, so a
// generic call and a direct call to the same concrete method cannot
// diverge in behavior.
//
// # capability.PackageManagerCapable is the requirement, and it resolves downward
//
// These methods require the broad PackageManagerCapable rather than any
// concrete one, which is what that capability's own doc comment says it
// exists for. A device declaring AptCapable satisfies it, because
// record.Base.Declares runs capability.Resolves and AptCapable is a
// child of PackageManagerCapable. Requiring a concrete capability here
// would defeat the point of the generic method entirely.
//
// No device type in this repository structurally implements AptCapable
// or DnfCapable today (see internal/catalog/pkg/apt's own doc comment
// for the full account), so this dispatcher is exercised in this
// package's own tests against a test-local device, not yet reachable
// end to end against a real inventory item. Its own logic (resolve,
// look up, capability-check the concrete method, invoke) is unaffected
// by that and is real, tested code, in exactly the position svc.go's
// dispatch was in before svc.windows.* existed: a generic layer with
// only one working destination so far.
package pkg

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

// paramName is the package to act on, ansible.builtin.package's own
// spelling and the same one the concrete methods take, so a task
// converted from pkg.install to pkg.apt.install needs no parameter edit.
//
// paramVersion pins the operation to a specific version. Left unset, an
// install or upgrade means "whatever the manager considers current."
const (
	paramName    = "name"
	paramVersion = "version"
)

// managerNamespace maps what a device says its package manager is onto
// the Collection namespace that implements it.
//
// It is data rather than a type switch for the reason the rest of this
// codebase gives for the same choice: supporting a new package manager
// is an entry here plus a namespace of concrete methods, never a change
// to the dispatch below. The keys are the values a device type's
// PackageManagerName returns.
var managerNamespace = map[string]string{
	"apt": "pkg.apt",
	"dnf": "pkg.dnf",
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

// dispatch resolves the concrete method for this device's package
// manager and invokes it.
//
// verb is the last segment of the FQCN ("install"), identical between
// the generic method and every concrete one, so the concrete name is the
// namespace plus the verb rather than a second lookup table that could
// disagree with the first.
//
// mode picks the concrete method's function (collection.Descriptor.MethodFor),
// so a check reaches the concrete method's check. A concrete method with
// no check support answers that it cannot check this call, which is the
// truth: this generic method can be checked on a device whose manager's
// method can, and not on one whose cannot.
func dispatch(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, verb string, mode collection.Mode) (collection.Result, error) {
	fqcn := "pkg." + verb

	if device == nil {
		return collection.Result{}, fmt.Errorf("%s: needs a target device to resolve a package manager from", fqcn)
	}

	// The device says which manager it runs. This is a property rather
	// than a capability because "has a package manager" and "runs APT
	// specifically" are different claims.
	manager, ok := device.(capability.PackageManagerCapable)
	if !ok {
		return collection.Result{}, fmt.Errorf("%s: device %q does not report a package manager, so there is nothing to dispatch to",
			fqcn, device.Name())
	}
	name := manager.PackageManagerName()

	namespace, known := managerNamespace[name]
	if !known {
		return collection.Result{}, fmt.Errorf("%s: device %q reports package manager %q, which this platform has no methods for (it knows: %s)",
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
	// requirement, PackageManagerCapable, before dispatch; invoking the
	// descriptor directly bypasses the executor, so nothing else would
	// ever check that the device satisfies the concrete method's
	// stricter one.
	for _, required := range desc.Manifest.RequiredCapabilities {
		if !device.HasCapability(required) {
			return collection.Result{}, fmt.Errorf("%s: device %q reports package manager %s, but does not have %s, which %s requires",
				fqcn, device.Name(), name, required, target)
		}
	}

	method, err := desc.MethodFor(mode)
	if err != nil {
		if mode == collection.ModeCheck {
			return collection.Result{}, collection.CannotCheck(fmt.Sprintf("device %q runs %s, and %s does not declare check support", device.Name(), name, target))
		}
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return method(ctx, rc, device, params)
}

// genericDoc builds the reference documentation shared by all three
// generic methods.
func genericDoc(summary, description string, examples []collection.Example, seeAlso []string) collection.Doc {
	return collection.Doc{
		Summary:     summary,
		Description: description,
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The package to act on. This is passed straight through to the concrete method for the device's package manager, so it means whatever that manager means by a package name: an APT package name on a Debian-family host, an RPM package name on a Red Hat-family one."},
			{Name: paramVersion, Type: "string", Description: "Pin to this exact version instead of whatever the package manager considers current. Passed straight through to the concrete method; leave unset to mean \"whatever version is current.\""},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: paramName, Type: "string", Returned: "always", Description: "The package this task acted on, as recorded by the concrete method that ran."},
			{Name: paramVersion, Type: "string", Returned: "always", Description: "The version left installed after this task, empty when the package is absent afterward. The exact value comes from the concrete method that ran."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What the package manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a package differs between package managers."},
		},
		Examples: examples,
		SeeAlso:  seeAlso,
	}
}

// genericManifest builds the manifest shared by all three generic
// methods, which differ only in their documentation and reversibility.
func genericManifest(reversibility collection.Reversibility, doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		// Empty rather than "ssh": which transport this reaches the
		// device over is the concrete method's business, and an APT host
		// and a DNF one do not have to agree on the answer, even though
		// both happen to be SSH today.
		SupportedTransports:  nil,
		RequiredCapabilities: []capability.Name{capability.NamePackageManager},
		ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
		PlatformTargets:      nil,
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		Reversibility:        reversibility,
		Doc:                  doc,
		// A check reaches the concrete method's check (dispatch), and
		// every concrete method this namespace dispatches to has one.
		SupportsCheck: true,
	}
}
