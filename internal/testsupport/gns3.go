// A real GNS3 server for a test, of either API line, with VPCS nodes whose
// telnet consoles a test reaches at the numbers the server reports.
package testsupport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// GNS3BaseImage is the Python the GNS3 test servers are built on, pulled;
// the servers themselves are built (testdata/gns3) and never pulled.
const GNS3BaseImage = "python:3.12.12-slim-bookworm"

// The GNS3 server releases the importer is proven against, one per API
// line (Phase 127c's spike).
const (
	GNS3Version3 = "3.0.6"
	GNS3Version2 = "2.2.61"
)

// The account a fresh 3.x server logs in; a 2.2.x server asks for none.
const (
	GNS3AdminUser     = "admin"
	GNS3AdminPassword = "admin"
)

// gns3ConsolePorts is how many console ports a test server has.
const gns3ConsolePorts = 4

// GNS3Server is a running GNS3 server.
type GNS3Server struct {
	// URL is its base URL, http://127.0.0.1:<port>.
	URL string
	// API is "v3" or "v2".
	API string
	// ConsolePorts are the console ports its nodes take, published on
	// this machine's loopback at the same numbers.
	ConsolePorts []int

	token string
}

// StartGNS3 builds and starts a GNS3 server of version (GNS3Version3 or
// GNS3Version2), returning it once its API answers. It is removed when tb
// ends.
func StartGNS3(tb testing.TB, version string) *GNS3Server {
	tb.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		tb.Skip("docker not found on PATH")
	}
	tag := "pleiades/gns3:" + version
	src := filepath.Join(RepoRoot(tb), "internal", "testsupport", "testdata", "gns3")
	build := exec.Command("docker", "build", "--build-arg", "BASE="+GNS3BaseImage, "--build-arg", "GNS3_VERSION="+version, "-t", tag, src) // #nosec G204 -- fixed arguments and a path in this repository
	if out, err := build.CombinedOutput(); err != nil {
		tb.Fatalf("building %s: %v\n%s", tag, err, out)
	}
	api := "v3"
	if strings.HasPrefix(version, "2.") {
		api = "v2"
	}

	// The console ports are published at their own numbers, found free a
	// moment before Docker binds them; another test can take one in that
	// moment (seen under make pr-check: "address already in use"), so a
	// start that loses that race picks a fresh range and starts again.
	var c testcontainers.Container
	var ports []int
	var err error
	for attempt := 1; ; attempt++ {
		ports = freeConsoleRange(tb)
		c, err = startGNS3Container(tb, tag, api, ports)
		if err == nil {
			break
		}
		logs := "(no container was created)"
		if c != nil {
			logs = containerLogTail(c, 30)
			_ = c.Terminate(context.Background())
		}
		if !strings.Contains(err.Error(), "address already in use") || attempt == gns3StartAttempts {
			tb.Fatalf("starting GNS3 %s: %v\nits log:\n%s", version, err, logs)
		}
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	mapped, err := c.MappedPort(context.Background(), "3080/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	s := &GNS3Server{URL: "http://127.0.0.1:" + strconv.Itoa(int(mapped.Num())), API: api, ConsolePorts: ports}
	if api == "v3" {
		s.login(tb)
	}
	return s
}

// gns3StartAttempts bounds how many fresh console port ranges a start tries
// after losing one to another process.
const gns3StartAttempts = 5

// startGNS3Container starts the built image with its console ports
// published at their own numbers and the server's console range set to
// them, returning once its API answers.
func startGNS3Container(tb testing.TB, tag, api string, ports []int) (testcontainers.Container, error) {
	tb.Helper()
	conf := filepath.Join(tb.TempDir(), "gns3_server.conf")
	if err := os.WriteFile(conf, fmt.Appendf(nil, "[Server]\nconsole_start_port_range = %d\nconsole_end_port_range = %d\n", ports[0], ports[len(ports)-1]), 0o644); err != nil { // #nosec G306 -- the server reads it as another user inside the container
		return nil, err
	}
	exposed := []string{"3080/tcp"}
	bindings := mobynetwork.PortMap{}
	for _, p := range ports {
		port := mobynetwork.MustParsePort(strconv.Itoa(p) + "/tcp")
		exposed = append(exposed, port.String())
		bindings[port] = []mobynetwork.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(p)}}
	}
	return testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        tag,
			Cmd:          []string{"--config", "/etc/gns3_server.conf"},
			ExposedPorts: exposed,
			Files:        []testcontainers.ContainerFile{{HostFilePath: conf, ContainerFilePath: "/etc/gns3_server.conf", FileMode: 0o644}},
			HostConfigModifier: func(hc *container.HostConfig) {
				// uBridge, which every VPCS node starts, needs it.
				hc.CapAdd = append(hc.CapAdd, "NET_ADMIN")
				if hc.PortBindings == nil {
					hc.PortBindings = mobynetwork.PortMap{}
				}
				for p, b := range bindings {
					hc.PortBindings[p] = b
				}
			},
			WaitingFor: wait.ForHTTP("/" + api + "/version").WithPort("3080/tcp").WithStatusCodeMatcher(func(code int) bool { return code == http.StatusOK }).WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
}

