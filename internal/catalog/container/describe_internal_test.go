// container.info's answer built from an inspect response shaped as a real
// engine's: every field the reference names is filled, and every key the
// method records is one its Returns declares.
package container

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// inspected is a running web container on one network, publishing a port
// on the loopback address, with a health check and a past start error.
func inspected(t *testing.T) moby.InspectResponse {
	t.Helper()
	port, err := network.ParsePort("80/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return moby.InspectResponse{
		ID: "abc123", Name: "/web", Image: "sha256:img", Created: "2026-10-27T10:00:00Z", RestartCount: 2, Platform: "linux",
		State: &moby.State{Status: "running", Running: true, Pid: 4242, StartedAt: "2026-10-27T10:00:01Z", FinishedAt: "0001-01-01T00:00:00Z",
			Error: strings.Repeat("x", maxStateError+50), Health: &moby.Health{Status: "healthy"}},
		Config: &moby.Config{Hostname: "web", User: "101", WorkingDir: "/srv", Cmd: []string{"nginx", "-g", "daemon off;"},
			Entrypoint: []string{"/docker-entrypoint.sh"}, Env: []string{"TOKEN=secret-sentinel", "PATH=/usr/bin"}, Labels: map[string]string{"app": "web"}},
		HostConfig: &moby.HostConfig{NetworkMode: "appnet", ReadonlyRootfs: true, CapAdd: []string{"NET_BIND_SERVICE"}, Resources: moby.Resources{Memory: 64 << 20},
			PortBindings: network.PortMap{port: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "8080"}, {HostPort: "8081"}}}},
		NetworkSettings: &moby.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"appnet": {NetworkID: "net1", IPAddress: netip.MustParseAddr("172.20.0.5"), Gateway: netip.MustParseAddr("172.20.0.1"),
				MacAddress: network.HardwareAddr{0x02, 0x42, 0xac, 0x14, 0x00, 0x05}, Aliases: []string{"web"}},
		}},
	}
}

// TestDescribe_ReportsTheWholeContainerAndNoEnvironmentValue holds R5: one
// info call answers ports, networks, health and the rest, bounded, and R3:
// each of them is a declared return.
func TestDescribe_ReportsTheWholeContainerAndNoEnvironmentValue(t *testing.T) {
	got := describe(inspected(t))

	if ports := got["ports"].([]string); !slices.Equal(ports, []string{"127.0.0.1:8080:80/tcp", "8081:80/tcp"}) {
		t.Errorf("ports = %v", ports)
	}
	nets := got["networks"].([]any)
	if len(nets) != 1 {
		t.Fatalf("networks = %v", nets)
	}
	n := nets[0].(map[string]any)
	if n["name"] != "appnet" || n["ip_address"] != "172.20.0.5" || n["gateway"] != "172.20.0.1" || n["mac_address"] != "02:42:ac:14:00:05" {
		t.Errorf("network = %v", n)
	}
	if got["health"] != "healthy" || got["pid"] != 4242 || got["read_only"] != true || got["memory"] != int64(64<<20) || got["restart_count"] != 2 {
		t.Errorf("state fields = %v", got)
	}
	if e := got["state_error"].(string); len(e) > maxStateError+3 || !strings.HasSuffix(e, "...") {
		t.Errorf("state_error is %d bytes, not cut to %d", len(e), maxStateError)
	}
	for k, v := range got {
		if strings.Contains(strings.Join(anyStrings(v), " "), "secret-sentinel") {
			t.Errorf("%s holds an environment value", k)
		}
	}

	declared := map[string]bool{}
	desc, _ := collection.Lookup("container.info")
	for _, r := range desc.Manifest.Doc.Returns {
		declared[r.Name] = true
	}
	for k := range got {
		if !declared[k] {
			t.Errorf("container.info records %s, which its Returns does not declare", k)
		}
	}
}

// TestDescribe_LeavesOutWhatTheEngineDoesNotSay: a stopped container with no
// health check and no start error has neither key, and no address.
func TestDescribe_LeavesOutWhatTheEngineDoesNotSay(t *testing.T) {
	c := inspected(t)
	c.State.Health, c.State.Error = nil, ""
	c.NetworkSettings.Networks["appnet"] = &network.EndpointSettings{NetworkID: "net1"}
	got := describe(c)
	for _, k := range []string{"health", "state_error"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s reported when the engine says nothing of it", k)
		}
	}
	if n := got["networks"].([]any)[0].(map[string]any); n["ip_address"] != nil {
		t.Errorf("a network with no address reports one: %v", n)
	}
}

// TestSummaryPorts writes published and unpublished ports as docker ps does.
func TestSummaryPorts(t *testing.T) {
	got := summaryPorts([]moby.PortSummary{
		{PrivatePort: 80, PublicPort: 8080, Type: "tcp", IP: netip.MustParseAddr("0.0.0.0")},
		{PrivatePort: 80, PublicPort: 8080, Type: "tcp", IP: netip.MustParseAddr("::")},
		{PrivatePort: 443, Type: "tcp"},
		{PrivatePort: 53, PublicPort: 5353, Type: "udp", IP: netip.MustParseAddr("127.0.0.1")},
	})
	want := []string{"127.0.0.1:5353->53/udp", "443/tcp", "8080->80/tcp"}
	if !slices.Equal(got, want) {
		t.Errorf("summaryPorts = %v, want %v", got, want)
	}
}

// anyStrings is every string inside v, for the leak check.
func anyStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []string:
		return x
	case map[string]string:
		var out []string
		for k, s := range x {
			out = append(out, k, s)
		}
		return out
	}
	return nil
}
