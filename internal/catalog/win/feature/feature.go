// Package feature implements the two "win.feature.*" Collection methods:
// install and remove.
//
// # One code path, two registrations
//
// Mirrors internal/catalog/svc/windows's own shape: read the feature's
// current state, decide whether anything needs doing, do it, and record
// what it was. That shape lives once, in runFeatureOp below, and each
// method file is a registration plus the two answers that make it
// different: whether the feature is already in the wanted state, and
// which pkg/winrmdism verb to send.
//
// # Why the inverse here is simpler than svc.windows's
//
// svc.windows.enable/disable have to worry about a third start type,
// Manual, that neither of them targets, so their own inverse only fires
// on an exact opposite transition. DISM's own feature states have no
// equivalent third state this namespace manages around: install only
// ever produces Enabled, remove only ever produces Disabled, and
// FeatureState.Enabled/DisabledState are each other's exact complement
// for the transitions these two methods make. The inverse here is
// therefore unconditional on any change, the same shape
// svc.systemd.start/stop already use.
package feature

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// paramName is the feature to act on, matching ansible.windows.win_
// optional_feature's own "name" parameter.
const paramName = "name"

// Stat names this namespace emits.
const (
	statName           = "name"
	statRebootRequired = "reboot_required"
)

// statusFunc, enableFunc and disableFunc are the seams runFeatureOp reads
// and changes state through.
//
// There is no real Windows host in this environment and no fake DISM
// worth building, the identical reasoning internal/catalog/svc/windows's
// own statusFunc doc comment gives. This package's tests swap these to
// canned answers instead; the live device belongs to the gated Release
// Gate (cmd/pleiades/winrm_service_feature_release_gate_test.go).
var (
	statusFunc  = winrmdism.Status
	enableFunc  = winrmdism.Enable
	disableFunc = winrmdism.Disable
)

// featureOp is what makes one feature-shaped method different from
// another. Everything else lives in runFeatureOp, so adding a verb is
// filling in this struct rather than writing a method.
type featureOp struct {
	// fqcn is the method's own name, used in every error it returns.
	fqcn string

	// converged reports whether the feature is already in the state this
	// operation produces, in which case nothing is sent at all.
	converged func(winrmdism.FeatureState) bool

	// apply sends the change and reports whether it needs a restart.
	apply func(ctx context.Context, session winrmdism.Session, logPath, name string) (winrmdism.ChangeResult, error)

	// inverse builds the instruction that undoes a run that changed
	// something. Unlike svc.windows's own inverse fields, this is
	// unconditional on the state found before -- see the package doc for
	// why that asymmetry is correct here and not there.
	inverse func(name string) (sdk.Inverse, bool)
}

