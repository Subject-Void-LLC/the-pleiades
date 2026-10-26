// An interactive CLI shell on a device, over whichever carrier reaches it.
package sdk

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ParamInsecureTelnet is the task's opt-in to reaching a device over bare
// Telnet, whose login crosses the network in cleartext. It is the same
// name the telnet_exec action takes, for the same reason
// ParamInsecureSkipHostKeyVerify is a task parameter: the exposure sits
// beside the task it applies to, where review sees it.
const ParamInsecureTelnet = "insecure_telnet"

// OpenShell opens an interactive CLI session on device for a method that
// speaks a device's CLI through pkg/netcli: through its console server
// when the device is reached by one (openConsole), over SSH when it is
// reachable over SSH, else over Telnet when it is reachable over Telnet
// and the method is given insecure_telnet: true among its parameters, its
// login answered with the task's credential. The shell's Close ends the
// connection under it too.
func OpenShell(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (netcli.Shell, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", fqcn)
	}
	// A device behind a console server is reached only through it: it has
	// no address of its own, and no other carrier is tried.
	if _, ok := device.(capability.ConsoleServedCapable); ok && device.HasCapability(capability.NameConsoleServed) {
		return openConsole(ctx, rc, device, fqcn)
	}
	if _, ok := device.(capability.SSHTransportCapable); ok && device.HasCapability(capability.NameSSHTransport) {
		conn, err := Connect(ctx, rc, device, params, fqcn)
		if err != nil {
			var dialErr *remoteexec.DialError
			// An open circuit counts too: only a device that did not answer
			// opens one, never a refused login or host key.
			unreached := errors.As(err, &dialErr) && dialErr.Unreached() || errors.Is(err, breaker.ErrOpen)
			if BoolParam(params, ParamInsecureTelnet) && unreached {
				// The task asked for Telnet, and the device's own record says
				// SSH: insecure_telnet only accepts Telnet's exposure, it never
				// chooses the carrier, so say where the carrier is chosen. Only
				// for a device that never answered SSH: a refused host key or
				// login is an answer, and Telnet would be no fix for either.
				return nil, fmt.Errorf("%w (device %q is reached over SSH, its transport property; %s: true only accepts Telnet for a device whose transport is telnet: pleiades set-host %s --set transport=telnet)", err, device.Name(), ParamInsecureTelnet, device.Name())
			}
			return nil, err
		}
		shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%s: %w", fqcn, err)
		}
		return &sshShell{Shell: shell, conn: conn}, nil
	}
	tel, ok := device.(capability.TelnetCapable)
	if !ok || !device.HasCapability(capability.NameTelnet) {
		return nil, fmt.Errorf("%s: device %q is reachable over neither SSH nor Telnet", fqcn, device.Name())
	}
	if !BoolParam(params, ParamInsecureTelnet) {
		return nil, fmt.Errorf("%s: device %q is reached over Telnet, whose login crosses the network in cleartext; pass %s: true among the method's parameters (beside its other parameters, not as a task key) to accept that", fqcn, device.Name(), ParamInsecureTelnet)
	}
	secrets := rc.InjectSecrets()
	login := telnetexec.Login{Username: secrets[wire.SecretUsername], Password: secrets[wire.SecretPassword]}
	session, err := telnetexec.Dial(ctx, tel.TelnetHost(), tel.TelnetPort(), telnetexec.Options{}, login)
	if err != nil {
		if errors.Is(err, telnetexec.ErrLoginNeeded) {
			return nil, fmt.Errorf("%s: device %q: %w; store one with pleiades add-credential", fqcn, device.Name(), err)
		}
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	return session, nil
}

// sshShell is an SSH shell whose Close also releases the connection it
// was opened on, so a method closes one thing.
type sshShell struct {
	*remoteexec.Shell
	conn *remoteexec.Conn
}

// Close closes the shell, then the connection, reporting the first
// failure.
func (s *sshShell) Close() error {
	shellErr := s.Shell.Close()
	connErr := s.conn.Close()
	if shellErr != nil {
		return shellErr
	}
	return connErr
}

// OpenCLI opens device's CLI (OpenShell) in dialect, taking an account that
// lands in user mode ("R1>") to privileged mode with the credential's enable
// secret when it holds one. The session's Close ends the connection under
// it too.
func OpenCLI(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, dialect netcli.Dialect) (*netcli.Session, error) {
	shell, err := OpenShell(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	session, err := netcli.Open(ctx, shell, dialect, netcli.Options{EnableSecret: rc.InjectSecrets()[wire.SecretEnablePassword]})
	if err != nil {
		_ = shell.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	return session, nil
}
