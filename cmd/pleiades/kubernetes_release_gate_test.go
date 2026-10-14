// Release gate for Phase 119c: through the real binary, a kubeconfig
// context becomes a kubernetes_cluster device and its credential, and
// onboarding reaches a real k3s API server with it.
package main_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestCLI_KubernetesClusterFromKubeconfig is the user's path to a cluster:
// import-kubeconfig, then onboard. It imports k3s's own kubeconfig (a
// client certificate) and a token kubeconfig, and proves a kubeconfig that
// would run a program is refused with nothing added.
func TestCLI_KubernetesClusterFromKubeconfig(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	files := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A kubeconfig that would run a program is refused by name, and leaves
	// the inventory as it was.
	execConfig := strings.Replace(k.Kubeconfig, "client-certificate-data:", "exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: evil\n    client-certificate-data:", 1)
	if out, err := runPleiades(t, dir, "import-kubeconfig", "bad", "--kubeconfig", write("exec.yaml", execConfig)); err == nil || !strings.Contains(out, "exec") {
		t.Fatalf("import of an exec kubeconfig: %v\n%s; want a refusal naming exec", err, out)
	}
	if out, _ := runPleiades(t, dir, "inventory", "list"); strings.Contains(out, "bad") {
		t.Errorf("a refused import added a host:\n%s", out)
	}

	// k3s's own kubeconfig: the administrator's client certificate.
	out, err := runPleiades(t, dir, "import-kubeconfig", "k3s", "--kubeconfig", write("k3s.yaml", k.Kubeconfig), "--json")
	if err != nil || !strings.Contains(out, `"identity": "client certificate"`) || !strings.Contains(out, `"pinned_ca": true`) {
		t.Fatalf("import-kubeconfig: %v\n%s", err, out)
	}
	res := onboardJSON(t, dir, nil, "k3s")
	if !slices.Contains(res.Capabilities, "KubernetesCapable") {
		t.Errorf("onboarded capabilities = %v, want KubernetesCapable", res.Capabilities)
	}
	if v, _ := res.Facts["kubernetes_version"].(string); !strings.HasPrefix(v, "v1.37.") {
		t.Errorf("kubernetes_version = %q", v)
	}

	// A token kubeconfig, onboarded as a second cluster.
	token := k.ServiceAccountToken(t, "", "importer", "cluster-admin")
	tokenConfig := "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: " + k.Server + "\n    certificate-authority-data: " + b64(k.CAPEM) + "\n" +
		"users:\n- name: u\n  user:\n    token: " + token + "\n"
	if out, err := runPleiades(t, dir, "import-kubeconfig", "k3s-token", "--kubeconfig", write("token.yaml", tokenConfig)); err != nil {
		t.Fatalf("import of a token kubeconfig: %v\n%s", err, out)
	} else if strings.Contains(out, token) {
		t.Errorf("the import printed the token:\n%s", out)
	}
	if res := onboardJSON(t, dir, nil, "k3s-token"); !slices.Contains(res.Capabilities, "KubernetesCapable") {
		t.Errorf("token cluster capabilities = %v", res.Capabilities)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) || !strings.Contains(string(raw), "token_encrypted") {
		t.Errorf("the token is not stored encrypted in the credential file")
	}
}

