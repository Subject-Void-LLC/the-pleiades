// Package winrmdism reads and changes Windows optional feature/role
// state on a device over WinRM using dism.exe, and is the single place
// this platform does that.
//
// It exists for the reason pkg/winrmsvc exists: a Collection may import
// pkg/ and nothing else in this module, so internal/catalog/win/feature's
// install and remove methods cannot each carry their own idea of how to
// ask DISM what a feature is doing.
//
// # DISM, not the ServerManager module
//
// win.feature.* shells out to dism.exe rather than calling
// Install-WindowsFeature/Get-WindowsFeature (the ServerManager
// PowerShell module Ansible's own win_feature module prefers when it is
// present). Two reasons, and the second is the one that matters more:
// windows.Server.DISMLogPath already commits this design to DISM, since
// a ServerManager cmdlet has no log path to report; and dism.exe /online
// works on every Windows SKU, while ServerManager is Windows Server
// only, so building on DISM is the more universal claim a generic
// win.feature FQCN should make.
//
// # exit $LASTEXITCODE is not decoration
//
// Every script this package sends ends with an explicit
// "exit $LASTEXITCODE" line, and it is worth saying why rather than
// treating it as boilerplate. Calling a native executable from
// PowerShell does not make powershell.exe's own process exit code equal
// the executable's exit code; $LASTEXITCODE holds that value, and a
// script that never reads it leaves the host's own exit status at
// whatever it would otherwise be (typically 0), even when dism.exe just
// reported a real failure. Without the explicit propagation,
// Result.ExitCode from every call in this package would read success
// regardless of what DISM actually did.
//
// # Exit codes DISM itself defines, and what this package does with them
//
// 0 is success. 3010 is success-reboot-required, ERROR_SUCCESS_REBOOT_
// REQUIRED, and this package treats it as success while reporting the
// reboot need as data (ChangeResult.RebootRequired) rather than folding
// it into changed the way Ansible's win_feature reports restart_needed
// separately. 87, ERROR_INVALID_PARAMETER, is what
// /get-featureinfo returns for a feature name DISM does not recognize;
// Status reports that as FeatureState{Exists: false} rather than an
// error, the same "a name the platform has never heard of is an answer,
// not a failure" rule pkg/remotesvc applies to a systemd unit. Any other
// non-zero code is a real failure, surfaced with DISM's own output.
package winrmdism

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// Session bundles what every call in this package needs to reach one
// Windows device. See pkg/winrmsvc's package doc for why this is a
// config bundle rather than a live connection.
type Session struct {
	Target  winrmexec.Target
	Auth    winrmexec.Auth
	Options winrmexec.Options
}

// errInvalidParameter is DISM's own exit code for, among other causes, a
// /get-featureinfo call naming a feature it does not recognize.
const errInvalidParameter = 87

// rebootRequiredCode is DISM's own ERROR_SUCCESS_REBOOT_REQUIRED.
const rebootRequiredCode = 3010

// FeatureState is what DISM currently reports about one optional
// feature.
type FeatureState struct {
	// Name is the feature name this state describes.
	Name string

	// Exists reports whether DISM recognizes this feature name at all.
	// False means /get-featureinfo returned errInvalidParameter, not that
	// a request failed.
	Exists bool

	// State is DISM's own reported State line, verbatim: "Enabled",
	// "Disabled", "Enable Pending", "Disable Pending", "Partially
	// Enabled", "Superseded", or empty when Exists is false.
	State string
}

// Enabled reports whether the feature is fully enabled right now.
//
// "Enable Pending" deliberately does NOT count, the same "pending is not
// done" rule winrmsvc.State.Running and remotesvc.State.Active both
// apply: a feature part-way through enabling has not finished.
func (f FeatureState) Enabled() bool { return f.State == "Enabled" }

// DisabledState reports whether the feature is fully disabled right now.
func (f FeatureState) DisabledState() bool { return f.State == "Disabled" }

// Map renders the FeatureState as the map a diff stat records, the same
// contract winrmsvc.State.Map and remotesvc.State.Map document.
func (f FeatureState) Map() map[string]any {
	return map[string]any{
		"name":   f.Name,
		"exists": f.Exists,
		"state":  f.State,
	}
}

// ChangeResult is what an Enable or Disable call learned about the
// change it made.
type ChangeResult struct {
	// RebootRequired reports whether DISM answered 3010: the change
	// succeeded but needs a restart to take full effect.
	RebootRequired bool
}

