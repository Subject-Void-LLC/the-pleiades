// Package capability implements the two "win.capability.*" Collection
// methods, install and remove: a Windows capability (a feature on demand,
// Get-WindowsCapability's noun) made present or absent with DISM.
//
// It has internal/catalog/win/feature's shape: read the capability's state,
// decide whether anything needs doing, do it, read it back and record what
// it was, once in runCapabilityOp, with each method file supplying what
// makes it different. Two things differ from a feature. The change runs as
// SYSTEM, since the Windows Update agent a capability's payload comes from
// refuses a WinRM logon (pkg/winrmdism's capability.go). And a capability
// left pending a restart counts as the verb's own state, done but for the
// restart: a later run reports no change and reboot_required true, so a
// rerun before the restart sends nothing and still says what finishes it.
package capability

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Parameters.
const (
	paramName    = "name"
	paramSource  = "source"
	paramTimeout = "timeout"
)

// Stats this namespace emits.
const (
	statName           = "name"
	statRebootRequired = "reboot_required"
)

// defaultTimeout bounds DISM's run when a task names none: an hour, since a
// large capability fetched from Windows Update takes tens of minutes.
const defaultTimeout = 3600

// statusFunc, addFunc and removeFunc are the seams runCapabilityOp reads
// and changes state through; a test replaces them. The live device belongs
// to the lab's win-check, where both methods were run (docs/design/os-checks.md).
var (
	statusFunc = winrmdism.CapabilityStatus
	addFunc    = winrmdism.AddCapability
	removeFunc = winrmdism.RemoveCapability
)

// request is one task's parameters, read and checked before any contact.
type request struct {
	name    string
	source  string
	timeout time.Duration
}

// capabilityOp is what makes install different from remove.
type capabilityOp struct {
	// fqcn is the method's name, in every error it returns.
	fqcn string
	// source says whether the method takes a source parameter.
	source bool
	// done reports whether the capability is already in the state this
	// operation produces, pending a restart included.
	done func(winrmdism.CapabilityState) bool
	// pending reports whether that state is waiting for a restart.
	pending func(winrmdism.CapabilityState) bool
	// apply sends the change.
	apply func(ctx context.Context, session winrmdism.Session, logPath string, r request) (winrmdism.ChangeResult, error)
	// inverse is the instruction undoing a run that changed the capability.
	inverse func(name string) sdk.Inverse
}

// readRequest reads and checks a task's parameters; withSource says
// whether the method takes a source.
func readRequest(params map[string]any, withSource bool) (request, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return request{}, err
	}
	if err := winrmdism.CheckCapabilityName(name); err != nil {
		return request{}, err
	}
	r := request{name: name, timeout: defaultTimeout * time.Second}
	if withSource {
		if r.source, err = sdk.OptionalStringParam(params, paramSource); err != nil {
			return request{}, err
		}
	}
	seconds, given, err := sdk.IntParam(params, paramTimeout)
	if err != nil {
		return request{}, err
	}
	if given {
		if seconds <= 0 {
			return request{}, fmt.Errorf("%s must be more than 0 seconds", paramTimeout)
		}
		r.timeout = time.Duration(seconds) * time.Second
	}
	return r, nil
}

// runCapabilityOp is the body of both methods. mode is collection.ModeCheck
// for a check: the same reads and refusals, then nothing sent.
func runCapabilityOp(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, op capabilityOp, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	r, err := readRequest(params, op.source)
	if err != nil {
		return fail(err)
	}
	session, logPath, err := winrmSession(rc, device)
	if err != nil {
		return fail(err)
	}
	before, err := statusFunc(ctx, session, logPath, r.name)
	if err != nil {
		return fail(err)
	}
	if !before.Exists {
		return fail(fmt.Errorf("DISM on device %q does not recognize a capability called %q: check the name (Get-WindowsCapability -Online lists them)", device.Name(), r.name))
	}

	changed := !op.done(before)
	rebootRequired := op.pending(before)
	after := before
	if mode == collection.ModeCheck {
		// A real run reads the state back rather than assuming it, since DISM
		// decides between the finished and pending states only as it runs; a
		// check leaves both out of the after half when it would change.
		predicted := before.Map()
		if changed {
			delete(predicted, "state")
		}
		if err := record(rc, r.name, rebootRequired, before.Map(), predicted); err != nil {
			return fail(err)
		}
		return collection.Result{Changed: changed}, nil
	}
	if changed {
		result, err := op.apply(ctx, session, logPath, r)
		if err != nil {
			return fail(err)
		}
		if after, err = statusFunc(ctx, session, logPath, r.name); err != nil {
			return fail(err)
		}
		rebootRequired = result.RebootRequired || op.pending(after)
	}
	if err := record(rc, r.name, rebootRequired, before.Map(), after.Map()); err != nil {
		return fail(err)
	}
	if changed {
		if err := sdk.RecordInverse(rc, op.inverse(r.name)); err != nil {
			return fail(err)
		}
	}
	return collection.Result{Changed: changed}, nil
}

// record sets the name, reboot_required and the diff.
func record(rc sdk.RunbookContext, name string, rebootRequired bool, before, after map[string]any) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	if err := rc.SetStat(statRebootRequired, rebootRequired); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before, After: after})
}

// winrmSession resolves the device's WinRM target, this run's credential
// and its DISM log path. WindowsFeatureCapable carries the log path, which
// the manifest requires and the engine verified before Invoke; a device
// reaching either error below declared a capability without its accessors.
func winrmSession(rc sdk.RunbookContext, device inventory.InventoryItem) (winrmdism.Session, string, error) {
	if device == nil {
		return winrmdism.Session{}, "", fmt.Errorf("needs a target device")
	}
	winrmDev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmdism.Session{}, "", fmt.Errorf("device %q declares %s but does not implement its accessors", device.Name(), capability.NameWinRM)
	}
	featureDev, ok := device.(capability.WindowsFeatureCapable)
	if !ok {
		return winrmdism.Session{}, "", fmt.Errorf("device %q declares %s but does not implement its accessors", device.Name(), capability.NameWindowsFeature)
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return winrmdism.Session{}, "", err
	}
	return winrmdism.Session{
		Target:  winrmexec.Target{Host: winrmDev.WinRMHost(), Port: winrmDev.WinRMPort()},
		Auth:    auth,
		Options: winrmexec.WithDeviceTLS(winrmexec.Options{}, devicetls.For(device)),
	}, featureDev.DISMLogPath(), nil
}