// runFeatureOp is the body of every win.feature method that names a
// feature.
//
// Reading before writing, and refusing a feature name DISM has never
// heard of rather than reporting it disabled, are the same reasoning
// internal/catalog/svc/windows's own runServiceOp documents.
//
// mode is collection.ModeCheck for a check: the same read, the same
// refusal and the same convergence decision, and then nothing is sent
// (predictFeature).
func runFeatureOp(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, op featureOp, mode collection.Mode) (collection.Result, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	session, logPath, err := winrmSession(rc, device, op.fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := statusFunc(ctx, session, logPath, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if !before.Exists {
		return collection.Result{}, fmt.Errorf("%s: DISM on device %q does not recognize a feature called %q: check the name",
			op.fqcn, device.Name(), name)
	}

	if mode == collection.ModeCheck {
		return predictFeature(rc, op, name, before)
	}

	changed := false
	rebootRequired := false
	if op.converged == nil || !op.converged(before) {
		result, err := op.apply(ctx, session, logPath, name)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
		changed = true
		rebootRequired = result.RebootRequired
	}

	// The "after" half is read back from the device rather than assumed,
	// the same reason runServiceOp does: an "Enable Pending" or partial
	// state would otherwise be misreported as the state this task asked
	// for.
	after := before
	if changed {
		if after, err = statusFunc(ctx, session, logPath, name); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
	}

	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := rc.SetStat(statRebootRequired, rebootRequired); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	if changed && op.inverse != nil {
		if inverse, ok := op.inverse(name); ok {
			if err := sdk.RecordInverse(rc, inverse); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
			}
		}
	}

	return collection.Result{Changed: changed}, nil
}

// predictFeature is a check's answer for a feature found as before:
// whether a real run would send its verb, the diff it would record, and
// nothing sent.
//
// When the feature would change, the diff's after half leaves out the
// state. A real run reads it back rather than assuming it, because DISM
// leaves a feature that needs a restart Enable Pending or Disable Pending
// rather than Enabled or Disabled, and only running the change says which.
// reboot_required is left out for the same reason. Nothing is undone by a
// check, so no inverse is recorded.
func predictFeature(rc sdk.RunbookContext, op featureOp, name string, before winrmdism.FeatureState) (collection.Result, error) {
	changed := op.converged == nil || !op.converged(before)
	after := before.Map()
	if changed {
		delete(after, "state")
	}
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	return collection.Result{Changed: changed}, nil
}

// winrmSession resolves the device's WinRM target, this run's
// credential, and its DISM log path into what every pkg/winrmdism call
// needs.
//
// Two capabilities are checked, not one: WinRMCapable for the transport
// (matching internal/catalog/svc/windows's own winrmSession) and
// WindowsFeatureCapable for DISMLogPath, which this method's manifest
// already requires and engine.checkMethodCapabilities already verified
// before Invoke runs. Reaching either error below means a device
// declared a capability without structurally implementing it, which
// HasCapability is supposed to make impossible.
func winrmSession(rc sdk.RunbookContext, device inventory.InventoryItem, fqcn string) (winrmdism.Session, string, error) {
	if device == nil {
		return winrmdism.Session{}, "", fmt.Errorf("%s: needs a target device", fqcn)
	}
	winrmDev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmdism.Session{}, "", fmt.Errorf("%s: device %q declares %s but does not implement its accessors",
			fqcn, device.Name(), capability.NameWinRM)
	}
	featureDev, ok := device.(capability.WindowsFeatureCapable)
	if !ok {
		return winrmdism.Session{}, "", fmt.Errorf("%s: device %q declares %s but does not implement its accessors",
			fqcn, device.Name(), capability.NameWindowsFeature)
	}

	// The credential vocabulary is read by pkg/winrmexec rather than
	// spelled out here, so a credential form added there reaches these
	// methods without an edit.
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return winrmdism.Session{}, "", fmt.Errorf("%s: %w", fqcn, err)
	}
	session := winrmdism.Session{
		Target: winrmexec.Target{Host: winrmDev.WinRMHost(), Port: winrmDev.WinRMPort()},
		Auth:   auth,
	}
	return session, featureDev.DISMLogPath(), nil
}

// featureDoc builds the reference documentation shared by the two
// feature-shaped methods, which differ only in their prose.
func featureDoc(summary, description string, examples []collection.Example, seeAlso []string) collection.Doc {
	return collection.Doc{
		Summary:     summary,
		Description: description,
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The Windows optional feature or role's DISM feature name, such as IIS-WebServerRole, not its display name. The feature must be one DISM recognizes: a name it does not is refused rather than reported as already the target state, since that is nearly always a typo."},
		},
		Returns: []collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The feature this task acted on."},
			{Name: statRebootRequired, Type: "bool", Returned: "always", Description: "Whether DISM reported that a restart is needed for this change to take full effect (its own ERROR_SUCCESS_REBOOT_REQUIRED). False on a run that changed nothing."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What DISM reported about the feature before this task and after it, each holding exists and state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
		},
		Examples: examples,
		SeeAlso:  seeAlso,
	}
}
