// Release Gate for net.trace from a container, through the real binary: a
// container host onboarded as a user's would be (this machine's engine)
// traces from a lab host container with no SSH, through a real FRR router,
// to an address behind it; and validation refuses a call whose device
// cannot take the shape it chose.
package main_test

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestCLI_NetTraceFromAContainerThroughARouter is the gate.
func TestCLI_NetTraceFromAContainerThroughARouter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts routers")
	}
	_, err := os.Stat("/var/run/docker.sock")
	testsupport.Require(t, "docker", err == nil, "the container host is this machine's engine, reached through its socket")
	hostLink := testsupport.NetLabLink{Name: "h1-r1", Subnet: netip.MustParsePrefix("10.217.0.0/29"), Ends: map[string]netip.Addr{
		"h1": netip.MustParseAddr("10.217.0.2"), "r1": netip.MustParseAddr("10.217.0.3"),
	}}
	coreLink := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.217.0.8/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.217.0.10"), "r2": netip.MustParseAddr("10.217.0.11"),
	}}
	lab := testsupport.StartNetLab(t, []string{"h1", "r1", "r2"}, nil, []testsupport.NetLabLink{hostLink, coreLink})
	// h1 is a host: it reaches r2's link through r1, which forwards, and r2
	// answers back through r1. Set up as root, outside what is tested.
	lab.Nodes["r1"].Exec(t, "sysctl", "-w", "net.ipv4.ip_forward=1")
	lab.Nodes["h1"].Exec(t, "ip", "route", "add", "10.217.0.8/29", "via", "10.217.0.3")
	lab.Nodes["r2"].Exec(t, "ip", "route", "add", "10.217.0.0/29", "via", "10.217.0.10")
	inspected, err := lab.Nodes["h1"].Container.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h1 := strings.TrimPrefix(inspected.Name, "/")

	dir := t.TempDir()
	must := func(args ...string) string {
		t.Helper()
		out, err := runPleiades(t, dir, args...)
		if err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	must("init")
	must("add-host", "dockerhost", "--type", "container_host")
	must("onboard", "dockerhost")
	must("add-host", "lin", "--type", "linux_server", "--set", "host=127.0.0.1")

	write := func(name, body string) string {
		t.Helper()
		rel := filepath.Join("runbooks", name)
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	header := "# Traces a path; read only.\n# Blast radius: none.\n# Changes: nothing.\n# Undo: nothing to undo.\n"
	trace := write("trace.yaml", header+`id: trace-from-h1
hosts: dockerhost
tasks:
  - name: h1 reaches r2 through r1
    net.trace:
      container: `+h1+`
      dest: 10.217.0.11
      expect:
        reached: true
        includes: [10.217.0.3]
`)
	if out := must("validate", trace); !strings.Contains(out, "no issues found") {
		t.Fatalf("validate:\n%s", out)
	}
	rep, raw, code := runPleiadesJSON(t, dir, "run", trace)
	if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "ok" {
		t.Fatalf("run: exit %d:\n%s", code, raw)
	}
	var hops []string
	for _, h := range rep.Tasks[0].Stats["hops"].([]any) {
		hops = append(hops, h.(string))
	}
	if !slices.Equal(hops, []string{"10.217.0.3", "10.217.0.11"}) || rep.Tasks[0].Stats["reached"] != true {
		t.Errorf("hops %v, stats %v; want r1 then r2, reached", hops, rep.Tasks[0].Stats)
	}

	// The path held to avoid r1 fails, from the container as from a device.
	rep, raw, code = runPleiadesJSON(t, dir, "adhoc", "dockerhost", "net.trace", "container="+h1, "dest=10.217.0.11", "expect:={avoids: [10.217.0.3]}")
	if code == 0 || len(rep.Tasks) != 1 || !strings.Contains(rep.Tasks[0].Error, "10.217.0.3 is on the path") {
		t.Errorf("a path that should avoid r1: exit %d:\n%s", code, raw)
	}

	// Each shape on a device that cannot take it is refused before anything
	// runs: no container on a container host (no SSH, not Linux), and a
	// container on a Linux server with no engine.
	for name, tc := range map[string]struct{ body, want string }{
		"plain.yaml":     {"id: plain\nhosts: dockerhost\ntasks:\n  - name: trace\n    net.trace:\n      dest: 10.217.0.11\n", "LinuxCapable"},
		"container.yaml": {"id: container\nhosts: lin\ntasks:\n  - name: trace\n    net.trace:\n      container: " + h1 + "\n      dest: 10.217.0.11\n", "ContainerEngineCapable"},
	} {
		out, err := runPleiades(t, dir, "validate", write(name, header+tc.body))
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("validate %s: %v, want a refusal naming %s:\n%s", name, err, tc.want, out)
		}
	}
}
