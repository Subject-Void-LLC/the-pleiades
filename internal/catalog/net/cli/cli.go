// Package cli implements "net.cli.command" and "net.cli.config": the two
// generic, cross-vendor network CLI methods. Neither carries
// vendor-specific knowledge of its own; the only convention either has
// to work with is whatever a device's own cli_prompt property declares
// (capability.NetworkCLICapable), which is what netcli.FromPrompt builds
// a Dialect from. internal/catalog/net/ios is the Cisco-IOS-specific
// sibling that carries netcli.IOS's own real paging, configuration-mode
// and error conventions instead.
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// cliSession is the narrow surface Command and Config actually use from
// a *netcli.Session: enough that cli_internal_test.go can swap in a
// canned double with no real I/O, and no more. Neither method here ever
// calls Session.Config (see this package's own doc comment for why), so
// unlike internal/catalog/net/ios's own iosSession, this interface has
// no Config method at all.
type cliSession interface {
	Command(ctx context.Context, line string) (string, error)
	Close() error
}

// openSession is the seam this package's own tests swap, mirroring the
// role statusFunc/startFunc play in internal/catalog/svc/windows: real
// I/O in production, a canned double in cli_internal_test.go.
var openSession = realOpenSession

// realOpenSession connects to device over SSH, opens an interactive
// shell, and builds a netcli.Session from the device's own declared CLI
// prompt.
func realOpenSession(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (cliSession, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	session, err := netcli.Open(ctx, shell, netcli.FromPrompt(cliPrompt(device)), netcli.Options{})
	if err != nil {
		_ = shell.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}

	return &sessionAndConn{Session: session, conn: conn}, nil
}

// sessionAndConn embeds a *netcli.Session (promoting Command) but
// overrides Close so one call releases the Session's own PTY session
// AND the underlying *remoteexec.Conn it was opened on: Session.Close
// only closes the former, and sdk.Connect dials a fresh Conn on every
// call the same way exec.command's own connect-then-defer-Close does,
// so leaving the latter open would leak one real SSH connection per
// task.
type sessionAndConn struct {
	*netcli.Session
	conn *remoteexec.Conn
}

func (s *sessionAndConn) Close() error {
	sessErr := s.Session.Close()
	connErr := s.conn.Close()
	if sessErr != nil {
		return sessErr
	}
	return connErr
}

// genericCommandTimeout bounds how long a single line sent through a
// FromPrompt-built generic Dialect is allowed to run before Command and
// Config give up and report an error, rather than trusting the caller's
// own ctx to bound it.
//
// This is not a stylistic choice; it closes a real hang found by
// running this method against a real device rather than by design
// review. A generic Dialect has no known paging-disable convention
// (FromPrompt's own doc comment states this plainly), so a command
// whose output is longer than the device's own terminal length pauses
// on a "--More--"-style prompt this method has no way to answer.
// Verified directly: "show version" against a real Cisco IOS XE device
// with its default terminal settings (net.ios.config's own netcli.IOS
// dialect sends "terminal length 0" and never hits this; the generic
// Dialect built here does not) hung indefinitely, because
// cmd/pleiades's own run path calls this with context.Background(),
// which never would have stopped it either. net.ios.config's own
// session calls carry no equivalent bound: its paging is genuinely
// disabled, and a large "show running-config" backup capture legitimately
// taking longer than any fixed bound this package would choose is a real
// risk a blanket timeout there would trade one failure mode for another.
const genericCommandTimeout = 30 * time.Second

// runCommand sends line through session, bounded by
// genericCommandTimeout, so a pager pause this Dialect cannot answer
// fails clearly instead of hanging forever.
func runCommand(ctx context.Context, session cliSession, line string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, genericCommandTimeout)
	defer cancel()
	return session.Command(ctx, line)
}

// cliPrompt reads the device's own declared CLI prompt, or "" when it
// does not structurally implement capability.NetworkCLICapable at all.
// This IS the real gate for a device that reaches this far without one:
// engine.checkMethodCapabilities enforces RequiredCapabilities before
// Invoke runs, so in practice this only ever returns "" for a device
// whose cli_prompt property itself was left unset, which netcli.Open
// then refuses with a clear error rather than a nil-pointer panic. See
// internal/catalog/exec/exec.go's own workingDirectory for the identical
// shape and the same reasoning.
func cliPrompt(device inventory.InventoryItem) string {
	if d, ok := device.(capability.NetworkCLICapable); ok {
		return d.CLIPrompt()
	}
	return ""
}
