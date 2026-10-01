// Release gate for Phase 111's generic device types: through the real
// binary, each is added, onboarded against a real server that speaks its
// protocol, and used by a method only its discovered capability admits.
package main_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// genericOnboarded is `pleiades onboard --json`'s answer.
type genericOnboarded struct {
	State        string         `json:"state"`
	Capabilities []string       `json:"capabilities"`
	Facts        map[string]any `json:"facts"`
}

// onboardJSON runs `pleiades onboard <name> --json` and decodes it.
func onboardJSON(t *testing.T, dir string, env map[string]string, name string) genericOnboarded {
	t.Helper()
	out, err := runPleiadesWithEnv(t, dir, env, "onboard", name, "--json", "--timeout", "90s")
	if err != nil {
		t.Fatalf("onboard %s: %v\n%s", name, err, out)
	}
	var res genericOnboarded
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("onboard --json printed %q: %v", out, err)
	}
	if res.State != "active" {
		t.Fatalf("onboarded %s is %s", name, res.State)
	}
	return res
}

// startGateContainer starts req and returns its host and the mapped port.
func startGateContainer(t *testing.T, req testcontainers.ContainerRequest, port string) (string, int) {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		_ = testcontainers.TerminateContainer(c) // a failed start still returns its container
		t.Fatalf("starting %s: %v", req.Image, err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	return host, int(mapped.Num())
}

// trustHostKey writes a known_hosts file holding addr's host key, read
// from the server's own handshake, and returns its path.
func trustHostKey(t *testing.T, addr string) string {
	t.Helper()
	var key ssh.PublicKey
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User: "probe",
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k
			return nil
		},
		Timeout: 30 * time.Second,
	})
	if err == nil {
		_ = client.Close()
	}
	if key == nil {
		t.Fatalf("reading %s's host key: %v", addr, err)
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{addr}, key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestGenericReleaseGate_SSH onboards a real Debian host as generic_ssh:
// the probe must find apt and the account tools, and the package and
// account methods must then reach it.
func TestGenericReleaseGate_SSH(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the generic_ssh release gate, which builds and runs a real Debian sshd, in short mode")
	}
	host, port := startGateContainer(t, testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{Context: filepath.Join("testdata", "debian-sshd")},
		ExposedPorts:   []string{"22/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("22/tcp").WithStartupTimeout(3*time.Minute),
			testsupport.SSHGreeting("22/tcp"),
		),
	}, "22/tcp")
	env := map[string]string{remoteexec.KnownHostsEnv: trustHostKey(t, net.JoinHostPort(host, strconv.Itoa(port)))}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "deb1", "--type", "generic_ssh", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "deb1", "--username", "root", "--password", debianGatePassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	res := onboardJSON(t, dir, env, "deb1")
	for _, want := range []string{"LinuxCapable", "AptCapable", "PosixAccountCapable", "ShellExecCapable"} {
		if !slices.Contains(res.Capabilities, want) {
			t.Errorf("the probe did not find %s: %v", want, res.Capabilities)
		}
	}
	if slices.Contains(res.Capabilities, "DnfCapable") || res.Facts["os_id"] != "debian" {
		t.Errorf("probed %+v", res)
	}

	writeFile(t, dir, "runbooks/reach.yaml", "id: reach\nhosts: deb1\ntasks:\n"+
		"  - name: make a group\n    fqcn: identity.group.create\n    params:\n      name: genericgate\n"+
		"  - name: the ssh server is installed\n    fqcn: pkg.install\n    params:\n      name: openssh-server\n")
	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/reach.yaml"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	// Read back over a session of the test's own, never from pleiades's
	// output.
	if out := gateSSHRun(t, net.JoinHostPort(host, strconv.Itoa(port)), "getent group genericgate"); !strings.HasPrefix(out, "genericgate:") {
		t.Fatalf("the group is not on the device: %q", out)
	}
}

// gateSSHRun runs cmd on the Debian gate device as root, over a
// connection this test opens itself, and returns its output.
func gateSSHRun(t *testing.T, addr, cmd string) string {
	t.Helper()
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password(debianGatePassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- the test's own read-back of a throwaway container it just started
		Timeout:         30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	out, err := session.CombinedOutput(cmd)
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// Notconf's own image and credential, as pkg/netconf's conformance suite
// pins them: a real RFC 6241 server (Netopeer2).
const (
	gateNotconfImage    = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"
	gateNotconfPassword = "admin"
)

// gateNACMPath selects the NACM subtree the gate edits, a leaf every
// Netopeer2 server has.
var gateNACMPath = datastore.Path{Elem: []datastore.PathElem{
	{Name: "nacm", Namespace: "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"},
}}

// TestGenericReleaseGate_NETCONF onboards a real NETCONF server as
// generic_netconf and edits its running datastore with
// net.netconf.config, then reads the edit back over a session of its own.
func TestGenericReleaseGate_NETCONF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the generic_netconf release gate, which runs a real NETCONF server, in short mode")
	}
	host, port := startGateContainer(t, testcontainers.ContainerRequest{
		Image:        gateNotconfImage,
		ExposedPorts: []string{"830/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog("Listening on :::830 for SSH connections").WithStartupTimeout(2*time.Minute),
			testsupport.SSHGreeting("830/tcp"),
		),
	}, "830/tcp")
	knownHosts := trustHostKey(t, net.JoinHostPort(host, strconv.Itoa(port)))
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "nc1", "--type", "generic_netconf", "--set", "host=" + host, "--set", "netconf_port=" + strconv.Itoa(port)},
		{"add-credential", "nc1", "--username", "admin", "--password", gateNotconfPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	res := onboardJSON(t, dir, env, "nc1")
	urns, _ := res.Facts["netconf_capabilities"].([]any)
	if !slices.Equal(res.Capabilities, []string{"NetconfCapable"}) || !slices.Contains(urns, any(netconf.CapabilityWritableRunning)) {
		t.Fatalf("probed %+v", res)
	}

	const disable = `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><enable-external-groups>false</enable-external-groups></nacm>`
	writeFile(t, dir, "runbooks/edit.yaml", "id: edit\nhosts: nc1\ntasks:\n  - name: turn external groups off\n    fqcn: net.netconf.config\n    params:\n      content: '"+disable+"'\n")
	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/edit.yaml"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := remoteexec.New(remoteexec.Options{KnownHostsPath: knownHosts}).Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port}, remoteexec.PasswordAuth("admin", gateNotconfPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatal(err)
	}
	s, err := netconf.Open(ctx, sub, netconf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	got, err := s.GetConfig(ctx, gateNACMPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("the edit did not land: %s", got.Bytes)
	}
}

