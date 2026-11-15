// Release gate for net.restconf.config's undo: through the real binary, a
// generic_restconf device for rousette (over sysrepo, in the notconf image
// the NETCONF gates pin) is onboarded, a runbook creates a NACM group whose
// name holds a slash and a comma, and pleiades rollback deletes it again from
// the undo the run recorded.
package main_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// restconfRollbackRunbook creates one group.
const restconfRollbackRunbook = `# Creates the NACM group gate/rollback,1 on rc1 over RESTCONF.
# Blast radius: rc1's access-control configuration, one group. Changes: the
# group is created. Undo: pleiades rollback deletes it, from the undo the run
# records.
id: restconf-create
hosts: rc1
tasks:
  - name: Make the gate group
    net.restconf.config:
      method: put
      path: ietf-netconf-acm:nacm/groups/group=gate%2Frollback%2C1
      content: '{"ietf-netconf-acm:group": [{"name": "gate/rollback,1", "user-name": ["erin"]}]}'
`

// restconfReport runs the binary with --json in dir and decodes its report.
func restconfReport(t *testing.T, dir string, args ...string) gateReport {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...) // #nosec G204 -- the test's own binary
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	var rep gateReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("pleiades %v --json printed no report: %v\nstdout: %s\nstderr: %s", args, err, out, stderr.String())
	}
	return rep
}

// gateGroupThere reads whether the gate group is on rousette, over RESTCONF.
func gateGroupThere(t *testing.T, origin string) bool {
	t.Helper()
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c, err := restconf.New(origin, hc, httpapi.AuthBasic, true, map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: gateNotconfPassword})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := c.DiscoverRoot(ctx); err != nil {
		t.Fatal(err)
	}
	_, found, err := c.Read(ctx, "", restconf.Path{restconf.Node("ietf-netconf-acm", "nacm"), restconf.Node("", "groups"), restconf.Node("", "group").Entry("gate/rollback,1")}, restconf.MediaTypeJSON)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestGenericReleaseGate_RESTCONFRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the RESTCONF rollback release gate, which runs a real RESTCONF server, in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        gateNotconfImage,
			ExposedPorts: []string{"80/tcp"},
			WaitingFor:   wait.ForHTTP("/.well-known/host-meta").WithPort("80/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "80/tcp")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + net.JoinHostPort(host, strconv.Itoa(int(port.Num())))

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "rc1", "--type", "generic_restconf", "--set", "base_url=" + origin, "--set", "http_allow_plaintext_credentials=true"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// The password on standard input, never argv, as every gate sends one.
	if out, err := runPleiadesWithStdin(t, dir, gateNotconfPassword+"\n", "add-credential", "rc1", "--username", "admin", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if res := onboardJSON(t, dir, nil, "rc1"); res.Facts["restconf_root"] != "/restconf" {
		t.Fatalf("onboarded %+v", res)
	}
	writeFile(t, dir, "runbooks/create.yaml", restconfRollbackRunbook)
	run := restconfReport(t, dir, "run", "runbooks/create.yaml")
	if run.Outcome.ExitCode != 0 || run.RunID == "" || !gateGroupThere(t, origin) {
		t.Fatalf("the create run %+v left the group there = %v", run, gateGroupThere(t, origin))
	}
	rollback := restconfReport(t, dir, "rollback", run.RunID)
	if rollback.Outcome.ExitCode != 0 || len(rollback.methods()) != 1 || !strings.HasPrefix(rollback.methods()[0], "net.restconf.config ") {
		t.Fatalf("the rollback %+v, want one net.restconf.config step", rollback)
	}
	if gateGroupThere(t, origin) {
		t.Error("the group is still there after the rollback")
	}
}
