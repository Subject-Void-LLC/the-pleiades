// Package remotesvc reads and changes systemd unit state on a device
// over an existing SSH connection, and is the single place this platform
// does that.
//
// It exists for the reason pkg/remoteexec and pkg/remotefile exist. A
// Collection may import pkg/ and nothing else in this module, which
// internal/archtest enforces, so internal/catalog/svc/systemd and
// internal/catalog/svc cannot share a helper unless that helper lives
// here. Without it each of eleven methods would carry its own idea of
// how to ask systemd what a unit is doing, and the two packages could
// not agree even in principle: they are siblings under internal/, and
// neither may import the other.
//
// # Reading before writing
//
// Every function that changes something is paired with a read, and the
// methods above call the read first. That is the whole idempotence and
// rollback contract: a method reports changed only when the state it
// found differs from the state it wants, and the inverse it emits at run
// time is built from what that read returned. Once a unit is started the
// fact that it was stopped is gone, so the forward run is the only thing
// in a position to record it.
//
// # Why systemctl show, and not is-active
//
// The obvious implementation asks `systemctl is-active` and
// `systemctl is-enabled`, and it is wrong in two ways that matter.
//
// It cannot tell a stopped unit from one that does not exist.
// `is-active` answers "inactive" with exit status 3 for both, so a
// method built on it would report "already stopped, nothing to do" for a
// typo in a unit name and a runbook would go green having done nothing.
// `systemctl show` reports LoadState=not-found instead, which is a
// different answer from ActiveState=inactive, and Status below keeps them
// different.
//
// And two probes are two round trips whose answers can disagree, because
// the unit can change between them. One `show` returns load, active and
// enablement together, from one snapshot.
package remotesvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// State is what systemd currently reports about one unit.
//
// The three raw strings are kept alongside the booleans deliberately.
// A boolean answers the question a method asks ("should I start it"),
// and the raw string answers the question an operator asks when the
// booleans look wrong ("why is it not enabled" is answered by "static",
// or by "masked", and those need different fixes).
type State struct {
	// Unit is the unit name this state describes.
	Unit string

	// LoadState is systemd's own LoadState: "loaded", "not-found",
	// "masked", "bad-setting", or "error".
	LoadState string

	// ActiveState is systemd's own ActiveState: "active", "inactive",
	// "activating", "deactivating", "failed".
	ActiveState string

	// UnitFileState is systemd's own UnitFileState: "enabled",
	// "enabled-runtime", "disabled", "static", "masked", "indirect",
	// "generated", or empty when the unit has no [Install] section or
	// does not exist.
	UnitFileState string
}

// Exists reports whether systemd knows this unit at all.
//
// A masked unit exists: it is present and deliberately blocked, which is
// a different situation from a name systemd has never heard of, and the
// two need different errors.
func (s State) Exists() bool {
	return s.LoadState != "" && s.LoadState != "not-found"
}

// Active reports whether the unit is running right now.
//
// "activating" is deliberately NOT active. A unit part-way through
// starting has not finished, and reporting it active would let a method
// conclude "already started, nothing to do" about a unit that may still
// fail to come up.
func (s State) Active() bool { return s.ActiveState == "active" }

// Failed reports whether the unit is in systemd's failed state, which is
// worth separating from merely inactive when explaining why a start did
// not take.
func (s State) Failed() bool { return s.ActiveState == "failed" }

// Enabled reports whether the unit is set to start at boot.
//
// "enabled-runtime" counts, because it is enabled until the next reboot
// and a method asked to enable it would otherwise try again every run.
func (s State) Enabled() bool {
	return s.UnitFileState == "enabled" || s.UnitFileState == "enabled-runtime"
}

// Masked reports whether the unit is masked, meaning symlinked to
// /dev/null so it cannot be started at all.
//
// It is checked in both places systemd reports it: LoadState for a unit
// masked at the load level and UnitFileState for one masked in the unit
// file table. A start against a masked unit fails with a message about
// the mask rather than about the service, so a method is better off
// saying so before it tries.
func (s State) Masked() bool {
	return s.LoadState == "masked" || s.UnitFileState == "masked"
}

// Static reports whether the unit has no [Install] section, which means
// it cannot be enabled or disabled at all.
//
// This is the answer that most often looks like a bug. `systemctl enable`
// on a static unit fails, and the reason is not that anything is broken
// but that the unit is meant to be pulled in by another one.
func (s State) Static() bool { return s.UnitFileState == "static" }

