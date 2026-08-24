package main_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// This file is Phase 86.5's Release Gate for net.ios.config, run against
// the real DevNet Catalyst 8000 Always-On sandbox (a real Catalyst 8000V
// running IOS XE), not a container: nothing else in this repository is a
// real, independently-implemented Cisco IOS CLI to test a PTY-based,
// vendor-specific session against, and inventing a fake one would only
// prove this package agrees with its own assumptions about what IOS
// says back -- exactly the class of "verified" that turned out false
// for Phase 86.5's own predecessor spec (FAILURE_PATTERNS.md #202).
//
// The device is a SHARED resource used by every other DevNet sandbox
// visitor at the same time, and its own rules forbid anything that
// could affect connectivity. Every write this gate makes is creating,
// then in the same run removing, one loopback interface: a loopback
// carries no traffic and cannot affect reachability to or through the
// device. See examples/catalyst8000_lab/README.md for the same round
// trip as a runnable demo runbook, including two real bugs this exact
// exercise found and fixed (a pager hang net.cli.command had no way to
// answer, and net.ios.config's own backup stat leaking the device's
// real enable secret/TACACS+ key unmasked).
//
// Gated on PLEIADES_E2E_IOS; skips rather than fails when unset, or when
// PLEIADES_E2E_IOS_USER/PLEIADES_E2E_IOS_PASS are unset. Credentials
// come from the environment only, never a literal in this file: the
// sandbox issues a fresh, unique password per reservation, so
// hardcoding one here would both leak a live credential and silently
// rot the moment that reservation expires.
const catalyst8000DefaultHost = "devnetsandboxiosxec8k.cisco.com"

func requireCatalyst8000(t *testing.T) (host, user, pass string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping net.ios.config Release Gate in short mode")
	}
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run net.ios.config's Release Gate against the real DevNet Catalyst 8000 Always-On sandbox")
	}
	user = os.Getenv("PLEIADES_E2E_IOS_USER")
	pass = os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set: this sandbox issues a unique password per reservation, so there is no default credential to fall back to")
	}
	host = os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = catalyst8000DefaultHost
	}
	return host, user, pass
}

// probeIOSPrompt opens its own independent connection and reads the
// device's own first exec-mode prompt, so this gate's own net.cli.command
// task (via add-host --set cli_prompt=) is matched against the device's
// REAL prompt text rather than a value this file assumes. The pattern
// used here is deliberately loose (matches WHERE, not what, the prompt
// is): once this probe captures the real text, every later read in this
// gate goes through netcli.FromPrompt matched against that exact
// string, never a heuristic.
func probeIOSPrompt(t *testing.T, host, user, pass string) string {
	t.Helper()
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: 22}, remoteexec.PasswordAuth(user, pass))
	if err != nil {
		t.Fatalf("probing the device's own prompt: connect: %v", err)
	}
	defer conn.Close()

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("probing the device's own prompt: shell: %v", err)
	}
	defer shell.Close()

	loose := regexp.MustCompile(`[\w.\-]+#`)
	out, err := shell.ReadUntil(ctx, loose, 4096)
	if err != nil {
		t.Fatalf("probing the device's own prompt: %v", err)
	}
	return strings.TrimSpace(out)
}

// verifyOverIOS opens its own independent PTY session to the device
// (never through pleiades or this package's own openSession seam) and
// runs command, returning its output. This is the same "verified on the
// device, not by reading our own log output" proof
// ssh_release_gate_test.go's own verifyOverSSH gives exec.command;
// net.ios.config needs a PTY-capable version of it, because "show
// running-config" cannot be asked over a one-shot exec channel the way
// verifyOverSSH's plain command can -- and, unlike exec.command's own
// verification, using pkg/netcli here is not circular: it is the
// already-independently-proven primitive (pkg/netcli/netcli_test.go,
// pkg/netcli/netcli_ssh_test.go, pkg/netcli/live_probe_test.go), the
// same role golang.org/x/crypto/ssh itself plays in verifyOverSSH.
func verifyOverIOS(t *testing.T, host, user, pass, command string) string {
	t.Helper()
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: host, Port: 22}, remoteexec.PasswordAuth(user, pass))
	if err != nil {
		t.Fatalf("independent verification connect to %s failed: %v", host, err)
	}
	defer conn.Close()

	shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("independent verification shell failed: %v", err)
	}
	defer shell.Close()

	session, err := netcli.Open(ctx, shell, netcli.IOS, netcli.Options{})
	if err != nil {
		t.Fatalf("independent verification session failed: %v", err)
	}
	defer session.Close()

	out, err := session.Command(ctx, command)
	if err != nil {
		t.Fatalf("independent verification command %q failed: %v", command, err)
	}
	return out
}

