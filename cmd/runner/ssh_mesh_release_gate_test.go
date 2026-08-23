package main_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// This file is Phase 16 (Native Go Execution Adapter)'s own Release Gate:
// "The Runner executes a mock Ping collection, captures the output, and
// pushes a task.completed event back to NATS." It combines the two real
// infrastructure harnesses earlier phases already established rather than
// inventing a third: a real NATS/JetStream container
// (internal/runner/agent_nats_test.go's own pattern) and a real,
// independently-implemented sshd
// (cmd/pleiades/ssh_release_gate_test.go's own pattern, the identical
// image and host-key-capture recipe). Unlike either of those, the Adapter
// under test here spawns real child processes of its own (the per-task
// subprocess boundary, PLAN.md Section 17.5), which is why TestMain below
// exists: this test binary must also be able to play the collection-child
// role when os.Executable() resolves to it, exactly as
// internal/adapters/native's own TestMain does.

// TestMain lets this package's own test binary double as the collection
// child (see internal/adapters/native/ipc_parent_test.go's identical
// TestMain for the full rationale): native.Adapter.Execute spawns
// os.Executable() re-exec'd with InternalCollectionRunnerArg, which
// resolves to this test binary under `go test`, not to a real
// cmd/runner-built binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == native.InternalCollectionRunnerArg {
		os.Exit(native.RunCollectionChild(context.Background()))
	}
	os.Exit(m.Run())
}

const (
	releaseGateSSHUser     = "testuser"
	releaseGateSSHPassword = "release-gate-p16-password"
)

// startSSHContainer starts a fresh openssh-server container and returns
// its externally reachable host and port, mirroring
// cmd/pleiades/ssh_release_gate_test.go's own identical helper exactly
// (a separate copy, not an import: that one lives in package main_test of
// a different binary, cmd/pleiades, which this package cannot reach).
func startSSHContainer(t *testing.T) (string, int) {
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
		WaitingFor: wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
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
	return host, int(mapped.Num())
}

// captureRealHostKey mirrors cmd/pleiades/ssh_release_gate_test.go's own
// identical helper: opens a bootstrap connection that only records the
// presented host key, the same role ssh-keyscan plays for a real
// operator populating a known_hosts file for the first time.
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
	defer func() { _ = client.Close() }()
	if captured == nil {
		t.Fatal("expected to capture a real host key from the container")
	}
	return captured
}

// releaseGateHarness bundles the real NATS and sshd containers plus the
// real Runner-mesh objects (Adapter, Agent) every test in this file needs,
// so each test function stays focused on its own scenario instead of
// repeating container/agent bring-up.
type releaseGateHarness struct {
	sshHost string
	sshPort int
	bus     event.Bus
	js      jetstream.JetStream
}

// knownHostsSource says how a test makes the container's real host key
// reachable by the code under test. The two values are the two real
// deployments, and they are not interchangeable.
//
// This distinction exists because its absence hid a defect. Every gate in
// this repository used the home directory, which is what a person running
// the CLI on their own laptop has and what no container has, so the
// published runner image could not verify a host key at all and nothing
// noticed (FAILURE_PATTERNS.md #150).
type knownHostsSource int

const (
	// knownHostsInHomeDir writes $HOME/.ssh/known_hosts, the Crawl tier's
	// arrangement: a person, a home directory, a file ssh itself would
	// have written.
	knownHostsInHomeDir knownHostsSource = iota

	// knownHostsInEnvironment writes the file somewhere unrelated and
	// names it with remoteexec.KnownHostsEnv, leaving $HOME pointing at a
	// directory with no known_hosts anywhere in it. That is the Walk
	// tier's arrangement, and the empty home directory is load-bearing:
	// it means a pass can only come from the environment variable being
	// read, and read inside the per-task child process, which is a
	// separate OS process from the one this test is running in.
	knownHostsInEnvironment
)

func newReleaseGateHarness(t *testing.T) *releaseGateHarness {
	t.Helper()
	return newReleaseGateHarnessWith(t, knownHostsInHomeDir)
}

// writeKnownHostsFor records addr's real, captured host key where source
// says the code under test will look for it, and returns nothing: the
// effect is entirely in the environment this process hands its children.
//
// $HOME is set in both cases, never unset. Emptying it would also take
// away what the Docker client reads for its own configuration, and this
// test starts containers; a home directory that simply has no known_hosts
// in it fails the fallback just as completely and leaves Docker alone.
func writeKnownHostsFor(t *testing.T, addr string, source knownHostsSource) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	realKey := captureRealHostKey(t, addr)
	line := knownhosts.Line([]string{addr}, realKey) + "\n"

	if source == knownHostsInEnvironment {
		// Deliberately not under $HOME, so nothing about this path could be
		// found by the home directory fallback even by accident.
		path := filepath.Join(t.TempDir(), "deployment_known_hosts")
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
		t.Setenv(remoteexec.KnownHostsEnv, path)
		return
	}

	t.Setenv(remoteexec.KnownHostsEnv, "")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}
}

