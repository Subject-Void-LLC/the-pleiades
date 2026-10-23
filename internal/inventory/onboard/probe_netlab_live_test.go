// Tests that the SSH probe grants FRR and Open vSwitch on real devices
// that answer as the connecting account, and nothing on one that does not.
package onboard

import (
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSSHProbe_GrantsFRRAndOpenVSwitchOnRealDevices: a generic_ssh device
// on a real FRR router is granted FRRCapable with the version vtysh gave,
// and one on a real Open vSwitch switch OpenVSwitchCapable, each probed as
// the unprivileged netops account the device's own tools answer.
func TestSSHProbe_GrantsFRRAndOpenVSwitchOnRealDevices(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	link := testsupport.NetLabLink{Name: "r1-sw1", Subnet: netip.MustParsePrefix("10.201.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.201.0.2"), "sw1": netip.MustParseAddr("10.201.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1"}, []string{"sw1"}, []testsupport.NetLabLink{link})

	var lines []byte
	for _, n := range lab.Nodes {
		lines = append(lines, []byte(knownhosts.Line([]string{net.JoinHostPort(n.Host, strconv.Itoa(n.Port))}, n.HostKey)+"\n")...)
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, path)
	secrets := map[string]string{wire.SecretUsername: testsupport.NetLabUser, wire.SecretPrivateKeyPEM: string(lab.PrivateKeyPEM)}

	for _, tc := range []struct {
		node     string
		want     capability.Name
		notWant  capability.Name
		factKey  string
		factWant string
	}{
		{"r1", capability.NameFRR, capability.NameOpenVSwitch, generic.FactFRRVersion, "10.1.1"},
		{"sw1", capability.NameOpenVSwitch, capability.NameFRR, generic.FactOVSVersion, ""},
	} {
		t.Run(tc.node, func(t *testing.T) {
			n := lab.Nodes[tc.node]
			dev := build(t, generic.TypeSSH, map[string]inventory.PropertyValue{"host": n.Host, "port": n.Port})
			got, err := sshProber{}.Probe(context.Background(), dev, secrets)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(got.Capabilities, tc.want) || slices.Contains(got.Capabilities, tc.notWant) {
				t.Errorf("%s was granted %v, want %s and not %s", tc.node, got.Capabilities, tc.want, tc.notWant)
			}
			if v, _ := got.Facts[tc.factKey].(string); v == "" || (tc.factWant != "" && v != tc.factWant) {
				t.Errorf("%s's %s = %q, want %q", tc.node, tc.factKey, v, tc.factWant)
			}
		})
	}
}
