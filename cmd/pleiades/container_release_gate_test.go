// Release gate for Phases 119a and 119b: through the real binary, a
// container host is onboarded, validate names the carrier each task will
// use, and container.exec reaches the engine the device advertised:
// over a real SSH login to a real Docker engine, on a socket channel or,
// where sshd refuses forwarding, through the engine's own dial-stdio, or
// through this machine's own engine, never through a socket a record typed.
package main_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// containerGateImage is the image the gate runs a container from, pinned
// exactly; it is copied into the fixture's engine from this machine's, so
// the fixture never pulls.
const containerGateImage = "alpine:3.20.10"

// gateContainerName is a container name no other run uses.
func gateContainerName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "pleiades-gate-" + hex.EncodeToString(b)
}

// localDocker runs the docker command on this machine.
func localDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput() // #nosec G204 -- fixed command, arguments from this test
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ensureGateImage makes containerGateImage present on this machine's
// engine, pulling only when it is absent: CI loads it from its image cache
// (tools/testimages reads this file's constant), and a pull of an image
// already present still asks the registry.
func ensureGateImage(t *testing.T) {
	t.Helper()
	if exec.Command("docker", "image", "inspect", containerGateImage).Run() == nil { // #nosec G204 -- fixed arguments
		return
	}
	localDocker(t, "pull", "-q", containerGateImage)
}

// execRunbook writes a runbook running container.exec on host and
// returns its path.
func execRunbook(t *testing.T, dir, host, containerName string) string {
	t.Helper()
	path := filepath.Join(dir, "runbooks", "exec-"+host+".yaml")
	body := "id: exec-" + host + "\n" +
		"hosts: " + host + "\n" +
		"tasks:\n" +
		"  - name: run a command inside the container\n" +
		"    container.exec:\n" +
		"      name: " + containerName + "\n" +
		"      cmd: echo reached $(cat /etc/alpine-release)\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCLI_ContainerHostOverSSH is the user's path to a remote engine:
// add-host with an address, add-credential, onboard, then run. It proves
// the exec reached the device's engine, not this machine's, and that the
// record could not have typed a socket.
func TestCLI_ContainerHostOverSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})

	// A container on the fixture's engine, from an image copied in rather
	// than pulled.
	name := d.StartContainer(t, containerGateImage, "")
	engineID := d.EngineID(t)

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}

	// A typed socket is refused by name: finding S1's fix at the record.
	if out, err := runPleiades(t, dir, "add-host", "typed", "--type", "container_host", "--set", "docker_endpoint=/var/run/docker.sock"); err == nil || !strings.Contains(out, "pleiades onboard") {
		t.Fatalf("add-host with docker_endpoint: %v\n%s; want a refusal naming onboarding", err, out)
	}

	if out, err := runPleiades(t, dir, "add-host", "box", "--type", "container_host", "--set", "host="+d.Host, "--set", "port="+strconv.Itoa(d.Port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, testsupport.DockerHostPassword, "add-credential", "box", "--username", testsupport.DockerHostUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}

	// Before onboarding the method cannot run there, and validate says so.
	runbook := execRunbook(t, dir, "box", name)
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", runbook); err == nil || !strings.Contains(out, "engine_socket") {
		t.Fatalf("validate before onboarding: %v\n%s; want a transport refusal", err, out)
	}

	res := onboardJSON(t, dir, env, "box")
	for _, want := range []string{"DockerCapable", "EngineSocketCapable"} {
		if !slices.Contains(res.Capabilities, want) {
			t.Errorf("onboarded capabilities = %v, want %s", res.Capabilities, want)
		}
	}
	if slices.Contains(res.Capabilities, "EngineLocalCapable") {
		t.Errorf("a remote host was granted EngineLocalCapable: %v", res.Capabilities)
	}
	if res.Facts["docker_id"] != engineID || res.Facts["docker_socket"] != testsupport.DockerHostSocket || res.Facts["login_user"] != testsupport.DockerHostUser {
		t.Errorf("facts = %v, want the fixture engine's ID %q, its socket, and the account it was found as", res.Facts, engineID)
	}

	// validate names the carrier the run will take, decided the way the run
	// decides it: the socket channel, since this sshd allows it.
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", runbook); err != nil || !strings.Contains(out, "engine_socket, the docker engine") {
		t.Errorf("validate after onboarding: %v\n%s; want the route over engine_socket", err, out)
	}

	out, err := runPleiadesWithEnv(t, dir, env, "run", runbook, "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reached 3.20") {
		t.Errorf("run output does not carry the container's answer:\n%s", out)
	}
	// The container exists only on the fixture's engine, so an exec that
	// succeeded reached the device's engine, not this machine's.
	if err := exec.Command("docker", "inspect", name).Run(); err == nil { // #nosec G204 -- a name this test made
		t.Errorf("container %s exists on this machine's engine too, so the gate proves nothing about where the exec went", name)
	}
}

