// Package systemd implements the six "svc.systemd.*" Collection methods:
// start, stop, restart, enable, disable and daemon_reload.
//
// # One code path, six registrations
//
// Five of the six do the same four things in the same order against a
// different verb: read the unit's current state, decide whether anything
// needs doing, do it, and record what it was. Written out six times that
// is six chances for one of them to forget the read and report changed
// forever, which is the defect this whole namespace is most prone to. So
// the shape lives once, in runUnitOp below, and each method file is a
// registration plus the three answers that make it different: whether the
// unit is already in the wanted state, which systemctl verb to send, and
// what undoes it.
//
// daemon_reload is deliberately NOT expressed through that shape. It
// takes no unit, has no state to read, and has no inverse, so forcing it
// through a unit-shaped helper would mean inventing all three.
//
// # Check mode is the same code path with the writes left out
//
// Every method here also answers collection.ModeCheck: it reports what a
// real run would change without changing it. That answer comes from
// runUnitOp too, called with the other mode, rather than from a second
// function that re-implements the comparison. A dry run that decided
// "already running" by a different rule from the real run would be
// worse than no dry run, because an operator reads it precisely to
// decide whether to run the real thing. So the check parses the same
// params, opens the same connection, reads the same state and refuses
// the same unknown, masked or static units, and differs in exactly three
// places: it sends no systemctl verb, it predicts the after state rather
// than reading it back, and it records no inverse, since nothing was done
// that could be undone.
//
// # Starting is not enabling
//
// These map to systemd's own distinction rather than blurring it, exactly
// as ansible.builtin.systemd does. start and stop change what is running
// right now and survive nothing. enable and disable change what happens
// at boot and do not touch the running system. A task that wants both
// says both, in two tasks, because a single method doing both could not
// report which half changed.
package systemd

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotesvc"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// paramName is the unit to act on. It is "name" rather than "unit"
// because that is ansible.builtin.systemd's own spelling, and a person
// converting a playbook should be renaming nothing.
const paramName = "name"

// Stat names this namespace emits. "name" mirrors the parameter so a
// later task's when_cel reads the same word it wrote.
const (
	statName = "name"
)

// unitOp is what makes one unit-shaped method different from another.
//
// Everything else about the five of them is identical and lives in
// runUnitOp, so adding a verb is filling in this struct rather than
// writing a method.
type unitOp struct {
	// fqcn is the method's own name, used in every error it returns.
	fqcn string

	// converged reports whether the unit is already in the state this
	// operation produces, in which case nothing is sent at all.
	//
	// Nil means the operation is never converged. restart is the only
	// one: restarting a running unit is not a no-op, it is the point.
	converged func(remotesvc.State) bool

	// apply sends the change.
	apply func(context.Context, *remoteexec.Conn, string) error

	// predict returns the state a successful apply leaves the unit in,
	// worked out from the state found rather than read back.
	//
	// A check records it as the after half of its diff, because a check
	// sends nothing and so has nothing to read back. It is the operation's
	// own meaning written as data (start leaves the unit active, enable
	// leaves it enabled), and it is called only for a unit that is not
	// already converged, since a converged unit's after state is its
	// before state.
	predict func(remotesvc.State) remotesvc.State

	// inverse builds the instruction that undoes a run that changed
	// something, or reports false when this operation emits none.
	//
	// It takes the state the run FOUND, because that is the only thing
	// that can say what to go back to, and it is gone the moment apply
	// succeeds.
	inverse func(unit string, before remotesvc.State) (sdk.Inverse, bool)

	// needsInstallSection marks the operations that write the boot-time
	// unit file table (enable and disable). A static unit has no
	// [Install] section, so systemctl cannot enable or disable it, and
	// saying that plainly beats relaying systemd's own message about a
	// symlink.
	needsInstallSection bool

	// refusesMasked marks the operations that cannot work on a masked
	// unit. Starting one fails with a message about the mask rather than
	// about the service, so this refuses before sending anything.
	refusesMasked bool
}

