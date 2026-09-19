// Package main_test: the Walk-tier Release Gate for external Collections
// (IMPLEMENTATION.md Phase 45's Runner-path test plan).
package main_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The file the gate's runbook writes on the device, and what it holds.
const (
	runnerExternalPath    = "/tmp/pleiades-runner-external-collection.txt"
	runnerExternalContent = "written by an external collection through the runner"
)

// buildApprovedExample builds examples/external_collection into a fresh
// directory the loader accepts and approves that exact build, the way an
// image build would, and returns the directory.
func buildApprovedExample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "note"), "../../examples/external_collection")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example external Collection: %v\n%s", err, out)
	}
	digest, _, err := loader.Inspect(context.Background(), dir, "note", loader.Options{})
	if err != nil {
		t.Fatalf("inspecting the example: %v", err)
	}
	if err := loader.Approve(dir, loader.Approval{Program: "note", Digest: digest, ApprovedBy: "release-gate", ApprovedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("approving the example: %v", err)
	}
	return dir
}

// readOverSSH runs command on the device at addr over a connection this
// test opens itself, verifying the host key it captured, and returns the
// output.
func readOverSSH(t *testing.T, addr, command string) string {
	t.Helper()
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            releaseGateSSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(releaseGateSSHPassword)},
		HostKeyCallback: ssh.FixedHostKey(captureRealHostKey(t, addr)),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dialing %s to verify: %v", addr, err)
	}
	defer func() { _ = client.Close() }()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	out, _ := session.CombinedOutput(command)
	return strings.TrimSpace(string(out))
}

// TestExternalCollectionReleaseGate_TheRunnerRunsAnApprovedProgram is the
// Walk-tier path end to end: a real, approved external program, loaded as
// the Runner loads it, a real dispatch over a real NATS JetStream stream
// carrying the credential the Controller resolved at fan-out, the real
// runner.Agent and native.Adapter taking the in-process route for the
// external method, the program running confined, and the file it writes
// verified on a real sshd over a connection this test opens itself.
func TestExternalCollectionReleaseGate_TheRunnerRunsAnApprovedProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier external Collection Release Gate container test in short mode")
	}
	t.Cleanup(collection.SnapshotForTest())

	collections := buildApprovedExample(t)
	if _, err := loader.Load(context.Background(), collections, loader.Options{}); err != nil {
		t.Fatalf("loading the approved example: %v", err)
	}

	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"note.yaml": "id: note\ntasks:\n  - name: leave-a-note\n    fqcn: example.note.write\n    params:\n" +
			"      path: " + runnerExternalPath + "\n      content: \"" + runnerExternalContent + "\"\n",
	})
	addr := net.JoinHostPort(h.sshHost, strconv.Itoa(h.sshPort))

	evt := h.dispatch(t, wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "note",
		DeviceID:     "release-gate-device",
		DeviceName:   "release-gate-device",
		DeviceHost:   h.sshHost,
		SSHPort:      h.sshPort,
		Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
	})
	// A fresh device has no such file, so a successful run changed it.
	if evt.Status != "changed" {
		t.Fatalf("the job did not report writing the file: %+v", evt)
	}
	if got := readOverSSH(t, addr, "cat "+runnerExternalPath+" 2>/dev/null || echo ABSENT"); got != runnerExternalContent {
		t.Fatalf("the device holds %q, want what the external program was asked to write", got)
	}
	if strings.Contains(evt.EventData.Message, releaseGateSSHPassword) {
		t.Error("the credential appeared in the job's event")
	}
}

// buildRunner builds this package's own binary, the real Runner.
func buildRunner(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pleiades-runner")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the runner: %v\n%s", err, out)
	}
	return bin
}

// TestExternalCollectionReleaseGate_TheRunnerRefusesABadDirectoryAtStartup
// runs the real Runner binary with a collections directory it must refuse,
// and no broker to connect to. It must stop at once, before trying to join
// the mesh, naming the reason. A Runner that loaded external Collections
// only after connecting would instead hang on the unreachable broker, and
// this test would time out.
func TestExternalCollectionReleaseGate_TheRunnerRefusesABadDirectoryAtStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Runner startup test in short mode")
	}
	bin := buildRunner(t)

	writable := t.TempDir()
	if err := os.Chmod(writable, 0o777); err != nil { // #nosec G302 -- the fixture under test
		t.Fatal(err)
	}
	unapproved := t.TempDir()
	if err := os.Chmod(unapproved, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unapproved, "stranger"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}

	for dir, want := range map[string]string{
		writable:   "group- or world-writable",
		unapproved: "is not approved to run",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, bin)
		cmd.Env = append(os.Environ(), "PLEIADES_COLLECTIONS_DIR="+dir, "NATS_URL=nats://127.0.0.1:1")
		out, err := cmd.CombinedOutput()
		cancel()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("the runner did not stop on a bad collections directory; it went on toward the broker:\n%s", out)
		}
		if err == nil || !strings.Contains(string(out), want) {
			t.Errorf("runner with %s = %v, want a refusal containing %q:\n%s", dir, err, want, out)
		}
	}
}
