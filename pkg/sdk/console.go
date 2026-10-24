// A device reached through a console server: the dialer the engine hands a
// method, and the shell OpenShell makes of it.
package sdk

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cmlconsole"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ConsoleDial attaches to the target device's console line through its
// console server, logged in to with the server's own credential and
// verified host key, and returns the line before the device's own login.
// Every call opens a connection of its own: a console line is never pooled.
type ConsoleDial func(ctx context.Context) (netcli.Shell, error)

// Consoler is implemented by a runbook context that can reach its target
// device through the device's console server. DeviceConsole returns nil
// where the tier cannot resolve the server and its credential.
type Consoler interface {
	DeviceConsole() ConsoleDial
}

// openConsole opens device's CLI through its console server: the line from
// the context's dialer, then the device's own login, answered with the
// task's credential.
func openConsole(ctx context.Context, rc RunbookContext, device inventory.InventoryItem, fqcn string) (netcli.Shell, error) {
	var dial ConsoleDial
	if c, ok := rc.(Consoler); ok {
		dial = c.DeviceConsole()
	}
	if dial == nil {
		return nil, fmt.Errorf("%s: device %q is reached through a console server, which only the pleiades CLI resolves so far", fqcn, device.Name())
	}
	shell, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	secrets := rc.InjectSecrets()
	login := cmlconsole.Login{Username: secrets[wire.SecretUsername], Password: secrets[wire.SecretPassword]}
	if err := cmlconsole.LogIn(ctx, shell, login); err != nil {
		// The login may have got as far as a prompt before it failed.
		_ = cmlconsole.LogOut(shell)
		_ = shell.Close()
		return nil, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	return &consoleShell{Shell: shell}, nil
}

// consoleShell is a console line whose Close logs the device's session out
// first, so the next person on that console meets a login, not this
// session's privileged prompt.
type consoleShell struct {
	netcli.Shell
}

// Close logs the line out, then closes it and the connection under it.
func (s *consoleShell) Close() error {
	_ = cmlconsole.LogOut(s.Shell)
	return s.Shell.Close()
}