// TestGenericReleaseGate_HTTP onboards a real HTTPS API as generic_http,
// verified against a certificate authority the binary trusts through
// SSL_CERT_FILE, and calls it with http.request's device mode: the
// device's stored credential goes to the device's own URL.
func TestGenericReleaseGate_HTTP(t *testing.T) {
	caFile, cert := gateIssue(t)
	var interfaces atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gate-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/openapi.json":
			_, _ = w.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Gate API","version":"1"},"paths":{"/interfaces":{}}}`))
		case "/api/interfaces":
			interfaces.Add(1)
			_, _ = w.Write([]byte(`{"interfaces":["eth0"]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	env := map[string]string{"SSL_CERT_FILE": caFile, "SSL_CERT_DIR": filepath.Dir(caFile)}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "api1", "--type", "generic_http", "--set", "base_url=" + srv.URL + "/api", "--set", "http_auth=bearer", "--set", "openapi_path=/openapi.json"},
		{"add-credential", "api1", "--username", "token", "--password", "gate-token"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	res := onboardJSON(t, dir, env, "api1")
	if !slices.Equal(res.Capabilities, []string{"HTTPAPICapable"}) || res.Facts["title"] != "Gate API" {
		t.Fatalf("probed %+v", res)
	}
	writeFile(t, dir, "runbooks/api.yaml", "id: api\nhosts: api1\ntasks:\n  - name: read the interfaces\n    fqcn: http.request\n    params:\n      url: /interfaces\n")
	out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/api.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	// The server answers the path only when the device's own credential
	// came with the request, so one hit is the proof that the relative
	// URL was joined to the device's base URL and authenticated.
	if interfaces.Load() != 1 {
		t.Errorf("%d authorized requests for /api/interfaces, want one", interfaces.Load())
	}
	if strings.Contains(out, "gate-token") {
		t.Error("the run printed the device's credential")
	}
}

// gateIssue writes a CA to a file and returns it with a leaf for
// 127.0.0.1 that CA signed.
func gateIssue(t *testing.T) (string, tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "generic gate CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}
}

// gateGRPCImage is the gRPC project's own example server, which serves a
// greeter plus the standard health and reflection services. Its README:
// "It also supports health and reflection services". Pinned by digest.
const gateGRPCImage = "grpc/java-example-hostname@sha256:49d4e43816f6a5ea28dd309fb63ea318cdaa2afb760b6b6ef01d4f25a6d34a59"

// TestGenericReleaseGate_GRPC onboards a real third-party gRPC server as
// generic_grpc: the probe must reach it and record the services its
// reflection reports.
func TestGenericReleaseGate_GRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the generic_grpc release gate, which runs a real gRPC server, in short mode")
	}
	host, port := startGateContainer(t, testcontainers.ContainerRequest{
		Image:        gateGRPCImage,
		ExposedPorts: []string{"50051/tcp"},
		// The server's own line, not the port: Docker Desktop's port proxy
		// accepts a connection before the JVM listens, and the image has no
		// shell to check the port from inside.
		// gRPC sends no line to read, so the port check stands in for a
		// greeting: it dials the mapped port as well as checking inside.
		WaitingFor: wait.ForAll(
			wait.ForLog("Listening on port 50051").WithStartupTimeout(2*time.Minute),
			wait.ForListeningPort("50051/tcp").WithStartupTimeout(2*time.Minute),
		),
	}, "50051/tcp")
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "grpc1", "--type", "generic_grpc", "--set", "target=" + net.JoinHostPort(host, strconv.Itoa(port)), "--set", "grpc_plaintext=true"},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	res := onboardJSON(t, dir, nil, "grpc1")
	services, _ := res.Facts["services"].([]any)
	if !slices.Equal(res.Capabilities, []string{"GRPCCapable"}) || !slices.Contains(services, any("helloworld.Greeter")) {
		t.Fatalf("probed %+v", res)
	}
}
