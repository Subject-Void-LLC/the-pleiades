// Tests that net.ovs.port holds the bridge a switch reports for a port to
// the interface-name rule before it sends that bridge back to ovs-vsctl
// (FAILURE_PATTERNS 1174's sweep). A real shell runs a stand-in ovs-vsctl
// on PATH, which answers port-to-br with whatever the test says the switch
// answers and records every other call.
package ovs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ovs"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// switchDevice is a switch the stand-in answers for, reached over the SSH
// harness.
type switchDevice struct {
	*inventorytest.Stub
	host string
	port int
}

// SSHHost is the harness's address.
func (d *switchDevice) SSHHost() string { return d.host }

// SSHPort is the harness's port.
func (d *switchDevice) SSHPort() int { return d.port }

// statsContext keeps a run's stats and hands over the harness's secrets.
type statsContext struct {
	secrets map[string]string
	stats   map[string]any
}

// InjectSecrets returns the harness's credential.
func (c *statsContext) InjectSecrets() map[string]string { return c.secrets }

// SetStat keeps a stat.
func (c *statsContext) SetStat(k string, v any) error { c.stats[k] = v; return nil }

// EmitFact keeps a fact as a stat.
func (c *statsContext) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// vsctlStandIn answers like ovs-vsctl for one port that is on the bridge
// FAKE_BRIDGE names, with no tag and nothing but defaults set, and records
// each call that is not a read.
const vsctlStandIn = `#!/bin/sh
case "$1" in
  port-to-br) printf '%s\n' "$FAKE_BRIDGE"; exit 0 ;;
  br-exists) exit 0 ;;
  get)
    case "$4" in
      type) printf '""\n' ;;
      options) printf '{}\n' ;;
      *) printf '[]\n' ;;
    esac
    exit 0 ;;
esac
printf '%s\n' "$*" >> "$FAKE_RECORD"
exit 0
`

// standInSwitch starts the harness with the stand-in on PATH, reporting the
// port as on bridge, and returns what a call needs and where calls land.
func standInSwitch(t *testing.T, bridge string) (*switchDevice, *statsContext, string) {
	t.Helper()
	need.POSIXShell(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ovs-vsctl"), []byte(vsctlStandIn), 0o700); err != nil { // #nosec G306 -- a stand-in that must be executable
		t.Fatal(err)
	}
	record := filepath.Join(dir, "calls")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_BRIDGE", bridge)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &switchDevice{
		Stub: &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameOpenVSwitch, capability.NameCommandExec}},
		host: srv.Host, port: srv.Port,
	}
	return device, &statsContext{secrets: srv.Secrets(), stats: map[string]any{}}, record
}

// sent returns the recorded calls that change something.
func sent(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record) // #nosec G304 -- a path under this test's own directory
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// TestPort_RefusesABridgeTheSwitchReportsThatIsNoName: a switch answering
// port-to-br with "--if-exists" or "-x" would have had that word sent back
// as del-port's bridge, an option in a bridge's place, when the port is
// removed or moved, and recorded in the undo. Each is refused with nothing
// sent, naming the port and what the switch said. The control is a switch
// answering br0, which does get del-port br0 eth1.
func TestPort_RefusesABridgeTheSwitchReportsThatIsNoName(t *testing.T) {
	for _, tc := range []struct {
		label, bridge string
		params        map[string]any
	}{
		{"remove, --if-exists", "--if-exists", map[string]any{"bridge": "br0", "name": "eth1", "state": "absent"}},
		{"move, -x", "-x", map[string]any{"bridge": "br1", "name": "eth1"}},
		{"move, two words", "br0 br9", map[string]any{"bridge": "br1", "name": "eth1"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			device, rc, record := standInSwitch(t, tc.bridge)
			tc.params["insecure_skip_host_key_verify"] = true
			_, err := ovs.Port(context.Background(), rc, device, tc.params)
			if err == nil || !strings.Contains(err.Error(), "names the bridge of eth1 as") || !strings.Contains(err.Error(), "not an interface name") {
				t.Fatalf("Port = %v, want the reported bridge refused", err)
			}
			if calls := sent(t, record); len(calls) != 0 {
				t.Errorf("ovs-vsctl was asked to change something: %v", calls)
			}
			if _, ok := rc.stats["inverse"]; ok {
				t.Errorf("an undo was recorded: %v", rc.stats["inverse"])
			}
		})
	}
	t.Run("control", func(t *testing.T) {
		device, rc, record := standInSwitch(t, "br0")
		// The stand-in leaves the port where it was, so the read-back after
		// del-port finds it still there and the run fails after sending; what
		// matters here is what was sent.
		_, _ = ovs.Port(context.Background(), rc, device, map[string]any{"bridge": "br0", "name": "eth1", "state": "absent", "insecure_skip_host_key_verify": true})
		if calls := sent(t, record); len(calls) != 1 || calls[0] != "del-port br0 eth1" {
			t.Errorf("calls = %v, want one del-port br0 eth1", calls)
		}
	})
}
