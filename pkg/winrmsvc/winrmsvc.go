// Package winrmsvc reads and changes Windows Service Control Manager
// state on a device over WinRM, and is the single place this platform
// does that.
//
// It exists for the reason pkg/remotesvc exists, adapted to a transport
// that has no persistent connection to share. A Collection may import
// pkg/ and nothing else in this module, which internal/archtest
// enforces, so internal/catalog/svc/windows's five methods cannot each
// carry their own idea of how to ask the SCM what a service is doing.
//
// # Session, not Conn
//
// pkg/remotesvc's functions take a *remoteexec.Conn because SSH sessions
// are worth keeping open across a method's read-then-act calls.
// pkg/winrmexec has no equivalent: Run dials, authenticates and closes
// per call, because the credential is a call argument rather than
// package state (see winrmexec's own doc comment). Session below is not
// a connection, only a bundle of the three values every call in this
// package needs, so a caller passes them once per method invocation
// instead of three times per call.
//
// # Reading before writing, and one round trip
//
// Every function that changes something is paired with a read, and
// internal/catalog/svc/windows calls the read first, for the identical
// reason pkg/remotesvc's own doc comment gives: a method reports changed
// only when the state it found differs from the state it wants, and the
// inverse it emits is built from what that read returned.
//
// Status asks for a service's existence, run state and start type in one
// PowerShell round trip rather than two, because the service can change
// between two separate probes and a method deciding from disagreeing
// answers would be wrong in a way neither answer alone was.
//
// # Why Get-Service -ErrorAction SilentlyContinue, not a thrown error
//
// A service name PowerShell has never heard of and a service that exists
// but is stopped are different answers, and Get-Service's default
// behavior collapses that distinction into "write a non-terminating
// error either way." This package asks it not to, and instead reads
// $null back, so State.Exists can say which happened -- the identical
// LoadState=not-found vs. ActiveState=inactive distinction
// pkg/remotesvc's own package doc argues for on the systemd side.
package winrmsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Session bundles what every call in this package needs to reach one
// Windows device. See the package doc for why this is a config bundle
// rather than a live connection.
type Session struct {
	Target  winrmexec.Target
	Auth    winrmexec.Auth
	Options winrmexec.Options
}

// State is what the Service Control Manager currently reports about one
// service.
//
// Status and StartType are kept as the raw strings PowerShell reports
// (rather than only the booleans a method asks) for the same reason
// remotesvc.State keeps LoadState/ActiveState/UnitFileState: the boolean
// answers the question a method asks, and the raw string answers the
// question an operator asks when the boolean looks wrong.
type State struct {
	// Name is the service name this state describes.
	Name string

	// Exists reports whether the Service Control Manager knows a service
	// by this name at all.
	Exists bool

	// Status is Get-Service's own Status.ToString(): "Running", "Stopped",
	// "Paused", "StartPending", "StopPending", "ContinuePending",
	// "PausePending", or empty when Exists is false.
	Status string

	// StartType is Get-Service's own StartType.ToString(): "Automatic",
	// "Manual", "Disabled", "Boot" or "System" (the latter two only for
	// driver services), or empty when Exists is false.
	StartType string
}

// Running reports whether the service is running right now.
//
// "StartPending" deliberately does NOT count as running, the same
// "activating is not active" rule remotesvc.State.Active applies: a
// service part-way through starting has not finished, and reporting it
// running would let a method conclude "already started" about one that
// may still fail to come up.
func (s State) Running() bool { return s.Status == "Running" }

// Disabled reports whether the service's start type blocks it from
// starting at all, the SCM's equivalent of a masked systemd unit.
func (s State) Disabled() bool { return s.StartType == "Disabled" }

// Map renders the State as the map a diff stat records, the same
// contract remotesvc.State.Map documents: the keys are what a method
// records under diff.before and diff.after.
func (s State) Map() map[string]any {
	return map[string]any{
		"name":       s.Name,
		"exists":     s.Exists,
		"running":    s.Running(),
		"status":     s.Status,
		"start_type": s.StartType,
	}
}

// statusResult is the JSON shape the Status script prints.
type statusResult struct {
	Exists    bool   `json:"Exists"`
	Status    string `json:"Status"`
	StartType string `json:"StartType"`
}

