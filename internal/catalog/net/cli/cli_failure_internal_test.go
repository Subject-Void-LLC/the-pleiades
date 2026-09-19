// Package cli: what the generic CLI methods do when the session cannot
// be opened or the device's answer cannot be recorded, and what prompt
// they open a session with.
package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// errNoSession is a session that could not be opened: the device is
// unreachable, refused this run's credential, or gave no prompt netcli
// could synchronize on.
var errNoSession = errors.New("dial tcp 10.0.0.1:22: connect: connection refused")

// promptDevice declares a CLI prompt, as a device type reaching these
// methods does.
type promptDevice struct {
	*inventorytest.Stub
	prompt string
}

func (d *promptDevice) CLIPrompt() string { return d.prompt }

// TestMethods_ReportASessionThatCannotOpen covers both methods when the
// device cannot be reached: the failure travels out as it came, and
// nothing is reported as run or configured, since neither reached the
// device at all.
func TestMethods_ReportASessionThatCannotOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method collection.Method
		params map[string]any
	}{
		{name: "command", method: Command, params: map[string]any{"command": "show version"}},
		{name: "config", method: Config, params: map[string]any{"config": "hostname r1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (cliSession, error) {
				return nil, errNoSession
			})
			result, err := tc.method(context.Background(), newStubContext(), nil, tc.params)
			if !errors.Is(err, errNoSession) {
				t.Errorf("err = %v, want the dial's own failure", err)
			}
			if result.Changed {
				t.Error("a task that never reached the device reported a change")
			}
		})
	}
}

// TestCommand_AnOutputThatCannotBeRecordedFails covers the last step of
// net.cli.command: its whole product is the device's output under the
// stdout stat, so output that could not be recorded fails the task rather
// than reporting a command that ran and left nothing to read.
func TestCommand_AnOutputThatCannotBeRecordedFails(t *testing.T) {
	session := &fakeSession{outputs: map[string]string{"show version": "IOS-XE 17.12.1"}}
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (cliSession, error) {
		return session, nil
	})
	rc := newStubContext()
	rc.statErr = errors.New("recording the result failed")

	_, err := Command(context.Background(), rc, nil, map[string]any{"command": "show version"})
	if err == nil || !errors.Is(err, rc.statErr) || !strings.HasPrefix(err.Error(), "net.cli.command: ") {
		t.Errorf("err = %v, want the recording failure named for the method", err)
	}
	if !session.closed {
		t.Error("the session was left open")
	}
}

// TestCliPrompt_ReadsTheDevicesOwnPrompt is the other half of
// TestCliPrompt_ReturnsEmptyForADeviceWithNoNetworkCLICapability: a
// device that declares one is opened with exactly that prompt, since
// netcli synchronizes on it and a wrong one hangs the session until its
// own timeout.
func TestCliPrompt_ReadsTheDevicesOwnPrompt(t *testing.T) {
	device := &promptDevice{Stub: &inventorytest.Stub{StubName: "sw1"}, prompt: "sw1#"}
	if got := cliPrompt(device); got != "sw1#" {
		t.Errorf("cliPrompt = %q, want the device's own prompt", got)
	}
}