// TestCLI_RunAppliesAndRevertsIOSConfig is net.ios.config's Release
// Gate. It proves, against a real device:
//
//  1. net.cli.command reaches the device for real, matched against a
//     prompt this test probed rather than assumed.
//  2. net.ios.config creates a loopback interface and a real backup
//     (netcli.IOS's own "terminal length 0" handling a genuinely large
//     "show running-config" response without truncation or a hang).
//  3. The interface's real, on-device state is checked over a
//     connection this test opened itself, never by trusting pleiades's
//     own printed output.
//  4. net.ios.config removes the interface again, leaving the shared
//     sandbox exactly as this test found it.
//  5. Removal is proven the same independent way: IOS refuses "show
//     running-config interface <name>" for a name that no longer
//     exists, rather than returning empty output, so that refusal IS
//     the proof of absence.
func TestCLI_RunAppliesAndRevertsIOSConfig(t *testing.T) {
	host, user, pass := requireCatalyst8000(t)

	prompt := probeIOSPrompt(t, host, user, pass)
	if prompt == "" {
		t.Fatal("probeIOSPrompt returned an empty prompt")
	}

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	hostKey := captureRealHostKeyFor(t, host+":22", user, pass)
	knownHostsLine := knownhosts.Line([]string{host + ":22"}, hostKey)
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", "cat8000", "--type", "cisco_router",
		"--set", "host="+host, "--set", "port=22", "--set", "cli_prompt="+prompt); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", "cat8000",
		"--username", user, "--password", pass); err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}

	// A distinctive, process-unique interface number: this sandbox is a
	// shared resource, and os.Getpid() is the same collision-avoidance
	// convention TestGenerate_ReleaseGate already uses for its own
	// process-unique scaffold package name.
	ifName := fmt.Sprintf("Loopback%d", 40000+os.Getpid()%10000)
	description := "pleiades-release-gate"

	createRunbook := filepath.Join(dir, "runbooks", "create.yaml")
	createContent := "id: net-ios-config-gate-create\n" +
		"hosts: cat8000\n" +
		"tasks:\n" +
		"  - name: read-the-software-version\n" +
		"    net.cli.command:\n" +
		"      command: show version | include Version\n" +
		"    register: version\n" +
		"  - name: create-the-loopback\n" +
		"    net.ios.config:\n" +
		"      backup: true\n" +
		"      lines:\n" +
		"        - interface " + ifName + "\n" +
		"        - description " + description + "\n" +
		"    register_mask: backup\n"
	if err := os.WriteFile(createRunbook, []byte(createContent), 0o644); err != nil {
		t.Fatalf("failed to write create runbook: %v", err)
	}

	revertRunbook := filepath.Join(dir, "runbooks", "revert.yaml")
	revertContent := "id: net-ios-config-gate-revert\n" +
		"hosts: cat8000\n" +
		"tasks:\n" +
		"  - name: remove-the-loopback\n" +
		"    net.ios.config:\n" +
		"      lines:\n" +
		"        - no interface " + ifName + "\n"
	if err := os.WriteFile(revertRunbook, []byte(revertContent), 0o644); err != nil {
		t.Fatalf("failed to write revert runbook: %v", err)
	}

	// Always attempt cleanup, even if an assertion below fails partway
	// through: this test must not be the reason a stray interface is
	// left on a resource other people are using at the same time.
	defer func() {
		_, _ = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/revert.yaml")
	}()

	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/create.yaml", "--verbose")
	if err != nil {
		t.Fatalf("create run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run complete") {
		t.Fatalf("expected a successful create run, got:\n%s", out)
	}
	if strings.Contains(out, pass) {
		t.Errorf("the stored password appeared in pleiades's own output:\n%s", out)
	}
	if strings.Contains(out, "backup:\n") && !strings.Contains(out, "backup: ********") {
		t.Errorf("the backup stat was not masked in --verbose output:\n%s", out)
	}
	if got := nodeStatus(t, out, 0); got != "changed" {
		t.Errorf("create run: read-the-software-version reported %q, want %q", got, "changed")
	}
	if got := nodeStatus(t, out, 1); got != "changed" {
		t.Errorf("create run: create-the-loopback reported %q, want %q", got, "changed")
	}

	// Proof on the device, over a connection this test opened itself.
	present := verifyOverIOS(t, host, user, pass, "show running-config interface "+ifName)
	if !strings.Contains(present, description) {
		t.Fatalf("expected %s's running-config to contain %q, got:\n%s", ifName, description, present)
	}

	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/revert.yaml", "--verbose")
	if err != nil {
		t.Fatalf("revert run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run complete") {
		t.Fatalf("expected a successful revert run, got:\n%s", out)
	}
	if got := nodeStatus(t, out, 0); got != "changed" {
		t.Errorf("revert run: remove-the-loopback reported %q, want %q", got, "changed")
	}

	absent := verifyOverIOS(t, host, user, pass, "show running-config interface "+ifName)
	if !strings.Contains(absent, "Invalid input") {
		t.Fatalf("expected IOS to refuse show running-config for a removed interface, got:\n%s", absent)
	}
	if strings.Contains(absent, description) {
		t.Fatalf("the interface still carries its description after revert:\n%s", absent)
	}
}

// captureRealHostKeyFor is ssh_release_gate_test.go's own
// captureRealHostKey, parameterized by user/pass: that file's version
// hardcodes the container's own fixed test credentials, which this
// gate's real, per-reservation ones cannot reuse.
func captureRealHostKeyFor(t *testing.T, addr, user, pass string) ssh.PublicKey {
	t.Helper()
	var captured ssh.PublicKey
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return nil
		},
		Timeout: 15 * time.Second,
	}
	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
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
		t.Fatal("expected to capture a real host key from the device")
	}
	return captured
}