// Status reads what the Service Control Manager currently reports about
// name.
func Status(ctx context.Context, session Session, name string) (State, error) {
	script := "$ErrorActionPreference = 'Stop'\n" +
		"$svc = Get-Service -Name " + winrmexec.QuotePS(name) + " -ErrorAction SilentlyContinue\n" +
		"if ($svc) {\n" +
		"  [PSCustomObject]@{Exists=$true; Status=$svc.Status.ToString(); StartType=$svc.StartType.ToString()} | ConvertTo-Json -Compress\n" +
		"} else {\n" +
		"  [PSCustomObject]@{Exists=$false; Status=''; StartType=''} | ConvertTo-Json -Compress\n" +
		"}"

	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return State{}, fmt.Errorf("reading the state of %s: %w", name, err)
	}
	if result.ExitCode != 0 {
		return State{}, fmt.Errorf("reading the state of %s: powershell exited %d: %s", name, result.ExitCode, firstLine(result.Stderr))
	}
	return parseStatusJSON(name, result.Stdout)
}

// parseStatusJSON turns the Status script's ConvertTo-Json output into a
// State. Split out from Status so the parsing itself is testable without
// a live WinRM round trip, the same separation remotesvc.parseShow keeps
// from remotesvc.Status.
func parseStatusJSON(name, stdout string) (State, error) {
	var parsed statusResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &parsed); err != nil {
		return State{}, fmt.Errorf("reading the state of %s: parsing Get-Service output: %w (stdout: %s)", name, err, stdout)
	}
	return State{Name: name, Exists: parsed.Exists, Status: parsed.Status, StartType: parsed.StartType}, nil
}

// The verbs this package can send. Kept as one table so every operation
// is spelled once, the same reason pkg/remotesvc keeps its own op
// constants.
const (
	opStart   = "Start-Service"
	opStop    = "Stop-Service"
	opRestart = "Restart-Service"
)

// Start starts name now, without changing its start type.
func Start(ctx context.Context, session Session, name string) error {
	return runVerb(ctx, session, opStart, name)
}

// Stop stops name now, without changing its start type.
func Stop(ctx context.Context, session Session, name string) error {
	return runVerb(ctx, session, opStop, name)
}

// Restart restarts name, starting it if it was not running, matching
// svc.systemd.restart's own state=restarted semantics.
func Restart(ctx context.Context, session Session, name string) error {
	return runVerb(ctx, session, opRestart, name)
}

// Enable sets name's start type to Automatic, making it start at boot,
// without starting it now.
func Enable(ctx context.Context, session Session, name string) error {
	return setStartupType(ctx, session, name, "Automatic")
}

// Disable sets name's start type to Disabled, stopping it starting at
// boot, without stopping it now.
func Disable(ctx context.Context, session Session, name string) error {
	return setStartupType(ctx, session, name, "Disabled")
}

// runVerb sends one *-Service cmdlet against one service name.
//
// $ErrorActionPreference = 'Stop' turns a cmdlet's default non-terminating
// error (which would leave the exit code 0 and the failure buried in
// stderr as a warning) into a terminating one, which is what makes
// powershell.exe report a real non-zero exit code -- the whole reason
// this package builds every script with that line first rather than
// trusting Result.ExitCode to reflect a plain cmdlet failure.
func runVerb(ctx context.Context, session Session, verb, name string) error {
	script := "$ErrorActionPreference = 'Stop'\n" + verb + " -Name " + winrmexec.QuotePS(name)
	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return fmt.Errorf("%s %s: %w", verb, name, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s %s exited %d: %s", verb, name, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}

// setStartupType runs Set-Service -StartupType against one service name.
func setStartupType(ctx context.Context, session Session, name, startupType string) error {
	script := "$ErrorActionPreference = 'Stop'\n" +
		"Set-Service -Name " + winrmexec.QuotePS(name) + " -StartupType " + startupType
	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return fmt.Errorf("Set-Service %s -StartupType %s: %w", name, startupType, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Set-Service %s -StartupType %s exited %d: %s", name, startupType, result.ExitCode, firstLine(result.Stderr))
	}
	return nil
}

// firstLine returns the first line of s, trimmed, so an error message
// carries the reason rather than a page of output. A near-copy of
// remotesvc's own helper; see exec/winrm/shell.go's secondsParam comment
// for why this repository accepts that duplication until a third
// package needs it.
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
