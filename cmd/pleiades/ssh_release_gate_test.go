package main_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// This file is Phase W6's own Release Gate: ".SPECIFICATION/IMPLEMENTATION.md"
// requires "a runbook run from the CLI configures a real device, verified
// on the device and not by reading our own log output." The "device" here
// is a real, independently-implemented sshd (lscr.io/linuxserver/openssh-server,
// the same image and recipe internal/transport/ssh's own container tests
// already proved works in this environment), reached over the network the
// real built pleiades binary actually dials, exactly like the NATS/Postgres
// containers earlier Crawl phases used as this repository's own definition
// of "real" infrastructure it does not own physical hardware for.

const (
	releaseGateSSHUser     = "testuser"
	releaseGateSSHPassword = "release-gate-w6-password"
)

// startReleaseGateContainer starts a fresh openssh-server container for
// this test only (not shared with internal/transport/ssh's own suite,
// which lives in a different package/test binary entirely) and returns
// its externally reachable host and port.
func startReleaseGateContainer(t *testing.T) (string, int) {
	t.Helper()
	_, host, port := startReleaseGateSSHD(t)
	return host, port
}

// startReleaseGateSSHD is startReleaseGateContainer that also returns the
// container, for a test that reads the server's own log.
func startReleaseGateSSHD(t *testing.T) (testcontainers.Container, string, int) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        testsupport.SSHDImage,
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"PASSWORD_ACCESS": "true",
			"USER_NAME":       releaseGateSSHUser,
			"USER_PASSWORD":   releaseGateSSHPassword,
		},
		// Both halves, because the log line alone has twice let a test
		// past a port it could not reach. "[ls.io-init] done." is the
		// image's own init chain finishing, which says nothing about the
		// host side of the mapping, and this package's two recorded
		// flakes are both on that side: `port "2222/tcp" not found`
		// (flaky-packages.json's own entry for this package) and, under
		// the make ci run of 2026-09-13, a mapped port that answered the
		// dial with connection refused. ForListeningPort dials the
		// published port FROM the host in a retry loop, which is the
		// check the log strategy cannot make, so the container is not
		// declared ready until the address every test here uses actually
		// accepts a connection. Each step names the bound itself: a step
		// with none stops at testcontainers' own sixty seconds whatever
		// the group allows (FAILURE_PATTERNS 350).
		WaitingFor: wait.ForAll(
			wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
			wait.ForListeningPort("2222/tcp").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		).WithStartupTimeout(testsupport.SSHDStartupTimeout),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start openssh-server container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}
	mapped, err := container.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}
	return container, host, int(mapped.Num())
}

// captureRealHostKey opens a bootstrap connection to addr using a
// HostKeyCallback that only records the presented key instead of
// verifying it, the same role ssh-keyscan (or a human's first-ever
// StrictHostKeyChecking=ask connection) plays in legitimately discovering
// a new host's key before it can be pinned. This is independent of
// anything internal/transport/ssh does at Exec time; it exists purely so
// this test can populate a real known_hosts file the same way an actual
// operator would, before asking the real binary to connect through the
// default, fail-closed host key verification path.
func captureRealHostKey(t *testing.T, addr string) ssh.PublicKey {
	t.Helper()
	var captured ssh.PublicKey
	config := &ssh.ClientConfig{
		User: releaseGateSSHUser,
		Auth: []ssh.AuthMethod{ssh.Password(releaseGateSSHPassword)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return nil
		},
		Timeout: 10 * time.Second,
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatalf("bootstrap dial to %s failed: %v", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		t.Fatalf("bootstrap ssh handshake with %s failed: %v", addr, err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	if captured == nil {
		t.Fatal("expected to capture a real host key from the container")
	}
	return captured
}

// verifyOverSSH opens its own independent SSH connection to addr (never
// through pleiades, internal/transport/ssh, or anything this test is
// exercising) and runs command, returning its combined stdout+stderr.
// This is the "verified on the device, not by reading our own log
// output" half of the Release Gate: it proves the runbook's ssh_exec task
// really executed against the real container, using a second,
// independent path to ask the container itself.
func verifyOverSSH(t *testing.T, addr, command string) string {
	t.Helper()
	config := &ssh.ClientConfig{
		User:            releaseGateSSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(releaseGateSSHPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // verification-only connection; host key behavior has its own dedicated tests
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		t.Fatalf("independent verification dial to %s failed: %v", addr, err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("independent verification session failed: %v", err)
	}
	defer session.Close()
	out, err := session.CombinedOutput(command)
	if err != nil {
		t.Fatalf("independent verification command %q failed: %v\noutput: %s", command, err, out)
	}
	return string(out)
}

// runPleiadesWithHome is runPleiades (e2e_test.go) plus control over the
// subprocess's HOME, so this test can point the real binary's default
// known_hosts lookup ("$HOME/.ssh/known_hosts", internal/transport/ssh's
// own applyDefaults) at a temp directory this test populated with the
// container's real captured host key, exactly like a real operator's own
// $HOME/.ssh/known_hosts would be populated after a first legitimate
// connection.
func runPleiadesWithHome(t *testing.T, dir, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir

	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "HOME="+home)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestCLI_RunExecutesSSHTransport is Phase W6's Release Gate. It drives
// the real built pleiades binary through init, add-host, add-credential,
// and run against a real, independently-implemented sshd, with real
// fail-closed host key verification (a real known_hosts file populated
// with the container's real captured key, the same way an operator's own
// would be), then verifies the runbook's command genuinely ran by asking
// the container directly over a second, independent SSH connection this
// test opens itself, never by trusting pleiades's own stdout.
func TestCLI_RunExecutesSSHTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH Release Gate container test in short mode")
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	realKey := captureRealHostKey(t, addr)
	knownHostsLine := knownhosts.Line([]string{addr}, realKey)
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	dir := t.TempDir()

	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", "container1", "--type", "linux_server",
		"--set", "host="+host, "--set", "port="+strconv.Itoa(port)); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}

	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", "container1",
		"--username", releaseGateSSHUser, "--password", releaseGateSSHPassword); err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}

	const marker = "/tmp/pleiades-w6-release-gate-marker"
	runbook := filepath.Join(dir, "runbooks", "ssh.yaml")
	content := "id: ssh-release-gate\n" +
		"tasks:\n" +
		"  - name: prove-real-ssh\n" +
		"    fqcn: ssh_exec\n" +
		"    params:\n" +
		"      target: container1\n" +
		"      command: \"mkdir -p $(dirname " + marker + ") && echo verified-by-pleiades-w6 > " + marker + "\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/ssh.yaml")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "changed") {
		t.Errorf("expected the ssh_exec task to report changed (Changed defaults true), got:\n%s", out)
	}
	if !strings.Contains(out, "run complete") {
		t.Errorf("expected a successful run, got:\n%s", out)
	}
	if strings.Contains(out, releaseGateSSHPassword) {
		t.Errorf("expected the stored password never to appear in pleiades's own output, got:\n%s", out)
	}

	// The actual proof: ask the container itself, over a connection this
	// test opened independently of pleiades, whether the command really
	// ran. Reading pleiades's own "changed" claim above is not this
	// proof; this is.
	verification := verifyOverSSH(t, addr, "cat "+marker)
	if !strings.Contains(verification, "verified-by-pleiades-w6") {
		t.Fatalf("expected the marker file written by the real ssh_exec task to be readable independently on the device, got:\n%s", verification)
	}
}