func newReleaseGateHarnessWith(t *testing.T, source knownHostsSource) *releaseGateHarness {
	t.Helper()
	ctx := context.Background()

	sshHost, sshPort := startSSHContainer(t)
	addr := net.JoinHostPort(sshHost, strconv.Itoa(sshPort))

	// A real known_hosts file, populated with the container's own real
	// captured key, exactly like a real operator's would be after a first
	// legitimate connection. Both Collection methods fail closed when it
	// is missing, so where this lands decides whether the run can work at
	// all.
	writeKnownHostsFor(t, addr, source)

	natsC, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start nats container: %v", err)
	}
	t.Cleanup(func() { _ = natsC.Terminate(context.Background()) })

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}

	runbookDir := t.TempDir()
	runbookYAML := "id: ping\ntasks:\n  - name: ping\n    fqcn: net.ssh.ping\n"
	if err := os.WriteFile(filepath.Join(runbookDir, "ping.yaml"), []byte(runbookYAML), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture: %v", err)
	}
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("failed to init runbook source: %v", err)
	}

	adapter, err := native.NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("failed to init native adapter: %v", err)
	}
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil)

	agentCtx, cancelAgent := context.WithCancel(ctx)
	t.Cleanup(cancelAgent)
	go func() { _ = agent.Run(agentCtx) }()

	return &releaseGateHarness{sshHost: sshHost, sshPort: sshPort, bus: bus, js: js}
}

// dispatch publishes payload to the real dispatch subject and waits for a
// task.completed wire.JobEvent on the real per-job log subject, the same
// two subjects internal/dispatch and internal/adapters/native use in
// production. It never inspects anything the Adapter did beyond what
// crossed the real bus, and each test independently verifies the actual
// device-side effect (or absence of one) itself.
func (h *releaseGateHarness) dispatch(t *testing.T, payload wire.DispatchPayload) wire.JobEvent {
	t.Helper()
	ctx := context.Background()

	logConsumer, err := h.js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.LogViewerConsumerConfig(payload.JobID))
	if err != nil {
		t.Fatalf("failed to create log consumer: %v", err)
	}

	evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
	if err != nil {
		t.Fatalf("wrap payload: %v", err)
	}
	if err := h.bus.Publish(ctx, topology.DispatchSubject(), *evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := logConsumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			t.Fatalf("fetch log events: %v", err)
		}
		for msg := range msgs.Messages() {
			var wrapped event.Event
			if err := json.Unmarshal(msg.Data(), &wrapped); err != nil {
				t.Fatalf("unmarshal event envelope: %v", err)
			}
			var jobEvt wire.JobEvent
			if err := json.Unmarshal(wrapped.Data, &jobEvt); err != nil {
				t.Fatalf("unmarshal job event: %v", err)
			}
			if jobEvt.Task == "task.completed" {
				return jobEvt
			}
		}
	}
	t.Fatal("timed out waiting for a task.completed job event")
	return wire.JobEvent{}
}

// TestSSHMeshReleaseGate_RealSecretThroughTheFullChain is Phase 16's own
// Release Gate. A real wire.DispatchPayload, carrying a real password
// (never a fake or hardcoded one), is published to the real dispatch
// subject; the real runner.Agent pulls it, native.Adapter resolves the
// real compiled DAG, runs it through the real engine.Executor and
// Collection registry, and net.ssh.ping's own real Invoke runs inside a
// real, separately-spawned child OS process (the per-task subprocess
// boundary), which dials a real SSH connection to a real,
// independently-implemented sshd container using the real password and
// runs a real remote command.
//
// Per LESSONS_LEARNED #73 ("the boundary crossing is the feature, not the
// header") and RULE 0 ("verified on the device, not by reading our own
// log output"), this test does not stop at asserting the platform's own
// "ok" status: TestSSHMeshReleaseGate_WrongSecretFails (below) proves the
// credential the child used was the real one, not merely present on the
// wire, by showing authentication genuinely fails with a wrong one.
func TestSSHMeshReleaseGate_RealSecretThroughTheFullChain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarness(t)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:        jobID,
		RunbookID:    "ping",
		DeviceID:     "release-gate-device",
		DeviceName:   "release-gate-device",
		DeviceHost:   h.sshHost,
		SSHPort:      h.sshPort,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
	}

	final := h.dispatch(t, payload)

	if final.Status != "ok" {
		t.Fatalf("final status = %q, want %q (net.ssh.ping never reports changed): message=%q", final.Status, "ok", final.EventData.Message)
	}
	if strings.Contains(final.EventData.Message, releaseGateSSHPassword) {
		t.Errorf("final message leaks the raw password: %q", final.EventData.Message)
	}
}