// runUnitOp is the body of every svc.systemd method that names a unit.
//
// # Why the read comes first, always
//
// A method that sent its verb unconditionally would work, and would
// report changed on every run forever, which is indistinguishable from a
// method that is genuinely fixing something every time. Reading first is
// what makes "changed" mean something, and it is also the only chance to
// capture what the state was: once the unit is started, the fact that it
// was stopped exists nowhere.
//
// # What counts as a failure before anything is sent
//
// Three conditions are refused up front rather than relayed from
// systemctl, because in each case systemd's own message describes a
// symptom and the useful message names the cause:
//
//   - The unit does not exist. This is almost always a typo, and
//     systemctl's answer for a stop is "inactive" with a success-shaped
//     exit, so a method trusting it would report "already stopped" for a
//     unit name that has never existed.
//   - The unit is masked, for the operations that need it not to be.
//   - The unit is static, for enable and disable.
//
// # The two modes
//
// mode is collection.ModeExecute for a real run and collection.ModeCheck
// for a dry run. Everything above holds for both, refusals included, so a
// check of a typo'd unit fails exactly the way the real run would. The
// check then skips the three steps that act or depend on acting: apply,
// the read-back, and the inverse. Its Changed is the same !converged a
// real run acts on, and its diff's after half is op.predict's answer.
func runUnitOp(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, op unitOp, mode collection.Mode) (collection.Result, error) {
	unit, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, op.fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	before, err := remotesvc.Status(ctx, conn, unit)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	if err := checkUnitUsable(op, unit, before); err != nil {
		return collection.Result{}, err
	}

	// The one decision both modes share: a unit already in the wanted
	// state needs nothing, and anything else is a change.
	changed := op.converged == nil || !op.converged(before)
	check := mode == collection.ModeCheck

	after := before
	switch {
	case !changed:
		// A converged run skips both the write and the read-back, since
		// nothing was written and a second read could only return what
		// the first already did. A converged check does the same.
	case check:
		// Nothing is sent, so there is nothing to read back. The after
		// half is what a successful real run would leave, which is the
		// "what would this do" a dry run exists to answer. It is a
		// prediction and says so only by being a check's diff: a unit that
		// would start and then fail cannot be foreseen without starting
		// it.
		after = op.predict(before)
	default:
		if err := op.apply(ctx, conn, unit); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}

		// The "after" half is read back from the device rather than
		// assumed from what was asked for. They differ more often than
		// they should: a unit that starts and immediately fails reports
		// ActiveState=failed, and a diff assembled from the request would
		// claim it is active.
		if after, err = remotesvc.Status(ctx, conn, unit); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
		}
	}

	if err := rc.SetStat(statName, unit); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
	}

	// An inverse is emitted only by a run that changed something. A
	// converged run records nothing, which is how it says that undoing it
	// means doing nothing. A check records nothing either, whatever it
	// predicted: it changed nothing, and the engine refuses a check result
	// that carries an undo instruction for a change that never happened.
	if changed && !check && op.inverse != nil {
		if inverse, ok := op.inverse(unit, before); ok {
			if err := sdk.RecordInverse(rc, inverse); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", op.fqcn, err)
			}
		}
	}

	return collection.Result{Changed: changed}, nil
}

// checkUnitUsable refuses the states this operation cannot act on,
// naming the cause rather than relaying a symptom.
func checkUnitUsable(op unitOp, unit string, state remotesvc.State) error {
	if !state.Exists() {
		return fmt.Errorf("%s: systemd does not know a unit called %q on this device (load state %q): check the name, including its suffix, since nginx and nginx.service are not always the same unit",
			op.fqcn, unit, state.LoadState)
	}
	if op.refusesMasked && state.Masked() {
		return fmt.Errorf("%s: unit %q is masked, which is a deliberate block on it running at all: unmask it before this task can act on it",
			op.fqcn, unit)
	}
	if op.needsInstallSection && state.Static() {
		return fmt.Errorf("%s: unit %q is static, meaning its unit file has no [Install] section, so it cannot be enabled or disabled: it is meant to be pulled in by another unit rather than started at boot on its own",
			op.fqcn, unit)
	}
	return nil
}

// The systemd state values a prediction writes. They are systemd's own
// spellings, the same ones remotesvc.State carries raw, so a predicted
// after half reads exactly like one read back from a device.
const (
	activeStateActive   = "active"
	activeStateInactive = "inactive"
	unitFileEnabled     = "enabled"
	unitFileDisabled    = "disabled"
)

// predictActiveState returns an op.predict for an operation that changes
// what is running right now (start, stop, restart). It sets ActiveState
// and leaves the boot-time setting alone, because that is exactly what
// those verbs do and the reason this namespace keeps them apart from
// enable and disable.
func predictActiveState(active string) func(remotesvc.State) remotesvc.State {
	return func(s remotesvc.State) remotesvc.State {
		s.ActiveState = active
		return s
	}
}

// predictUnitFileState returns an op.predict for an operation that
// changes what happens at boot (enable, disable). It sets UnitFileState
// and leaves the running system alone, for the same reason in reverse.
func predictUnitFileState(state string) func(remotesvc.State) remotesvc.State {
	return func(s remotesvc.State) remotesvc.State {
		s.UnitFileState = state
		return s
	}
}

// unitDoc builds the reference documentation shared by the five
// unit-shaped methods, which differ only in their prose.
func unitDoc(summary, description string, returns []collection.ReturnField, examples []collection.Example, seeAlso []string) collection.Doc {
	return collection.Doc{
		Summary:     summary,
		Description: description,
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: append([]collection.ReturnField{
			{Name: statName, Type: "string", Returned: "always", Description: "The unit this task acted on."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
		}, returns...),
		Examples: examples,
		SeeAlso:  seeAlso,
	}
}
