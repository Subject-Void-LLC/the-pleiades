// Tests of OpenShell's console branch: a device behind a console server is
// reached only through the dialer its context hands over, its own login is
// answered with the task's credential, and the line is logged out on close.
package sdk_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// consoleDevice is a device reached through a console server.
type consoleDevice struct{ *inventorytest.Stub }

func (consoleDevice) ConsoleServer() string { return "cml" }
func (consoleDevice) ConsoleLine() string   { return "0ba0f180-c5b2-4dcf-af64-bf9bb645a566" }

// line is an attached console line answering reads from its chunks in
// order and recording what is written.
type line struct {
	chunks  []string
	buf     string
	written []string
	closed  bool
}

func (l *line) WriteLine(_ context.Context, s string) error {
	l.written = append(l.written, s)
	return nil
}

func (l *line) ReadUntil(_ context.Context, re *regexp.Regexp, _ int) (string, error) {
	for {
		if loc := re.FindStringIndex(l.buf); loc != nil {
			out := l.buf[:loc[1]]
			l.buf = l.buf[loc[1]:]
			return out, nil
		}
		if len(l.chunks) == 0 {
			return "", errors.New("the line went quiet")
		}
		l.buf += l.chunks[0]
		l.chunks = l.chunks[1:]
	}
}

func (l *line) Close() error { l.closed = true; return nil }

// consoleContext is a task context that can reach its target's console.
type consoleContext struct {
	dial    sdk.ConsoleDial
	secrets map[string]string
}

func (c consoleContext) InjectSecrets() map[string]string { return c.secrets }
func (c consoleContext) SetStat(string, any) error        { return nil }
func (c consoleContext) EmitFact(string, any) error       { return nil }
func (c consoleContext) DeviceConsole() sdk.ConsoleDial   { return c.dial }

// plainContext can reach no console, as a Runner's cannot yet.
type plainContext struct{ consoleContext }

// TestOpenShell_ThroughTheConsoleServer: the node's login is answered with
// the task's credential on the line the dialer attached, Close logs the
// line out before closing it, and a device that also had an address is
// still reached only through its console.
func TestOpenShell_ThroughTheConsoleServer(t *testing.T) {
	device := consoleDevice{&inventorytest.Stub{StubName: "P1", Caps: []capability.Name{capability.NameConsoleServed, capability.NameCiscoIOS}}}
	l := &line{chunks: []string{"\r\nUsername: ", "Password: ", "\r\nP1#", "\r\nP1#", "\r\nP1#"}}
	rc := consoleContext{
		dial:    func(context.Context) (netcli.Shell, error) { return l, nil },
		secrets: map[string]string{wire.SecretUsername: "netops", wire.SecretPassword: "pw"},
	}
	shell, err := sdk.OpenShell(context.Background(), rc, device, nil, "net.ios.command")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(l.written, "|"); got != "|netops|pw|" {
		t.Errorf("the login wrote %q", got)
	}
	l.written = nil
	if err := shell.Close(); err != nil || !l.closed || strings.Join(l.written, "|") != "|end|exit" {
		t.Errorf("Close: %v, closed %v, wrote %q", err, l.closed, l.written)
	}
}

// TestOpenShell_ConsoleRefusals: a context with no console dialer (a tier
// that cannot resolve the console server) is refused naming why; a dial
// that fails, and a login the node refuses, are errors, and the refused
// line is closed.
func TestOpenShell_ConsoleRefusals(t *testing.T) {
	device := consoleDevice{&inventorytest.Stub{StubName: "P1", Caps: []capability.Name{capability.NameConsoleServed}}}
	if _, err := sdk.OpenShell(context.Background(), plainContext{}, device, nil, "net.ios.command"); err == nil || !strings.Contains(err.Error(), "only the pleiades CLI resolves") {
		t.Errorf("no dialer: %v", err)
	}
	broken := consoleContext{dial: func(context.Context) (netcli.Shell, error) { return nil, errors.New("console server refused") }}
	if _, err := sdk.OpenShell(context.Background(), broken, device, nil, "net.ios.command"); err == nil || !strings.Contains(err.Error(), "console server refused") {
		t.Errorf("a failed dial: %v", err)
	}
	l := &line{chunks: []string{"Username: ", "Password: ", "\r\n% Login invalid\r\nUsername: "}}
	refused := consoleContext{
		dial:    func(context.Context) (netcli.Shell, error) { return l, nil },
		secrets: map[string]string{wire.SecretUsername: "netops", wire.SecretPassword: "wrong"},
	}
	if _, err := sdk.OpenShell(context.Background(), refused, device, nil, "net.ios.command"); err == nil || !strings.Contains(err.Error(), "refused the login") || !l.closed {
		t.Errorf("a refused login: %v, line closed %v", err, l.closed)
	}
}