// TestSSHMeshReleaseGate_WrongSecretFails is the negative control for the
// test above: the same dispatch, against the same real container, but
// with a deliberately wrong password. If the child process's SSH dial
// were not actually using the credential carried on the wire (a stub, a
// bypassed auth check, a hardcoded success), this would still report
// success. It does not: authentication genuinely fails inside the real
// child process, and that failure genuinely propagates back through the
// subprocess boundary, the DAG executor, and onto the real bus.
func TestSSHMeshReleaseGate_WrongSecretFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarness(t)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:        jobID,
		RunbookID:    "ping",
		DeviceID:     "release-gate-device",
		DeviceName:   "release-gate-device",
		DeviceHost:   h.sshHost,
		SSHPort:      h.sshPort,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: "definitely-the-wrong-password"}),
	}

	final := h.dispatch(t, payload)

	if final.Status != "failed" {
		t.Fatalf("final status = %q, want %q: a wrong password must not authenticate", final.Status, "failed")
	}
}

// pingPayload builds the dispatch both host key tests below publish. They
// differ only in where the host key was put, so the payload must not be a
// second variable between them.
func pingPayload(h *releaseGateHarness) wire.DispatchPayload {
	return wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "ping",
		DeviceID:     "release-gate-device",
		DeviceName:   "release-gate-device",
		DeviceHost:   h.sshHost,
		SSHPort:      h.sshPort,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
	}
}

// TestSSHMeshReleaseGate_HostKeyVerifiedFromTheEnvironment proves the
// Walk tier can verify a real host key against a real device without a
// home directory to keep one in, which is what the published runner
// container is missing and what FAILURE_PATTERNS.md #150 recorded.
//
// The claim is narrow and worth stating exactly, because it is the whole
// reason this test is here rather than in pkg/remoteexec. The variable is
// read by a Collection method running inside a per-task CHILD PROCESS
// that native.Adapter spawns, several boundaries away from this test: a
// real NATS dispatch, the real Agent, the real DAG executor, then a fork
// and exec. A unit test on the resolution order proves none of that
// reaches the place it has to reach. This does, against a real sshd whose
// key was captured the way ssh-keyscan captures one, with the runbook
// setting no insecure_skip_host_key_verify anywhere.
//
// Before this, that flag was the only thing that worked on this tier: the
// off switch of a security control was the control's only functioning
// setting.
func TestSSHMeshReleaseGate_HostKeyVerifiedFromTheEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessWith(t, knownHostsInEnvironment)

	final := h.dispatch(t, pingPayload(h))

	if final.Status != "ok" {
		t.Fatalf("final status = %q, want %q: host key verification should have succeeded against %s with no known_hosts under $HOME: message=%q",
			final.Status, "ok", remoteexec.KnownHostsEnv, final.EventData.Message)
	}
}

// TestSSHMeshReleaseGate_NoHostKeySourceFailsWithAnActionableError is the
// negative control for the test above, and is also the reproduction of
// the defect itself.
//
// Same dispatch, same real container, same real credential, with the host
// key put nowhere the process will look. Two things must hold. The run
// must fail, which proves the test above passed because the environment
// variable was genuinely read rather than because verification had
// quietly stopped happening. And the failure must name the variable that
// fixes it, because the operator hitting this in a container cannot do
// anything about its home directory, and an error that offers only
// insecure_skip_host_key_verify is an error that teaches everyone to turn
// verification off.
func TestSSHMeshReleaseGate_NoHostKeySourceFailsWithAnActionableError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessWith(t, knownHostsInEnvironment)
	// Take the one source away, leaving $HOME pointing at the same
	// known_hosts-free directory the harness already set up.
	t.Setenv(remoteexec.KnownHostsEnv, "")

	final := h.dispatch(t, pingPayload(h))

	if final.Status != "failed" {
		t.Fatalf("final status = %q, want %q: with no known_hosts anywhere, the connection must fail closed", final.Status, "failed")
	}
	if !strings.Contains(final.EventData.Message, remoteexec.KnownHostsEnv) {
		t.Errorf("failure message = %q, want it to name %s, which is the fix an operator can actually apply",
			final.EventData.Message, remoteexec.KnownHostsEnv)
	}
}