// TestCLI_ContainerHostOverSSH_ForwardingDenied is the host whose sshd
// refuses every forwarded channel (DisableForwarding yes, which also
// overrides AllowStreamLocalForwarding): onboarding finds no socket reach
// and proves the engine's own dial-stdio instead, validate names it, and
// the run reaches the engine over it.
func TestCLI_ContainerHostOverSSH_ForwardingDenied(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{SSHDConfig: "DisableForwarding yes\n"})
	name := d.StartContainer(t, containerGateImage, "")
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "box", "--type", "container_host", "--set", "host="+d.Host, "--set", "port="+strconv.Itoa(d.Port)); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, testsupport.DockerHostPassword, "add-credential", "box", "--username", testsupport.DockerHostUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}

	res := onboardJSON(t, dir, env, "box")
	if !slices.Contains(res.Capabilities, "EngineStdioCapable") || slices.Contains(res.Capabilities, "EngineSocketCapable") {
		t.Errorf("onboarded capabilities = %v, want EngineStdioCapable and not EngineSocketCapable", res.Capabilities)
	}
	carriers, _ := res.Facts["docker_carriers"].([]any)
	if len(carriers) != 1 || carriers[0] != "stdio" {
		t.Errorf("docker_carriers = %v, want only stdio", res.Facts["docker_carriers"])
	}

	runbook := execRunbook(t, dir, "box", name)
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", runbook); err != nil || !strings.Contains(out, "engine_stdio, the docker engine") {
		t.Errorf("validate: %v\n%s; want the route over engine_stdio", err, out)
	}
	out, err := runPleiadesWithEnv(t, dir, env, "run", runbook, "--json")
	if err != nil {
		t.Fatalf("run over dial-stdio: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reached 3.20") {
		t.Errorf("run output does not carry the container's answer:\n%s", out)
	}
}

// TestCLI_ContainerHostOverTLS is the user's path to an engine with no SSH
// login at all, listening behind mutual TLS: add-host with the TLS address
// and the pinned authority, add-credential with the client certificate,
// then onboard, validate and run, the certificate coming from the
// credential store as any device's does.
func TestCLI_ContainerHostOverTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine")
	}
	d, addr, pki := testsupport.StartTLSDockerHost(t)
	name := d.StartContainer(t, containerGateImage, "")

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	files := t.TempDir()
	certPath, keyPath := filepath.Join(files, "client.pem"), filepath.Join(files, "client.key")
	if err := os.WriteFile(certPath, []byte(pki.ClientCert), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(pki.ClientKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "add-host", "tlsbox", "--type", "container_host",
		"--set", "engine_tls="+addr, "--set", "tls_ca_pem="+pki.CAPEM, "--set", "tls_server_name=localhost",
		"--set", "tls_client_certificate=true"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-credential", "tlsbox", "--certificate", certPath, "--key", keyPath); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}

	res := onboardJSON(t, dir, nil, "tlsbox")
	if !slices.Contains(res.Capabilities, "EngineTLSCapable") || slices.Contains(res.Capabilities, "EngineLocalCapable") {
		t.Errorf("onboarded capabilities = %v, want EngineTLSCapable and not EngineLocalCapable", res.Capabilities)
	}
	if res.Facts["docker_id"] != d.EngineID(t) {
		t.Errorf("docker_id = %v, want the fixture engine %q", res.Facts["docker_id"], d.EngineID(t))
	}

	runbook := execRunbook(t, dir, "tlsbox", name)
	if out, err := runPleiades(t, dir, "validate", runbook); err != nil || !strings.Contains(out, "engine_tls, the docker engine") {
		t.Errorf("validate: %v\n%s; want the route over engine_tls", err, out)
	}
	out, err := runPleiades(t, dir, "run", runbook, "--json")
	if err != nil {
		t.Fatalf("run over TLS: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reached 3.20") {
		t.Errorf("run output does not carry the container's answer:\n%s", out)
	}
}

// TestCLI_ContainerHostLocal is the user's path to this machine's own
// engine: a container host with no address, onboarded where pleiades runs,
// then an exec into a container this machine's engine runs.
func TestCLI_ContainerHostLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("uses this machine's Docker engine")
	}
	reachable := exec.Command("docker", "info").Run() == nil // #nosec G204 -- fixed arguments
	testsupport.Require(t, "docker", reachable, "this machine's Docker engine does not answer")

	ensureGateImage(t)
	name := gateContainerName(t)
	localDocker(t, "run", "-d", "--name", name, containerGateImage, "sleep", "600")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() }) // #nosec G204 -- a name this test made
	engineID := localDocker(t, "info", "--format", "{{.ID}}")

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "here", "--type", "container_host"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	res := onboardJSON(t, dir, nil, "here")
	if !slices.Contains(res.Capabilities, "EngineLocalCapable") || slices.Contains(res.Capabilities, "EngineSocketCapable") {
		t.Errorf("onboarded capabilities = %v, want EngineLocalCapable and not EngineSocketCapable", res.Capabilities)
	}
	if res.Facts["docker_id"] != engineID {
		t.Errorf("docker_id = %v, want this machine's engine %q", res.Facts["docker_id"], engineID)
	}
	out, err := runPleiades(t, dir, "run", execRunbook(t, dir, "here", name), "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("run --json printed %q: %v", out, err)
	}
	if !strings.Contains(out, "reached 3.20") {
		t.Errorf("run output does not carry the container's answer:\n%s", out)
	}
}

// TestCLI_AddRegistryCredential stores a registry account through the real
// binary, in the credential file's own encrypted section.
func TestCLI_AddRegistryCredential(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := runPleiadesWithStdin(t, dir, "registry-token-77\n", "add-registry-credential", "https://GHCR.io", "--username", "deployer", "--password-stdin")
	if err != nil || !strings.Contains(out, `"ghcr.io"`) {
		t.Fatalf("add-registry-credential: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "registries:") || !strings.Contains(string(raw), "ghcr.io:") {
		t.Errorf("credentials.yaml has no registries section for ghcr.io:\n%s", raw)
	}
	if strings.Contains(string(raw), "registry-token-77") {
		t.Error("the token is in the credential file in the clear")
	}
	if out, err := runPleiades(t, dir, "add-registry-credential", "ghcr.io"); err == nil {
		t.Errorf("add-registry-credential with no --username succeeded:\n%s", out)
	}
}
