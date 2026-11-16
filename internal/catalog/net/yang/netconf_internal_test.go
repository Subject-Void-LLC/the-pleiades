// Package yang: net.yang.schema over NETCONF, through the real session (a
// real SSH connection, the netconf subsystem, pkg/netconf's client) against an
// in-process server scripted with a hello and a get-schema answer. Netopeer2
// itself is in live_container_test.go.
package yang

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// netconfDevice is a NETCONF device at a scripted server's address.
type netconfDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *netconfDevice) SSHHost() string   { return d.host }
func (d *netconfDevice) SSHPort() int      { return d.port }
func (d *netconfDevice) NetconfPort() int  { return d.port }
func (d *netconfDevice) CLIPrompt() string { return "" }

// The hello capabilities a scripted server advertises: the probe as a
// module, and ietf-netconf-monitoring, which offers get-schema. The server
// writes them into its hello as they are, so each "&" is written escaped.
const (
	probeCapability      = "urn:pleiades:probe?module=pleiades-probe&amp;revision=2026-11-16"
	monitoringCapability = netconf.MonitoringNamespace + "?module=ietf-netconf-monitoring&amp;revision=2010-10-04"
)

// scripted starts a server advertising capabilities and answering
// get-schema with source, and returns a device and context pointed at it.
func scripted(t *testing.T, source string, capabilities ...string) (*netconfDevice, *testContext) {
	t.Helper()
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(source)
	srv, err := remoteexectest.Start(remoteexectest.Options{Netconf: &remoteexectest.NetconfDevice{
		Capabilities: capabilities,
		Replies:      map[string]string{"get-schema": `<data xmlns="` + netconf.MonitoringNamespace + `">` + escaped + `</data>`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	rc := newContext(t)
	rc.secrets = srv.Secrets()
	return &netconfDevice{Stub: &inventorytest.Stub{StubID: "nc1", StubName: "nc1", Caps: netconfOnly}, host: srv.Host, port: srv.Port}, rc
}

// TestNETCONF_FetchesWhatTheHelloNames reads the modules a server with no
// YANG library names in its hello, asks get-schema for the probe, and saves
// it byte for byte, saying the host key was not verified.
func TestNETCONF_FetchesWhatTheHelloNames(t *testing.T) {
	dev, rc := scripted(t, probeSource, probeCapability, monitoringCapability)
	if _, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}, "insecure_skip_host_key_verify": true}); err != nil {
		t.Fatal(err)
	}
	if got, ok := rc.read(t, "yang/nc1/pleiades-probe@2026-11-16.yang"); !ok || got != probeSource {
		t.Fatalf("saved %q, want the probe byte for byte", got)
	}
	warnings, _ := rc.stats["warnings"].([]string)
	if len(warnings) == 0 || !strings.Contains(warnings[0], "host key was not verified") {
		t.Errorf("warnings = %v, want the unverified host key said", warnings)
	}
}

// TestNETCONF_RefusesAServerOfferingNoGetSchema names the other protocol,
// and so does a host key no known_hosts names.
func TestNETCONF_RefusesAServerOfferingNoGetSchema(t *testing.T) {
	dev, rc := scripted(t, probeSource, probeCapability)
	_, err := Schema(context.Background(), rc, dev, map[string]any{"insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "offers no get-schema: fetch over RESTCONF with via: restconf") {
		t.Errorf("a server without get-schema: %v", err)
	}
	if _, err := Schema(context.Background(), rc, dev, map[string]any{}); err == nil || !strings.Contains(err.Error(), "over netconf") {
		t.Errorf("an unknown host key: %v, want the task's refusal", err)
	}
}

// TestNETCONF_OpenRefusesADeviceWithNoPort names the missing capability, a
// device dispatch reached around the manifest's requirement.
func TestNETCONF_OpenRefusesADeviceWithNoPort(t *testing.T) {
	_, err := openNETCONF(context.Background(), newContext(t), &inventorytest.Stub{StubName: "x", Caps: []capability.Name{capability.NameNetconf}}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "does not expose a NETCONF port") {
		t.Errorf("openNETCONF = %v", err)
	}
}
