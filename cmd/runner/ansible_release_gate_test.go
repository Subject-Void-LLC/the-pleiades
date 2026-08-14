package main_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// This file is Phase 17 (Legacy Ansible Adapter)'s own Release Gate:
// "The Runner successfully parses STDOUT from an Ansible run into a
// structured JSON event payload." It mirrors
// ssh_mesh_release_gate_test.go's own real-infrastructure shape (real
// NATS container, real dispatch through the real mesh, a genuine
// wrong-credential negative control) rather than inventing a different
// one, applied to legacy.Adapter instead of native.Adapter.
//
// One real difference from that file: here BOTH ends of the SSH
// connection this test proves are ephemeral containers on the same
// Docker host (the sshd target and the Ansible runner container
// legacy.Adapter itself provisions), not "the Go test process dials the
// target directly." The two need a shared Docker network with a stable
// container-network alias to reach each other portably; relying on a
// Docker-Desktop convenience like host.docker.internal would pass in
// this repository's own sandboxed development environment and silently
// fail on a native-Linux CI runner, which is exactly the class of
// environment-specific assumption this repository's own
// FAILURE_PATTERNS/LESSONS_LEARNED culture warns against.

const (
	ansibleGateSSHUser      = "testuser"
	ansibleGateSSHPassword  = "release-gate-p17-password"
	ansibleGateNetworkAlias = "sshd-target"
)

// startSSHDOnNetwork starts a real openssh-server container attached to
// networkName under the stable alias ansibleGateNetworkAlias, so a
// second container on the same network (the Ansible runner container
// legacy.Adapter provisions) can reach it by name rather than by a
// host-mapped port only the test process itself can see.
func startSSHDOnNetwork(t *testing.T, networkName string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:          testsupport.SSHDImage,
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {ansibleGateNetworkAlias}},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"PASSWORD_ACCESS": "true",
			"USER_NAME":       ansibleGateSSHUser,
			"USER_PASSWORD":   ansibleGateSSHPassword,
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
}

// observingOrchestrator wraps a real legacy.ContainerOrchestrator,
// recording the last ContainerSpec it was given, so a Release Gate test
// can assert on the real argv a real container really ran with
// (AWX_PARITY_ROADMAP.md Section 3b.1's own gate text: "asserted from the
// sandboxed process's own argv"), without any production code exposing
// that internal detail for a test's sake. legacy.DockerOrchestrator.Run
// passes spec.Argv straight into testcontainers.ContainerRequest.Cmd with
// no transformation (docker_orchestrator.go), so the spec recorded here
// is genuinely the argv the real Docker daemon received for the real
// container this file's own SSH/remote-command assertions already prove
// really ran; Run itself still delegates to the real orchestrator, so
// wrapping it changes nothing about what actually executes.
type observingOrchestrator struct {
	real legacy.ContainerOrchestrator
	mu   sync.Mutex
	last legacy.ContainerSpec
}

func (o *observingOrchestrator) Run(ctx context.Context, spec legacy.ContainerSpec) (legacy.ContainerResult, error) {
	o.mu.Lock()
	o.last = spec
	o.mu.Unlock()
	return o.real.Run(ctx, spec)
}

func (o *observingOrchestrator) lastSpec() legacy.ContainerSpec {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last
}

// ansibleReleaseGateHarness bundles the real NATS container plus the real
// Runner-mesh objects (legacy.Adapter, runner.Agent) every test in this
// file needs.
type ansibleReleaseGateHarness struct {
	bus  event.Bus
	js   jetstream.JetStream
	orch *observingOrchestrator
}

func newAnsibleReleaseGateHarness(t *testing.T, playbookYAML string) *ansibleReleaseGateHarness {
	t.Helper()
	return newAnsibleReleaseGateHarnessWithLogger(t, playbookYAML, nil)
}

// newAnsibleReleaseGateHarnessWithLogger is the same harness with the
// adapter's logger supplied by the caller.
//
// It exists for the injection gate's leak run, which asserts against every
// byte the adapter's own logger wrote. A nil logger keeps
// legacy.NewAdapter's own slog.Default() fallback, which is what every
// other test in this package wants.
func newAnsibleReleaseGateHarnessWithLogger(t *testing.T, playbookYAML string, logger *slog.Logger) *ansibleReleaseGateHarness {
	t.Helper()
	ctx := context.Background()

	image := testsupport.BuildAnsibleRunnerImage(t)

	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create docker network: %v", err)
	}
	t.Cleanup(func() { _ = net.Remove(context.Background()) })

	startSSHDOnNetwork(t, net.Name)

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

	playbookDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(playbookDir, "upgrade.yml"), []byte(playbookYAML), 0o644); err != nil {
		t.Fatalf("failed to write playbook fixture: %v", err)
	}
	playbooks, err := playbook.NewDirSource(playbookDir)
	if err != nil {
		t.Fatalf("failed to init playbook source: %v", err)
	}

	orch := &observingOrchestrator{real: legacy.NewDockerOrchestrator()}
	adapter := legacy.NewAdapter(bus, playbooks, orch, image, logger, legacy.WithNetworks([]string{net.Name}))
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil)

	agentCtx, cancelAgent := context.WithCancel(ctx)
	t.Cleanup(cancelAgent)
	go func() { _ = agent.Run(agentCtx) }()

	return &ansibleReleaseGateHarness{bus: bus, js: js, orch: orch}
}