// freeConsoleRange returns gns3ConsolePorts consecutive ports free on this
// machine's loopback, so the server's console ports can be published at
// their own numbers.
func freeConsoleRange(tb testing.TB) []int {
	tb.Helper()
	for range 50 {
		base := 33000 + rand.IntN(20000) // #nosec G404 -- a port to try, not a secret
		var held []net.Listener
		ok := true
		for i := range gns3ConsolePorts {
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(base+i)))
			if err != nil {
				ok = false
				break
			}
			held = append(held, l)
		}
		for _, l := range held {
			_ = l.Close()
		}
		if ok {
			ports := make([]int, gns3ConsolePorts)
			for i := range ports {
				ports[i] = base + i
			}
			return ports
		}
	}
	tb.Fatal("no free range of console ports on loopback")
	return nil
}

// login takes a 3.x server's bearer token for the test's own setup calls.
func (s *GNS3Server) login(tb testing.TB) {
	tb.Helper()
	res, err := http.PostForm(s.URL+"/v3/access/users/login", url.Values{"username": {GNS3AdminUser}, "password": {GNS3AdminPassword}}) // #nosec G107 -- the test's own server
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&t); err != nil || t.AccessToken == "" {
		tb.Fatalf("logging in to GNS3: %d, %v", res.StatusCode, err)
	}
	s.token = t.AccessToken
}

// call sends one setup request and decodes its answer into out, when out
// is not nil.
func (s *GNS3Server) call(tb testing.TB, method, path string, body, out any) {
	tb.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			tb.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, s.URL+"/"+s.API+path, &buf)
	if err != nil {
		tb.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		var msg bytes.Buffer
		_, _ = msg.ReadFrom(res.Body)
		tb.Fatalf("GNS3 %s %s: %d %s", method, path, res.StatusCode, msg.String())
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			tb.Fatal(err)
		}
	}
}

// NewProject makes a project named name and returns its id.
func (s *GNS3Server) NewProject(tb testing.TB, name string) string {
	tb.Helper()
	var p struct {
		ID string `json:"project_id"`
	}
	s.call(tb, http.MethodPost, "/projects", map[string]any{"name": name}, &p)
	return p.ID
}

// AddVPCS adds a VPCS node named name to project and returns its console
// port.
func (s *GNS3Server) AddVPCS(tb testing.TB, project, name string) int {
	tb.Helper()
	var n struct {
		Console int `json:"console"`
	}
	s.call(tb, http.MethodPost, "/projects/"+project+"/nodes", map[string]any{"name": name, "node_type": "vpcs", "compute_id": "local"}, &n)
	return n.Console
}

// StartNodes starts every node of project.
func (s *GNS3Server) StartNodes(tb testing.TB, project string) {
	tb.Helper()
	s.call(tb, http.MethodPost, "/projects/"+project+"/nodes/start", map[string]any{}, nil)
}