// b64 is s base64-encoded, as a kubeconfig embeds data.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestCLI_KubernetesNodesAreFoundOnTheirEngine is the Docker Desktop shape:
// a cluster whose node is a container on this machine's engine. The engine
// is onboarded as a container_host with no address, the cluster names it
// as its node engine host, and onboarding records which container the node
// is, proved by the machine ID read from inside it.
func TestCLI_KubernetesNodesAreFoundOnTheirEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	reachable := exec.Command("docker", "info").Run() == nil // #nosec G204 -- fixed arguments
	testsupport.Require(t, "docker", reachable, "this machine's Docker engine does not answer")
	machineID := randomHex(t, 16)
	k := testsupport.StartK3s(t, testsupport.K3sOptions{MachineID: machineID})
	insp, err := k.Container.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "engine", "--type", "container_host"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	onboardJSON(t, dir, nil, "engine")
	path := filepath.Join(t.TempDir(), "k3s.yaml")
	if err := os.WriteFile(path, []byte(k.Kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "import-kubeconfig", "k3s", "--kubeconfig", path); err != nil {
		t.Fatalf("import-kubeconfig: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "set-host", "k3s", "--set", "node_engine_host=engine"); err != nil {
		t.Fatalf("set-host: %v\n%s", err, out)
	}

	res := onboardJSON(t, dir, nil, "k3s")
	nodes, _ := res.Facts["node_containers"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("node_containers = %v, want the one node's container", res.Facts["node_containers"])
	}
	node, _ := nodes[0].(map[string]any)
	if node["container"] != insp.ID || node["engine"] != "docker" {
		t.Errorf("the node's container = %v, want %s on docker", node, insp.ID)
	}
}

// randomHex is n random bytes as hex, a machine ID's shape.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// TestCLI_KubernetesClusterThroughABastion is a cluster reachable only
// through a jump host: its API server is known by an alias on a Docker
// network this machine is not on, and an sshd on that network is the
// bastion. Without a route the onboarding cannot reach the server; a route
// whose hop has no credential is refused naming the hop; with both, the
// cluster is onboarded through the bastion, its host key verified.
func TestCLI_KubernetesClusterThroughABastion(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server and a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("creating a network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	const alias = "k3s-api"
	k := testsupport.StartK3s(t, testsupport.K3sOptions{Networks: []string{nw.Name}, Alias: alias})
	bastion, err := testsupport.StartSSHDOn(ctx, nw.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bastion.Terminate(context.Background()) })
	knownHosts, err := bastion.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "bastion", "--type", "linux_server", "--set", "host="+bastion.Host, "--set", "port="+strconv.Itoa(bastion.Port)); err != nil {
		t.Fatalf("add-host bastion: %v\n%s", err, out)
	}
	// The server as the bastion's network knows it, which this machine
	// cannot resolve.
	path := filepath.Join(t.TempDir(), "k3s.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(k.Kubeconfig, k.Server, "https://"+alias+":6443", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "import-kubeconfig", "k3s", "--kubeconfig", path); err != nil {
		t.Fatalf("import-kubeconfig: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "onboard", "k3s", "--json", "--timeout", "30s"); err == nil {
		t.Fatalf("onboarding a cluster this machine cannot reach, with no route, succeeded:\n%s", out)
	}

	// The route, as hosts.yaml carries one.
	hostsPath := filepath.Join(dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hosts {
		if hosts[i].Name == "k3s" {
			hosts[i].Properties["route"] = []any{"bastion"}
		}
	}
	if err := inventory.WriteHosts(hostsPath, hosts); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "onboard", "k3s", "--json", "--timeout", "30s"); err == nil || !strings.Contains(out, `"bastion"`) {
		t.Fatalf("onboarding through a hop with no credential: %v\n%s; want a refusal naming the hop", err, out)
	}

	if out, err := runPleiadesWithStdin(t, dir, testsupport.SSHDPassword, "add-credential", "bastion", "--username", testsupport.SSHDUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential bastion: %v\n%s", err, out)
	}
	// The bastion's host key is checked like any SSH connection's: with a
	// known_hosts that does not name it, the route is refused.
	unknown := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(unknown, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, dir, map[string]string{"PLEIADES_KNOWN_HOSTS": unknown}, "onboard", "k3s", "--json", "--timeout", "30s"); err == nil || !strings.Contains(out, "key is unknown") {
		t.Fatalf("onboarding through a bastion whose host key is unknown: %v\n%s; want a host key refusal", err, out)
	}
	res := onboardJSON(t, dir, env, "k3s")
	if !slices.Contains(res.Capabilities, "KubernetesCapable") {
		t.Errorf("onboarded capabilities = %v, want KubernetesCapable", res.Capabilities)
	}
	if v, _ := res.Facts["kubernetes_version"].(string); !strings.HasPrefix(v, "v1.37.") {
		t.Errorf("kubernetes_version = %q", v)
	}

	// Importing the context again with --replace replaces only what the
	// kubeconfig owns: the route stays, and the cluster still onboards
	// through it.
	if out, err := runPleiades(t, dir, "import-kubeconfig", "k3s", "--kubeconfig", path, "--replace"); err != nil {
		t.Fatalf("import-kubeconfig --replace: %v\n%s", err, out)
	}
	if hosts, err = inventory.ReadHosts(hostsPath); err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.Name == "k3s" && fmt.Sprint(h.Properties["route"]) != "[bastion]" {
			t.Errorf("the replacing import left route %v, want [bastion]", h.Properties["route"])
		}
	}
	onboardJSON(t, dir, env, "k3s")
}