// Map renders the State as the map a diff stat records.
//
// The keys are the contract: a method records them under diff.before and
// diff.after, so an inverse reading them later and a person reading
// --diff output see the same words.
func (s State) Map() map[string]any {
	return map[string]any{
		"unit":            s.Unit,
		"exists":          s.Exists(),
		"active":          s.Active(),
		"enabled":         s.Enabled(),
		"load_state":      s.LoadState,
		"active_state":    s.ActiveState,
		"unit_file_state": s.UnitFileState,
	}
}

// Status reads what systemd currently reports about unit.
//
// A unit systemd has never heard of is an ANSWER (State.Exists reports
// false), not an error: a method that treated it as a failure could
// never tell an operator which unit name was wrong, and one that treated
// it as "stopped" would report success for a typo.
func Status(ctx context.Context, conn *remoteexec.Conn, unit string) (State, error) {
	quoted := remoteexec.QuoteArg(unit)
	cmd := "systemctl show " + quoted +
		" --property=LoadState --property=ActiveState --property=UnitFileState"

	result, err := conn.Run(ctx, cmd)
	if err != nil {
		return State{}, fmt.Errorf("reading the state of %s: %w", unit, err)
	}
	if result.ExitCode != 0 {
		// `systemctl show` answers 0 even for a unit that does not exist,
		// so a non-zero status here means systemd could not be reached at
		// all: not installed, not running, or the account cannot talk to
		// it. Naming that is more useful than reporting the unit absent.
		return State{}, fmt.Errorf("reading the state of %s: systemctl exited %d: %s",
			unit, result.ExitCode, firstLine(result.Stderr))
	}

	return parseShow(unit, result.Stdout), nil
}

// parseShow turns `systemctl show` output into a State.
//
// It reads KEY=value lines by key rather than by position, and that is
// deliberate: `--value` would print bare values in an order this code
// would then depend on, and a systemd version that reordered them would
// silently swap ActiveState and UnitFileState rather than failing.
//
// An unknown key is ignored rather than refused, since a future systemd
// printing more than was asked for is not this function's problem.
func parseShow(unit, stdout string) State {
	state := State{Unit: unit}
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			state.LoadState = value
		case "ActiveState":
			state.ActiveState = value
		case "UnitFileState":
			state.UnitFileState = value
		}
	}
	return state
}

// The verbs this package can send. Kept as one table so every operation
// is spelled once and a method names the operation rather than a string.
const (
	opStart        = "start"
	opStop         = "stop"
	opRestart      = "restart"
	opEnable       = "enable"
	opDisable      = "disable"
	opDaemonReload = "daemon-reload"
)

// Start starts unit now, without changing whether it starts at boot.
func Start(ctx context.Context, conn *remoteexec.Conn, unit string) error {
	return run(ctx, conn, opStart, unit)
}

// Stop stops unit now, without changing whether it starts at boot.
func Stop(ctx context.Context, conn *remoteexec.Conn, unit string) error {
	return run(ctx, conn, opStop, unit)
}

// Restart restarts unit, starting it if it was not running.
//
// This is `systemctl restart` rather than `try-restart`, matching what
// ansible.builtin.systemd's state=restarted does: an operator asking for
// a restart wants the unit running afterward.
func Restart(ctx context.Context, conn *remoteexec.Conn, unit string) error {
	return run(ctx, conn, opRestart, unit)
}

// Enable makes unit start at boot, without starting it now.
func Enable(ctx context.Context, conn *remoteexec.Conn, unit string) error {
	return run(ctx, conn, opEnable, unit)
}

// Disable stops unit starting at boot, without stopping it now.
func Disable(ctx context.Context, conn *remoteexec.Conn, unit string) error {
	return run(ctx, conn, opDisable, unit)
}

// DaemonReload makes systemd re-read every unit file on disk.
//
// It takes no unit, because it is not about one: it is the step that
// makes a unit file written by an earlier task visible to systemd at
// all.
func DaemonReload(ctx context.Context, conn *remoteexec.Conn) error {
	result, err := conn.Run(ctx, "systemctl "+opDaemonReload)
	if err != nil {
		return fmt.Errorf("systemctl %s: %w", opDaemonReload, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("systemctl %s exited %d: %s", opDaemonReload, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}

// run sends one systemctl verb against one unit.
//
// The unit name is quoted with remoteexec.QuoteArg before it reaches the
// remote shell, so a name arriving from a runbook variable is a name
// rather than syntax. Nothing else is interpolated.
func run(ctx context.Context, conn *remoteexec.Conn, op, unit string) error {
	result, err := conn.Run(ctx, "systemctl "+op+" "+remoteexec.QuoteArg(unit))
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w", op, unit, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("systemctl %s %s exited %d: %s", op, unit, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}

// firstLine returns the first line of s, trimmed, so an error message
// carries the reason rather than a page of output.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "no output"
	}
	return s
}
