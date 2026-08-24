package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// This file is Phase 74a's Release Gate for net.netconf.config, run
// against the real DevNet Catalyst 8000 Always-On sandbox. It shares
// net_ios_config_release_gate_test.go's gating helpers
// (requireCatalyst8000, probeIOSPrompt, verifyOverIOS,
// captureRealHostKeyFor) and its credential rule verbatim: the
// username and password come from the environment only, never from a
// literal here, because the sandbox issues a fresh password per
// reservation.
//
// # The verification is deliberately cross-protocol
//
// Every claim this gate makes about the device is checked over an
// INTERACTIVE CLI session that this test opens itself, not by reading
// the change back over the same NETCONF session that made it. Reading
// back over the same session proves the server echoes what it was
// sent; reading it back through a completely different protocol proves
// the configuration actually changed. That is a strictly stronger
// assertion and it costs one extra connection.
//
// # Safety on a shared device
//
// The only write is creating, then in the same run removing, one
// loopback interface, whose number is derived per process so two
// concurrent sandbox users cannot collide. A loopback carries no
// traffic and cannot affect reachability to or through the device,
// which is what the sandbox's own rules forbid affecting. Nothing here
// runs "write memory": everything this gate does is gone on the next
// reload, on a device we do not own.
//
// # What this gate cannot cover, stated rather than left to be found
//
// The candidate datastore, commit, discard-changes and lock/unlock are
// NOT exercised here, because this device does not advertise
// :candidate at all: it offers :writable-running only, which was
// verified directly against it rather than assumed. Those operations
// are covered against a real, independently-implemented NETCONF server
// (Netopeer2) by pkg/netconf's own container conformance suite. Between
// the two, every operation this client implements is proven against
// some real server, and neither target alone would have been enough.

// netconfPortForGate is the port this gate reaches NETCONF on.
//
// It is 830 and not 22, and that is a measured fact rather than a
// convention followed. NETCONF over SSH is a subsystem on an ordinary
// SSH connection, so port 22 should serve it; this device ACCEPTS the
// "netconf" subsystem request on port 22 and then immediately ends the
// channel, serving NETCONF only on 830. A client that checked only the
// subsystem request's reply would report this device as working.
const netconfPortForGate = "830"

func TestCLI_RunAppliesAndRevertsNetconfConfig(t *testing.T) {
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

	// The host key is captured for the NETCONF port specifically. A key
	// recorded for host:22 would not satisfy verification of a
	// connection to host:830 even on the same device, since known_hosts
	// keys are per host-and-port. Getting this wrong would look like a
	// host key mismatch rather than like a missing entry, which is
	// exactly the kind of confusing failure worth pinning.
	netconfAddr := host + ":" + netconfPortForGate
	hostKey := captureRealHostKeyFor(t, netconfAddr, user, pass)
	knownHostsLine := knownhosts.Line([]string{netconfAddr}, hostKey)
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	// netconf_enabled is what makes this device satisfy NetconfCapable
	// at all: the capability's structural half (NetconfPort) is on the
	// type unconditionally, and this property is its data half. Without
	// it the dispatcher refuses the task at plan time, which is the
	// behavior a device with NETCONF switched off should get.
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", "cat8000", "--type", "cisco_router",
		"--set", "host="+host,
		"--set", "port=22",
		"--set", "cli_prompt="+prompt,
		"--set", "netconf_enabled=true",
		"--set", "netconf_port="+netconfPortForGate); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", "cat8000",
		"--username", user, "--password", pass); err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}

	// Process-unique, matching net.ios.config's own gate: this sandbox
	// is shared and two concurrent runs must not fight over one
	// interface.
	loopbackNumber := 40000 + os.Getpid()%10000
	ifName := fmt.Sprintf("Loopback%d", loopbackNumber)
	description := "pleiades-netconf-gate"

	const nativeNS = "http://cisco.com/ns/yang/Cisco-IOS-XE-native"

	createRunbook := filepath.Join(dir, "runbooks", "create.yaml")
	createContent := "id: net-netconf-config-gate-create\n" +
		"hosts: cat8000\n" +
		"tasks:\n" +
		"  - name: create-the-loopback\n" +
		"    net.netconf.config:\n" +
		"      backup: true\n" +
		"      content: |\n" +
		"        <native xmlns=\"" + nativeNS + "\">\n" +
		"          <interface>\n" +
		"            <Loopback>\n" +
		fmt.Sprintf("              <name>%d</name>\n", loopbackNumber) +
		"              <description>" + description + "</description>\n" +
		"            </Loopback>\n" +
		"          </interface>\n" +
		"        </native>\n" +
		"    register_mask: backup\n"
	if err := os.WriteFile(createRunbook, []byte(createContent), 0o644); err != nil {
		t.Fatalf("failed to write create runbook: %v", err)
	}

	// The delete uses default_operation: none plus an nc:operation
	// attribute on the one element being removed. Together those mean
	// "change nothing except what this document explicitly names";
	// default_operation: replace with the same document would apply
	// replace semantics to <native> and <interface> as well, which on a
	// live router means replacing every interface.
	revertRunbook := filepath.Join(dir, "runbooks", "revert.yaml")
	revertContent := "id: net-netconf-config-gate-revert\n" +
		"hosts: cat8000\n" +
		"tasks:\n" +
		"  - name: remove-the-loopback\n" +
		"    net.netconf.config:\n" +
		"      default_operation: none\n" +
		"      content: |\n" +
		"        <native xmlns=\"" + nativeNS + "\">\n" +
		"          <interface>\n" +
		"            <Loopback xmlns:nc=\"urn:ietf:params:xml:ns:netconf:base:1.0\" nc:operation=\"delete\">\n" +
		fmt.Sprintf("              <name>%d</name>\n", loopbackNumber) +
		"            </Loopback>\n" +
		"          </interface>\n" +
		"        </native>\n"
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
	// The backup stat is a full device configuration and genuinely
	// contains this device's own secrets; net.ios.config's gate found
	// exactly that leak on the CLI path, and a NETCONF get-config
	// returns the same material in XML.
	if strings.Contains(out, "backup:\n") && !strings.Contains(out, "backup: ********") {
		t.Errorf("the backup stat was not masked in --verbose output:\n%s", out)
	}
	if got := nodeStatus(t, out, 0); got != "changed" {
		t.Errorf("create run: create-the-loopback reported %q, want %q", got, "changed")
	}

	// Proof on the device, over an INTERACTIVE CLI session this test
	// opens itself: a different protocol, not a read-back over the
	// session that made the change.
	present := verifyOverIOS(t, host, user, pass, "show running-config interface "+ifName)
	if !strings.Contains(present, description) {
		t.Fatalf("expected %s's running-config to contain %q after a NETCONF edit-config, got:\n%s", ifName, description, present)
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
