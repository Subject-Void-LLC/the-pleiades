// Package netconf: what net.netconf.config does when a parameter is
// wrong, the subsystem cannot be opened, or the backup it captured cannot
// be recorded.
//
// The method's own decisions about the datastore, the lock and the commit
// are covered by netconf_internal_test.go. These are the paths a device
// on a bad day reaches, where the risk is the opposite one: reporting a
// configuration as applied when nothing was sent, or as backed up when
// nothing was kept.
package netconf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// errNoSubsystem is the NETCONF subsystem refusing to open: the device
// answers SSH but does not offer netconf, which is what a device with the
// feature switched off does.
var errNoSubsystem = errors.New("subsystem request failed")

// TestConfig_RefusesAParameterItCannotRead covers the parameters read
// before anything is opened: each is refused naming the method, and the
// device is never dialed, so a typo is answered without touching it.
func TestConfig_RefusesAParameterItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		says   string
	}{
		{name: "a backup flag that is not a bool", params: map[string]any{"content": "<config/>", "backup": "maybe"}, says: "net.netconf.config"},
		{name: "a commit flag that is not a bool", params: map[string]any{"content": "<config/>", "commit": "later"}, says: "net.netconf.config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialed := false
			swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string, netconf.Datastore) (netconfSession, error) {
				dialed = true
				return &fakeSession{}, nil
			})
			_, err := Config(context.Background(), newStubContext(), nil, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("err = %v, want one containing %q", err, tc.says)
			}
			if dialed {
				t.Error("the device was dialed to report a parameter this method could have read first")
			}
		})
	}
}

// TestConfig_ReportsASubsystemThatCannotOpen covers a device that answers
// SSH but offers no NETCONF subsystem: the failure travels out as it came,
// and nothing is reported as configured.
func TestConfig_ReportsASubsystemThatCannotOpen(t *testing.T) {
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string, netconf.Datastore) (netconfSession, error) {
		return nil, errNoSubsystem
	})
	result, err := Config(context.Background(), newStubContext(), nil, map[string]any{"content": "<config/>"})
	if !errors.Is(err, errNoSubsystem) {
		t.Errorf("err = %v, want the subsystem's own failure", err)
	}
	if result.Changed {
		t.Error("a configuration that never reached the device was reported as a change")
	}
}

// TestConfig_ABackupThatCannotBeRecordedStopsTheEdit is the order that
// matters: the backup exists so an operator can put the device back, so a
// backup that could not be recorded must stop the run before the edit is
// sent. Sending it first would leave a changed device whose prior
// configuration nobody has.
func TestConfig_ABackupThatCannotBeRecordedStopsTheEdit(t *testing.T) {
	session := &fakeSession{getPayload: datastore.Payload{Bytes: []byte("<config><hostname>r1</hostname></config>")}}
	useSession(t, session)
	rc := newStubContext()
	rc.failStat = statBackup

	_, err := Config(context.Background(), rc, nil, map[string]any{"content": "<config/>", "backup": true})
	if !errors.Is(err, errRecording) {
		t.Fatalf("err = %v, want the recording failure", err)
	}
	if len(session.editCalls) != 0 {
		t.Errorf("the device was configured anyway: %v", session.editCalls)
	}
	if session.committed != 0 {
		t.Error("the run committed after failing to record its backup")
	}
}