// dispatch publishes payload to the real dispatch subject and waits for a
// task.completed wire.JobEvent on the real per-job log subject, returning
// every event observed along the way so a caller can assert on
// intermediate per-task events too (not just the terminal one).
func (h *ansibleReleaseGateHarness) dispatch(t *testing.T, payload wire.DispatchPayload) []wire.JobEvent {
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

	var seen []wire.JobEvent
	deadline := time.Now().Add(90 * time.Second)
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
			seen = append(seen, jobEvt)
			if jobEvt.Task == "task.completed" {
				return seen
			}
		}
	}
	t.Fatal("timed out waiting for a task.completed job event")
	return seen
}

// releaseGatePlaybook is the real, unconverted Ansible playbook this
// Release Gate runs. "report inventory plumbing" proves
// BuildInventoryJSON's own group/hostvar generation reached the real
// playbook (Phase 17's own "Generate inventory.json ... preserving
// capabilities and group paths as hostvars" checklist item); "run a real
// remote command" proves a real command genuinely executed on the real
// target over a real SSH connection. It uses the "raw" module rather
// than "command": the sshd target is a bare container with no Python
// interpreter, the same shape a real bare network device has, and "raw"
// is what a real legacy playbook targeting such a device already uses
// instead of a Python-dependent module.
const releaseGatePlaybook = `---
- hosts: all
  gather_facts: false
  tasks:
    - name: report inventory plumbing
      debug:
        msg: "groups={{ group_names }} caps={{ pleiades_capabilities }}"
    - name: run a real remote command
      raw: echo hello-from-legacy-adapter
      changed_when: false
`

// TestAnsibleReleaseGate_RealPlaybookThroughTheFullChain is Phase 17's own
// Release Gate. A real wire.DispatchPayload, carrying a real password
// (never a fake or hardcoded one) and real Tags/Capabilities, is
// published to the real dispatch subject; the real runner.Agent pulls
// it, legacy.Adapter builds a real inventory.json, spins up a real
// ephemeral container running the real, repository-committed Ansible
// runner image, which runs the real playbook above against a real,
// independently-implemented sshd container over a real SSH connection,
// and the real captured stdout is really parsed back into structured
// wire.JobEvent values published over the real bus.
func TestAnsibleReleaseGate_RealPlaybookThroughTheFullChain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newAnsibleReleaseGateHarness(t, releaseGatePlaybook)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:        jobID,
		RunbookID:    "upgrade.yml",
		DeviceID:     "release-gate-device",
		DeviceName:   "sw1",
		DeviceHost:   ansibleGateNetworkAlias,
		SSHPort:      2222,
		Tags:         []string{"catalyst_lab"},
		Capabilities: []capability.Name{capability.NameCiscoIOS},
		Secrets:      credential.Flatten(credential.Credential{Username: ansibleGateSSHUser, Password: ansibleGateSSHPassword}),
	}

	events := h.dispatch(t, payload)
	final := events[len(events)-1]

	if final.Status != "ok" {
		t.Fatalf("final status = %q, want %q: events=%+v", final.Status, "ok", events)
	}
	if strings.Contains(final.EventData.Message, ansibleGateSSHPassword) {
		t.Errorf("final message leaks the raw password: %q", final.EventData.Message)
	}

	// Proves inventory.json's own group/hostvar generation (tags ->
	// group_names, capabilities -> pleiades_capabilities) genuinely
	// reached the playbook, not just that a Go struct was built
	// correctly in isolation: the debug task's own real remote-rendered
	// message must contain both.
	var sawGroupPlumbing, sawRemoteCommand bool
	for _, evt := range events {
		if evt.Task == "report inventory plumbing" {
			sawGroupPlumbing = strings.Contains(evt.EventData.Message, "catalyst_lab") &&
				strings.Contains(evt.EventData.Message, "CiscoIOSCapable")
		}
		if evt.Task == "run a real remote command" {
			sawRemoteCommand = true
		}
	}
	if !sawGroupPlumbing {
		t.Errorf("no event proved inventory.json's group/hostvar plumbing reached the playbook: events=%+v", events)
	}
	if !sawRemoteCommand {
		t.Errorf("no event for the real remote command task: events=%+v", events)
	}
}

