// Tests for wait.snmp against the in-process agent (pkg/snmp/snmptest) over
// SNMPv2c, through the method's own client path and a real generic_snmp
// device: an agent that answers is done at the first try; one that drops
// every request, as an agent drops a community it does not know, is given up
// on at the timeout after several tries; and a record whose credential the
// client refuses ends the wait at once.
package wait_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// snmpCommunity is the test agent's throwaway community.
const snmpCommunity = "wait-agent-community"

// snmpDevice is a generic_snmp at agent over SNMPv2c.
func snmpDevice(t *testing.T, agent *snmptest.Agent, version string) inventory.InventoryItem {
	t.Helper()
	props := map[string]any{generic.HostProperty: "127.0.0.1", snmp.PortProperty: agent.Port(), snmp.TimeoutProperty: 1, snmp.RetriesProperty: 0}
	if version != "3" {
		props[snmp.VersionProperty], props[snmp.AllowCleartextCommunityProperty] = version, true
	}
	item, err := generic.NewSNMP(record.Record{Name: "agent", Type: generic.TypeSNMP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// snmpContext hands the method cred.
func snmpContext(cred wire.SNMPCredential) *waitContext {
	return &waitContext{secrets: cred.Secrets(), stats: map[string]any{}}
}

// routedWaitContext is a task's context for a device reached through a route
// of SSH hops (sdk.Router).
type routedWaitContext struct{ *waitContext }

// DeviceRoute implements sdk.Router with a dialer that is never called.
func (routedWaitContext) DeviceRoute() sdk.RouteDial {
	return func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("never dialed") }
}

// TestSNMP_AnAnswerEndsTheWait: an agent answering sysUpTime.0 ends the wait
// at the first try, recording the version and port.
func TestSNMP_AnAnswerEndsTheWait(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.System()...).Handle)
	rc := snmpContext(wire.SNMPCredential{Community: snmpCommunity})
	if _, err := wait.SNMP(context.Background(), rc, snmpDevice(t, agent, "2c"), map[string]any{"timeout": 10}); err != nil {
		t.Fatal(err)
	}
	if rc.stats["version"] != "2c" || rc.stats["port"] != agent.Port() || rc.stats["tries"] != 1 || rc.stats["failures"] != 0 {
		t.Errorf("stats = %v", rc.stats)
	}
	if !strings.Contains(strings.Join(rc.stats["warnings"].([]string), " "), "in the clear") {
		t.Errorf("warnings = %v, want the cleartext community named", rc.stats["warnings"])
	}
}

// TestSNMP_SilenceIsTriedUntilTheTimeout: an agent that drops every request
// is tried again until the timeout, and the error says it timed out.
func TestSNMP_SilenceIsTriedUntilTheTimeout(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore("another-community-here").Handle)
	_, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{Community: snmpCommunity}), snmpDevice(t, agent, "2c"),
		map[string]any{"timeout": 4, "sleep": 1, "connect_timeout": 2})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("wait.snmp = %v, want a timeout", err)
	}
	if n := len(agent.Requests()); n < 2 {
		t.Errorf("the agent was tried %d times, want several", n)
	}
}

// TestSNMP_ARefusedRecordStopsAtOnce: a community for an SNMPv3 device is
// refused by the client before anything is sent, which no try changes; a
// device that is not SNMPCapable and bad timing are refused before any try.
func TestSNMP_ARefusedRecordStopsAtOnce(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.System()...).Handle)
	_, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{Community: snmpCommunity}), snmpDevice(t, agent, "3"), map[string]any{"timeout": 30})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || len(agent.Requests()) != 0 {
		t.Fatalf("wait.snmp = %v after %d requests, want it stopped before sending", err, len(agent.Requests()))
	}
	if _, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{}), &inventorytest.Stub{StubName: "box"}, nil); err == nil {
		t.Error("a device with no agent was waited for")
	}
	if _, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{}), nil, nil); err == nil {
		t.Error("no device was waited for")
	}
	routed := &routedWaitContext{snmpContext(wire.SNMPCredential{Community: snmpCommunity})}
	if _, err := wait.SNMP(context.Background(), routed, snmpDevice(t, agent, "2c"), map[string]any{"timeout": 30}); err == nil || !strings.Contains(err.Error(), "route of SSH hops") {
		t.Errorf("a routed device = %v, want it refused", err)
	}
	if len(agent.Requests()) != 0 {
		t.Error("a refused wait sent a request")
	}
	if _, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{}), snmpDevice(t, agent, "2c"), map[string]any{"timeout": 0}); err == nil {
		t.Error("a timeout of 0 was accepted")
	}
}

// TestSNMP_AHostNameNotResolvingYetIsTriedAgain: a host name that does not
// resolve, as a device's own name may not until its lease registers it, is a
// failed try like silence, tried until the timeout, never a refused record.
func TestSNMP_AHostNameNotResolvingYetIsTriedAgain(t *testing.T) {
	item, err := generic.NewSNMP(record.Record{Name: "unborn", Type: generic.TypeSNMP, Properties: map[string]any{
		generic.HostProperty: "no-such-agent.invalid", snmp.VersionProperty: "2c", snmp.AllowCleartextCommunityProperty: true,
		snmp.TimeoutProperty: 1, snmp.RetriesProperty: 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{Community: snmpCommunity}), item, map[string]any{"timeout": 3, "sleep": 1, "connect_timeout": 2})
	if err == nil || !strings.Contains(err.Error(), "timed out") || strings.Contains(err.Error(), "stopped at try") {
		t.Fatalf("wait.snmp of a name that does not resolve = %v, want it tried until the timeout", err)
	}
}

// TestSNMP_AStatItCannotRecordFailsTheTask: an answering agent whose
// warnings, diff or any stat cannot be recorded fails the task.
func TestSNMP_AStatItCannotRecordFailsTheTask(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.System()...).Handle)
	for _, key := range []string{sdk.StatWarnings, sdk.StatDiff, "version", "port", "elapsed", "tries", "failures"} {
		t.Run(key, func(t *testing.T) {
			rc := snmpContext(wire.SNMPCredential{Community: snmpCommunity})
			rc.failOn = key
			if _, err := wait.SNMP(context.Background(), rc, snmpDevice(t, agent, "2c"), map[string]any{"timeout": 10}); !errors.Is(err, errWaitStat) {
				t.Errorf("failing the %s stat: %v", key, err)
			}
		})
	}
}

// TestSNMP_AnAgentWithNoUptimeIsNotReady: an agent that answers and holds no
// sysUpTime.0 for this credential is one more failed try, not readiness.
func TestSNMP_AnAgentWithNoUptimeIsNotReady(t *testing.T) {
	agent := snmptest.Start(t, snmptest.NewStore(snmpCommunity, snmptest.Text("1.3.6.1.2.1.1.5.0", "no-uptime")).Handle)
	_, err := wait.SNMP(context.Background(), snmpContext(wire.SNMPCredential{Community: snmpCommunity}), snmpDevice(t, agent, "2c"),
		map[string]any{"timeout": 3, "sleep": 1, "connect_timeout": 2})
	if err == nil || !strings.Contains(err.Error(), "holds no sysUpTime.0") {
		t.Fatalf("wait.snmp = %v, want the missing uptime quoted at the timeout", err)
	}
}
