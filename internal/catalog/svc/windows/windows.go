// Package windows implements the five "svc.windows.*" Collection methods:
// start, stop, restart, enable and disable.
//
// # One code path, five registrations
//
// Mirrors internal/catalog/svc/systemd's own shape exactly: read the
// service's current state, decide whether anything needs doing, do it,
// and record what it was. That shape lives once, in runServiceOp below,
// and each method file is a registration plus the three answers that
// make it different: whether the service is already in the wanted
// state, which pkg/winrmsvc verb to send, and what undoes it.
//
// There is no daemon_reload counterpart here, unlike svc.systemd. The
// Service Control Manager has no "reread every service definition from
// disk" operation for this platform to expose; a service's configuration
// changes when something reconfigures it, not on a separate reload step.
//
// # Starting is not enabling, same as systemd
//
// start/stop change what is running right now; enable/disable change
// the start type and do not touch the running service. A task wanting
// both says both, in two tasks, for the identical reason svc.systemd
// keeps them separate: a single method doing both could not report which
// half changed.
package windows

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmsvc"
)

// paramName is the service to act on, matching svc.systemd's own "name"
// spelling (ansible.builtin.systemd's parameter name) rather than
// ansible.windows.win_service's "name" -- the two already agree, so
// there is nothing to reconcile.
const paramName = "name"

// statName mirrors the parameter, so a later task's when_cel reads the
// same word it wrote.
const statName = "name"

// statusFunc is the seam runServiceOp reads state through.
//
// pkg/winrmexec's own test file explains why: there is no real Windows
// host in this environment and no fake WinRM server worth building, so
// the one thing that genuinely needs a live Service Control Manager
// belongs in the gated Release Gate
// (cmd/pleiades/winrm_service_feature_release_gate_test.go), not here.
// This package's own tests instead swap statusFunc to a canned answer,
// the same role remoteexectest's fake systemctl plays for
// pkg/remotesvc's tests, adapted to a transport with no in-process fake
// to run against.
var statusFunc = winrmsvc.Status

// startFunc, stopFunc, restartFunc, enableFunc and disableFunc are the
// same seam for the five change operations, each verb file's own apply
// field. Swapping these alongside statusFunc lets a test exercise the
// real, registered Start/Stop/Restart/Enable/Disable functions -- including
// each one's own inverse closure -- without any of them dialing out, so a
// test genuinely proves what the registered method does rather than a
// hand-copied stand-in for it.
var (
	startFunc   = winrmsvc.Start
	stopFunc    = winrmsvc.Stop
	restartFunc = winrmsvc.Restart
	enableFunc  = winrmsvc.Enable
	disableFunc = winrmsvc.Disable
)

// serviceOp is what makes one service-shaped method different from
// another. Everything else lives in runServiceOp, so adding a verb is
// filling in this struct rather than writing a method.
type serviceOp struct {
	// fqcn is the method's own name, used in every error it returns.
	fqcn string

	// converged reports whether the service is already in the state this
	// operation produces, in which case nothing is sent at all.
	//
	// Nil means the operation is never converged. restart is the only
	// one, mirroring svc.systemd.restart exactly: restarting a running
	// service is not a no-op, it is the point.
	converged func(winrmsvc.State) bool

	// apply sends the change.
	apply func(context.Context, winrmsvc.Session, string) error

	// refusesDisabled marks the operations that cannot act on a service
	// whose start type is Disabled: the Service Control Manager refuses
	// to start one, so start and restart name the cause up front rather
	// than relaying Start-Service's own generic failure.
	refusesDisabled bool

	// inverse builds the instruction that undoes a run that changed
	// something, or reports false when this operation emits none for the
	// state it found.
	//
	// It takes the state the run FOUND, because that is the only thing
	// that can say what to go back to, and it is gone the moment apply
	// succeeds.
	inverse func(name string, before winrmsvc.State) (sdk.Inverse, bool)

	// predict is what apply would leave a service found as before, for a
	// check: the one field apply changes set to what it sets.
	predict func(before winrmsvc.State) winrmsvc.State
}