// dockerDesktopKubeconfigEnv names a kubeconfig holding Docker Desktop's
// docker-desktop context, for the opt-in test below. The test reads only
// the file it names, never a default location.
const dockerDesktopKubeconfigEnv = "PLEIADES_TEST_DOCKER_DESKTOP_KUBECONFIG"

// TestCLI_DockerDesktopClusterAsIs is Docker Desktop's own cluster, opt in:
// its docker-desktop context imports with no edit (a loopback API server
// and a client certificate) and onboards, and its node is found as a
// container on this machine's engine. Everything it asks the cluster only
// reads: the version, discovery, a rules review, the node list, and the
// node's machine ID through the engine.
func TestCLI_DockerDesktopClusterAsIs(t *testing.T) {
	path := os.Getenv(dockerDesktopKubeconfigEnv)
	testsupport.Require(t, "docker-desktop", path != "", dockerDesktopKubeconfigEnv+" does not name a kubeconfig holding the docker-desktop context")
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "engine", "--type", "container_host"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	onboardJSON(t, dir, nil, "engine")
	out, err := runPleiades(t, dir, "import-kubeconfig", "desktop", "--kubeconfig", path, "--context", "docker-desktop", "--json")
	if err != nil || !strings.Contains(out, `"identity": "client certificate"`) || !strings.Contains(out, `"pinned_ca": true`) {
		t.Fatalf("import-kubeconfig: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "set-host", "desktop", "--set", "node_engine_host=engine"); err != nil {
		t.Fatalf("set-host: %v\n%s", err, out)
	}
	res := onboardJSON(t, dir, nil, "desktop")
	if !slices.Contains(res.Capabilities, "KubernetesCapable") {
		t.Errorf("onboarded capabilities = %v, want KubernetesCapable", res.Capabilities)
	}
	nodes, _ := res.Facts["nodes"].([]any)
	mapped, _ := res.Facts["node_containers"].([]any)
	if len(nodes) == 0 || len(mapped) != len(nodes) {
		t.Fatalf("%d nodes, %d found on the engine; want every node found: %v", len(nodes), len(mapped), res.Facts["node_containers"])
	}
	for _, m := range mapped {
		node, _ := m.(map[string]any)
		if id, _ := node["container"].(string); len(id) != 64 || node["engine"] != "docker" {
			t.Errorf("node mapped to %v, want a Docker container ID", node)
		}
	}
}

// TestCLI_KubernetesNeverUsesTheProcessesOwnServiceAccount runs the real
// binary where an in-cluster Runner runs, with everything a pod offers a
// Kubernetes client: the in-cluster service address, a ServiceAccount token
// bound to cluster-admin with its authority and namespace at the
// in-cluster path, and a KUBECONFIG naming k3s's administrator. A cluster
// with no stored credential is refused, and one whose stored credential
// may do nothing sees only what that credential may: the process's own
// identity never stands in for, or adds to, a cluster's credential.
func TestCLI_KubernetesNeverUsesTheProcessesOwnServiceAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	ctx := context.Background()
	k := testsupport.StartK3s(t, testsupport.K3sOptions{})

	// The binary as a static executable, which runs in the server's
	// container as it would in a Runner's pod.
	bin := filepath.Join(t.TempDir(), "pleiades")
	build := exec.Command("go", "build", "-o", bin, ".") // #nosec G204 -- fixed arguments
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building a static pleiades: %v\n%s", err, out)
	}
	if err := k.Container.CopyFileToContainer(ctx, bin, "/opt/pleiades", 0o755); err != nil {
		t.Fatal(err)
	}

	// What a pod's process sees.
	const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"
	admin := k.ServiceAccountToken(t, "", "in-cluster-runner", "cluster-admin")
	if code, _, err := k.Container.Exec(ctx, []string{"mkdir", "-p", saDir, "/work"}); err != nil || code != 0 {
		t.Fatalf("preparing the in-cluster paths: %v (exit %d)", err, code)
	}
	for name, body := range map[string]string{"token": admin, "ca.crt": k.CAPEM, "namespace": "default"} {
		if err := k.Container.CopyToContainer(ctx, []byte(body), saDir+"/"+name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ip, err := k.Kubectl(ctx, "get", "service", "kubernetes", "-o", "jsonpath={.spec.clusterIP}")
	if err != nil {
		t.Fatal(err)
	}
	server := "https://" + strings.TrimSpace(ip) + ":443"
	env := []string{"KUBERNETES_SERVICE_HOST=" + strings.TrimSpace(ip), "KUBERNETES_SERVICE_PORT=443",
		"KUBECONFIG=/etc/rancher/k3s/k3s.yaml", "HOME=/work"}
	run := func(args ...string) (string, error) {
		code, reader, err := k.Container.Exec(ctx, append([]string{"/opt/pleiades"}, args...), tcexec.Multiplexed(), tcexec.WithEnv(env), tcexec.WithWorkingDir("/work"))
		if err != nil {
			return "", err
		}
		out, _ := io.ReadAll(reader)
		if code != 0 {
			return string(out), fmt.Errorf("pleiades %s exited %d", args[0], code)
		}
		return string(out), nil
	}
	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// No stored credential: refused, though a cluster-admin token is a
	// file away.
	if out, err := run("add-host", "bare", "--type", "kubernetes_cluster", "--set", "api_server="+server); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := run("onboard", "bare", "--json"); err == nil || !strings.Contains(out, "no credential") {
		t.Fatalf("onboarding a cluster with no stored credential in a pod: %v\n%s; want a refusal naming the missing credential", err, out)
	}

	// A stored credential that may do nothing: onboarding sees its rights,
	// not the pod's.
	nobody := k.ServiceAccountToken(t, "default", "nobody", "")
	config := "apiVersion: v1\nkind: Config\ncurrent-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u}\n" +
		"clusters:\n- name: k\n  cluster:\n    server: " + server + "\n    certificate-authority-data: " + b64(k.CAPEM) + "\n" +
		"users:\n- name: u\n  user:\n    token: " + nobody + "\n"
	if err := k.Container.CopyToContainer(ctx, []byte(config), "/work/nobody.yaml", 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("import-kubeconfig", "weak", "--kubeconfig", "/work/nobody.yaml"); err != nil {
		t.Fatalf("import-kubeconfig: %v\n%s", err, out)
	}
	out, err := run("onboard", "weak", "--json")
	if err != nil {
		t.Fatalf("onboard: %v\n%s", err, out)
	}
	var res genericOnboarded
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("onboard --json printed %q: %v", out, err)
	}
	if _, ok := res.Facts["nodes"]; ok {
		t.Errorf("a credential that may not list nodes saw them: %v", res.Facts["nodes"])
	}
	rules, _ := res.Facts["rules"].([]any)
	for _, r := range rules {
		if s, _ := r.(string); strings.HasPrefix(s, "*") {
			t.Errorf("the powerless credential holds rule %q, the pod's cluster-admin", s)
		}
	}
	if len(rules) == 0 {
		t.Error("no rules recorded at all, so the test proves nothing about whose they are")
	}
}
