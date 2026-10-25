// A device's console server as a dialer a Collection method can use: the
// server its console_server names, logged in to with that server's own
// credential and verified host key, attached to the device's console line
// (pkg/cmlconsole).
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cmlconsole"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// deviceByName finds an inventory device by name.
type deviceByName interface {
	GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error)
}

// ConsoleDialerFor returns a dialer to device's console line through the
// console server its console_server names, and that server credential's
// secret values, which the caller must add to its masking set; a nil dialer
// when device is not reached through a console server. The server's host
// key is always verified against known_hosts, whatever a task asks of its
// own target's, as a route's hops are, and every dial opens a connection of
// its own, never pooled.
func ConsoleDialerFor(ctx context.Context, repo deviceByName, credentials credential.Store, device pkginventory.InventoryItem) (sdk.ConsoleDial, []string, error) {
	served, ok := device.(capability.ConsoleServedCapable)
	if !ok || !device.HasCapability(capability.NameConsoleServed) {
		return nil, nil, nil
	}
	name := served.ConsoleServer()
	server, err := repo.GetByName(ctx, name)
	if err != nil {
		return nil, nil, fmt.Errorf("device %q's console server %q: %w", device.Name(), name, err)
	}
	api, ok := server.(capability.CMLAPICapable)
	if !ok || !server.HasCapability(capability.NameCMLAPI) {
		return nil, nil, fmt.Errorf("device %q's console server %q is not a cml_controller", device.Name(), name)
	}
	cred, err := credentials.Lookup(ctx, name)
	if errors.Is(err, credential.ErrNotFound) {
		return nil, nil, fmt.Errorf("device %q's console server %q has no login stored (pleiades add-credential %s --username <user> --password-stdin)", device.Name(), name, name)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("device %q's console server %q: %w", device.Name(), name, err)
	}
	auth, err := remoteexec.AuthFrom(cred.Username, cred.Password, cred.PrivateKeyPEM, cred.Passphrase)
	if err != nil {
		return nil, nil, fmt.Errorf("device %q's console server %q: %w", device.Name(), name, err)
	}
	host, port := api.CMLConsoleAddress()
	line := served.ConsoleLine()
	runner := remoteexec.Shared(remoteexec.Options{})
	dial := func(ctx context.Context) (netcli.Shell, error) {
		conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: port}, auth)
		if err != nil {
			return nil, fmt.Errorf("the console server %q: %w", name, err)
		}
		shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("the console server %q: %w", name, err)
		}
		if err := cmlconsole.Attach(ctx, shell, line); err != nil {
			_ = shell.Close()
			_ = conn.Close()
			return nil, err
		}
		return &consoleConn{Shell: shell, conn: conn}, nil
	}
	return dial, []string{cred.Password, string(cred.PrivateKeyPEM), cred.Passphrase}, nil
}

// consoleConn is a console line whose Close also closes the connection to
// the console server under it.
type consoleConn struct {
	*remoteexec.Shell
	conn *remoteexec.Conn
}

// Close closes the line, then the connection, reporting the first failure.
func (c *consoleConn) Close() error {
	shellErr := c.Shell.Close()
	connErr := c.conn.Close()
	if shellErr != nil {
		return shellErr
	}
	return connErr
}