// runServiceOp is the body of every svc.windows method that names a
// service.
//
// # Why the read comes first
//
// Sending the verb unconditionally would work and would report changed
// on every run forever, indistinguishable from a method genuinely fixing
// something every time. Reading first is what makes "changed" mean
// something, and it is the only chance to capture what the state was:
// once the service is started, the fact that it was stopped exists
// nowhere.
//
// # A service the SCM has never heard of is refused, not reported stopped
//
// The same reasoning pkg/remotesvc and svc.systemd apply: a typo in a
// service name should not read as "already stopped."
//
// mode is collection.ModeCheck for a check: the same read, the same
// refusals and the same convergence decision, then op.predict in place of
// op.apply and the read-back.
func runServiceOp(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, op serviceOp, mode collection.Mode) (collection.Result, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	session, err := winrmSession(rc, device, op.fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := statusFunc(ctx, session, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := checkServiceUsable(op, device, name, before); err != nil {
		return collection.Result{}, err
	}

	if mode == collection.ModeCheck {
		changed := op.converged == nil || !op.converged(before)
		after := before
		if changed {
			after = op.predict(before)
		}
		if err := rc.SetStat(statName, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
		if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
		return collection.Result{Changed: changed}, nil
	}

	changed := false
	if op.converged == nil || !op.converged(before) {
		if err := op.apply(ctx, session, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
		changed = true
	}

	// The "after" half is read back from the device rather than assumed
	// from what was asked for, the same reason runUnitOp does: a service
	// that starts and immediately crashes reports Status=Stopped, and a
	// diff assembled from the request would claim it is running.
	after := before
	if changed {
		if after, err = statusFunc(ctx, session, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
	}

	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	if changed && op.inverse != nil {
		if inverse, ok := op.inverse(name, before); ok {
			if err := sdk.RecordInverse(rc, inverse); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
			}
		}
	}

	return collection.Result{Changed: changed}, nil
}

// checkServiceUsable refuses the states this operation cannot act on,
// naming the cause rather than relaying a symptom.
func checkServiceUsable(op serviceOp, device inventory.InventoryItem, name string, state winrmsvc.State) error {
	if !state.Exists {
		return fmt.Errorf("%s: the Service Control Manager on device %q does not know a service called %q: check the name",
			op.fqcn, device.Name(), name)
	}
	if op.refusesDisabled && state.Disabled() {
		return fmt.Errorf("%s: service %q's start type is Disabled, which is a deliberate block on it starting at all: "+
			"run svc.windows.enable (or a start type other than Disabled) before this task can act on it",
			op.fqcn, name)
	}
	return nil
}

// winrmSession resolves the device's WinRM target and this run's
// credential into the Session every pkg/winrmsvc call needs.
//
// The capability is already required by this method's manifest and
// checked by engine.checkMethodCapabilities before Invoke runs, matching
// exec/winrm/shell.go's own winrmTarget: reaching the error below means a
// device declared WinRMCapable without structurally implementing it,
// which HasCapability is supposed to make impossible.
func winrmSession(rc sdk.RunbookContext, device inventory.InventoryItem, fqcn string) (winrmsvc.Session, error) {
	if device == nil {
		return winrmsvc.Session{}, fmt.Errorf("%s: needs a target device", fqcn)
	}
	dev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmsvc.Session{}, fmt.Errorf("%s: device %q declares %s but does not implement its accessors",
			fqcn, device.Name(), capability.NameWinRM)
	}

	// The credential vocabulary is read by pkg/winrmexec rather than
	// spelled out here, so a credential form added there reaches these
	// methods without an edit.
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return winrmsvc.Session{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return winrmsvc.Session{
		Target: winrmexec.Target{Host: dev.WinRMHost(), Port: dev.WinRMPort()},
		Auth:   auth,
		// The device's own pinned authority and server name, if it names any.
		Options: winrmexec.WithDeviceTLS(winrmexec.Options{}, devicetls.For(device)),
	}, nil
}

// serviceDoc builds the reference documentation shared by the five
// service-shaped methods, which differ only in their prose.
func serviceDoc(summary, description string, returns []collection.ReturnField, examples []collection.Example, seeAlso []string) collection.Doc {
	return collection.Doc{
		Summary:     summary,
		Description: description,
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The service this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
		}, returns...),
		Examples: examples,
		SeeAlso:  seeAlso,
	}
}