// dismCommand builds one dism.exe invocation as a PowerShell script,
// with exit code propagation. See the package doc for why the trailing
// exit line is required rather than cosmetic.
func dismCommand(logPath string, args ...string) string {
	parts := append([]string{"dism.exe", "/online"}, args...)
	parts = append(parts, "/logpath:"+quotePS(logPath))
	return strings.Join(parts, " ") + "\nexit $LASTEXITCODE"
}

// Status reads what DISM currently reports about featureName.
func Status(ctx context.Context, session Session, logPath, featureName string) (FeatureState, error) {
	script := dismCommand(logPath, "/get-featureinfo", "/featurename:"+quotePS(featureName))

	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return FeatureState{}, fmt.Errorf("reading the state of feature %s: %w", featureName, err)
	}
	if result.ExitCode == errInvalidParameter {
		return FeatureState{Name: featureName, Exists: false}, nil
	}
	if result.ExitCode != 0 {
		return FeatureState{}, fmt.Errorf("reading the state of feature %s: dism.exe /get-featureinfo exited %d: %s",
			featureName, result.ExitCode, dismOutput(result))
	}

	state, ok := parseStateLine(result.Stdout)
	if !ok {
		return FeatureState{}, fmt.Errorf("reading the state of feature %s: dism.exe /get-featureinfo succeeded but printed no \"State :\" line: %s",
			featureName, result.Stdout)
	}
	return FeatureState{Name: featureName, Exists: true, State: state}, nil
}

// Enable turns featureName on, including its required parent features
// (/all), and reports whether the change needs a restart.
func Enable(ctx context.Context, session Session, logPath, featureName string) (ChangeResult, error) {
	return runChange(ctx, session, logPath, "/enable-feature", featureName, "/all", "/norestart")
}

// Disable turns featureName off and reports whether the change needs a
// restart.
//
// Unlike Enable, this does not pass /all: removing a feature should not
// silently remove the parent features it depended on.
func Disable(ctx context.Context, session Session, logPath, featureName string) (ChangeResult, error) {
	return runChange(ctx, session, logPath, "/disable-feature", featureName, "/norestart")
}

// runChange sends one enable/disable verb against one feature name.
func runChange(ctx context.Context, session Session, logPath, verb, featureName string, extraFlags ...string) (ChangeResult, error) {
	args := append([]string{verb, "/featurename:" + quotePS(featureName)}, extraFlags...)
	script := dismCommand(logPath, args...)

	result, err := winrmexec.Run(ctx, session.Target, session.Auth, winrmexec.ShellPowerShell, script, session.Options)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("dism.exe %s %s: %w", verb, featureName, err)
	}
	switch result.ExitCode {
	case 0:
		return ChangeResult{RebootRequired: false}, nil
	case rebootRequiredCode:
		return ChangeResult{RebootRequired: true}, nil
	default:
		return ChangeResult{}, fmt.Errorf("dism.exe %s %s exited %d: %s", verb, featureName, result.ExitCode, dismOutput(result))
	}
}

// parseStateLine finds DISM's "State : <value>" line in /get-featureinfo
// output and returns the value.
//
// It parses by known key rather than by position, ignoring every other
// line ("Feature Name :", "Display Name :", "Restart Required :", ...),
// the same restrained style remotesvc.parseShow applies to
// "systemctl show" output: an unknown line is not this function's
// problem, and a DISM version that reordered or added lines would not
// break it.
func parseStateLine(stdout string) (string, bool) {
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "State" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// dismOutput renders a failed dism.exe call's output for an error
// message. DISM writes most of what it has to say, including its own
// error text, to stdout rather than stderr, so stdout is included even
// though stderr is checked too.
func dismOutput(result winrmexec.Result) string {
	var parts []string
	if s := strings.TrimSpace(result.Stdout); s != "" {
		parts = append(parts, "stdout: "+s)
	}
	if s := strings.TrimSpace(result.Stderr); s != "" {
		parts = append(parts, "stderr: "+s)
	}
	if len(parts) == 0 {
		return "no output"
	}
	return strings.Join(parts, "; ")
}

// quotePS renders s as a PowerShell single-quoted string literal, safe
// to splice into a script this package builds. See pkg/winrmsvc's own
// quotePS for the full reasoning; duplicated here because DISM's
// argument syntax (/flag:value) means this package builds a bare command
// line rather than calling a cmdlet with named parameters, but the
// escaping rule is identical.
func quotePS(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