// TestAnsibleReleaseGate_WrongSecretFails is the negative control for the
// test above, the identical shape ssh_mesh_release_gate_test.go's own
// TestSSHMeshReleaseGate_WrongSecretFails already establishes for the
// native path: the same dispatch, against the same real target, but with
// a deliberately wrong password. If the container's own SSH connection
// were not genuinely using the credential carried on the wire, this
// would still report success. It does not: authentication genuinely
// fails inside the real ephemeral container, against the real sshd
// target, and that failure genuinely propagates back through
// legacy.Adapter's own stdout parsing and onto the real bus.
func TestAnsibleReleaseGate_WrongSecretFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	// This playbook only ever exercises the module that actually dials
	// SSH: unlike releaseGatePlaybook, it carries no debug task
	// referencing pleiades_capabilities/pleiades_tags, so a connection
	// failure is unambiguously what fails this run, not an unrelated
	// undefined-variable error from a task that never got the chance to
	// try connecting.
	h := newAnsibleReleaseGateHarness(t, `---
- hosts: all
  gather_facts: false
  tasks:
    - name: run a real remote command
      raw: echo hello-from-legacy-adapter
      changed_when: false
`)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:      jobID,
		RunbookID:  "upgrade.yml",
		DeviceID:   "release-gate-device",
		DeviceName: "sw1",
		DeviceHost: ansibleGateNetworkAlias,
		SSHPort:    2222,
		Secrets:    credential.Flatten(credential.Credential{Username: ansibleGateSSHUser, Password: "definitely-the-wrong-password"}),
	}

	events := h.dispatch(t, payload)
	final := events[len(events)-1]

	if final.Status != "failed" {
		t.Fatalf("final status = %q, want %q: a wrong password must not authenticate: events=%+v", final.Status, "failed", events)
	}
}

// taggedReleaseGatePlaybook carries two independently tagged tasks, so
// TestAnsibleReleaseGate_LaunchFieldsReachRealInvocation can prove
// --tags/--skip-tags genuinely change which tasks a real ansible-playbook
// run executes, not just that the flag appears on the command line.
const taggedReleaseGatePlaybook = `---
- hosts: all
  gather_facts: false
  tasks:
    - name: deploy task
      tags: [deploy]
      raw: echo deploy-ran
      changed_when: false
    - name: other task
      tags: [other]
      raw: echo other-ran
      changed_when: false
`

// TestAnsibleReleaseGate_LaunchFieldsReachRealInvocation is
// AWX_PARITY_ROADMAP.md Section 3b.1's own stated gate, run for real: "a
// template launched with forks: 3 and job_tags: [deploy] open, overridden
// at launch to forks: 1, produces a real ansible-playbook invocation ...
// that actually used forks 1 and tag deploy, asserted from the sandboxed
// process's own argv[.] ... never from the job record alone."
//
// Two kinds of evidence, both real, both from this one dispatch:
//  1. observingOrchestrator's recorded ContainerSpec.Argv, the exact
//     argument vector the real Docker daemon started the real container
//     with, proves --forks 1, --tags deploy, --limit, and -v all reached
//     the real invocation at the position buildArgv documents.
//  2. The parsed job.log event stream proves --tags genuinely filtered
//     which tasks ran: "deploy task" carries the "deploy" tag and its own
//     event appears; "other task" carries "other" and is absent
//     entirely, not merely marked skipped, because ansible-playbook
//     excludes a non-matching-tag task from the play before it ever
//     starts.
func TestAnsibleReleaseGate_LaunchFieldsReachRealInvocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newAnsibleReleaseGateHarness(t, taggedReleaseGatePlaybook)

	jobID := uuid.New().String()
	payload := wire.DispatchPayload{
		JobID:      jobID,
		RunbookID:  "upgrade.yml",
		DeviceID:   "release-gate-device",
		DeviceName: "sw1",
		DeviceHost: ansibleGateNetworkAlias,
		SSHPort:    2222,
		Secrets:    credential.Flatten(credential.Credential{Username: ansibleGateSSHUser, Password: ansibleGateSSHPassword}),
		Fields: map[string]any{
			"forks":     1,
			"job_tags":  []any{"deploy"},
			"limit":     "sw1",
			"verbosity": 1,
		},
	}

	events := h.dispatch(t, payload)
	final := events[len(events)-1]
	if final.Status != "ok" {
		t.Fatalf("final status = %q, want %q: events=%+v", final.Status, "ok", events)
	}

	wantArgv := []string{
		"ansible-playbook", "-v", "-i", "/run/pleiades/inventory.json",
		"--limit", "sw1", "--forks", "1", "--tags", "deploy",
		"/run/pleiades/playbook.yml",
	}
	if gotArgv := h.orch.lastSpec().Argv; !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Errorf("real ContainerSpec.Argv (what the real Docker daemon started the real container with) = %#v, want %#v", gotArgv, wantArgv)
	}

	var sawDeployTask, sawOtherTask bool
	for _, evt := range events {
		if evt.Task == "deploy task" {
			sawDeployTask = true
		}
		if evt.Task == "other task" {
			sawOtherTask = true
		}
	}
	if !sawDeployTask {
		t.Errorf("no event for \"deploy task\": job_tags: [deploy] should have let it run: events=%+v", events)
	}
	if sawOtherTask {
		t.Errorf("saw an event for \"other task\": job_tags: [deploy] should have excluded it from the play entirely: events=%+v", events)
	}
}
