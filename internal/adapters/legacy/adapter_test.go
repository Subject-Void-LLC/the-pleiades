package legacy_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// mockBus is a minimal event.Bus fake: Adapter only ever calls Publish,
// never Subscribe, mirroring internal/adapters/native's own identical
// test double exactly.
type mockBus struct {
	published []event.Event
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.published = append(m.published, evt)
	return nil
}
func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}
func (m *mockBus) Close() error { return nil }

func (m *mockBus) jobEvents(t *testing.T) []wire.JobEvent {
	t.Helper()
	out := make([]wire.JobEvent, len(m.published))
	for i, evt := range m.published {
		if err := json.Unmarshal(evt.Data, &out[i]); err != nil {
			t.Fatalf("failed to unmarshal published event %d: %v", i, err)
		}
	}
	return out
}

// failingBus is an event.Bus whose Publish always errors, proving Execute
// propagates a publish failure rather than swallowing it, the same
// contract internal/adapters/native.Adapter already holds itself to.
type failingBus struct{}

func (failingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	return errors.New("deliberate publish failure")
}
func (failingBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}
func (failingBus) Close() error { return nil }

// fakeOrchestrator is a ContainerOrchestrator test double standing in for
// DockerOrchestrator: Adapter's own sequencing, error propagation, and
// secret-masking logic is what these tests exercise, not container
// provisioning itself, which docker_orchestrator_test.go and the real
// Release Gate already exercise against a genuine Docker daemon (RULE 0:
// a fake here does not weaken this package's own coverage, since the
// boundary it stands in for is proven real elsewhere).
type fakeOrchestrator struct {
	lastSpec legacy.ContainerSpec
	result   legacy.ContainerResult
	err      error
}

func (f *fakeOrchestrator) Run(ctx context.Context, spec legacy.ContainerSpec) (legacy.ContainerResult, error) {
	f.lastSpec = spec
	return f.result, f.err
}

// writePlaybooks writes id+".yml" containing content under a fresh temp
// directory and returns a real legacy.PlaybookSource rooted there (RULE
// 0: the real DirPlaybookSource, not a stub).
func writePlaybook(t *testing.T, id, content string) legacy.PlaybookSource {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(id)), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write playbook fixture: %v", err)
	}
	src, err := playbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("playbook.NewDirSource: %v", err)
	}
	return src
}

const realPlaybookOutput = `
PLAY [all] *********************************************************************

TASK [say hello] ***************************************************************
ok: [sw1] => {
    "msg": "hello from sw1"
}

PLAY RECAP *********************************************************************
sw1                        : ok=1    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0
`

func TestAdapter_Execute_PublishesStartedThenParsedEvents(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{result: legacy.ContainerResult{Output: []byte(realPlaybookOutput), ExitCode: 0}}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "pleiades/legacy-ansible-runner:test", nil)

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "upgrade.yml", DeviceID: "d1", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	events := bus.jobEvents(t)
	if len(events) != 3 {
		t.Fatalf("got %d published events, want 3 (started, one task result, one task.completed): %+v", len(events), events)
	}
	if events[0].Status != "started" {
		t.Errorf("events[0].Status = %q, want %q", events[0].Status, "started")
	}
	if events[1].Task != "say hello" || events[1].EventData.Message != "hello from sw1" {
		t.Errorf("events[1] = %+v, want Task=\"say hello\" Message=\"hello from sw1\"", events[1])
	}
	if events[2].Task != "task.completed" || events[2].Status != "ok" {
		t.Errorf("events[2] = %+v, want Task=task.completed Status=ok", events[2])
	}

	// The playbook and inventory must have actually been copied into the
	// spec the orchestrator received, at the exact paths Argv references.
	if orch.lastSpec.Argv[len(orch.lastSpec.Argv)-1] != "/run/pleiades/playbook.yml" {
		t.Errorf("Argv = %v, want it to end with the playbook's in-container path", orch.lastSpec.Argv)
	}
	if len(orch.lastSpec.Files) != 2 {
		t.Errorf("got %d container files, want 2 (inventory + playbook)", len(orch.lastSpec.Files))
	}
}

func TestAdapter_Execute_UnknownPlaybookReturnsError(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "does-not-exist.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	err := adapter.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("expected an error for an unresolvable playbook, got nil")
	}
	if !errors.Is(err, playbook.ErrNotFound) {
		t.Errorf("error = %v, want it to wrap playbook.ErrNotFound", err)
	}
	// The container must never have been run: a "started" event is fine
	// (published before playbook resolution), but no provisioning attempt
	// should have happened for a playbook that was never found.
	if orch.lastSpec.Image != "" {
		t.Errorf("orchestrator was invoked (spec.Image = %q) despite an unresolvable playbook", orch.lastSpec.Image)
	}
}

func TestAdapter_Execute_MasksSecretsInPublishedMessages(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	secretPassword := "hunter2-super-secret"
	output := `
PLAY [all] *********************************************************************

TASK [echo password] ************************************************************
ok: [sw1] => {
    "msg": "the password is ` + secretPassword + `"
}

PLAY RECAP *********************************************************************
sw1                        : ok=1    changed=0    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0
`
	orch := &fakeOrchestrator{result: legacy.ContainerResult{Output: []byte(output), ExitCode: 0}}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{
		JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
		Secrets: credential.Flatten(credential.Credential{Username: "svc", Password: secretPassword}),
	}
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	for _, evt := range bus.jobEvents(t) {
		if strings.Contains(evt.EventData.Message, secretPassword) {
			t.Errorf("published event leaks the raw secret: %+v", evt)
		}
	}
}

func TestAdapter_Execute_NonZeroExitWithNoRecapReportsFailed(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	// A container that crashed before printing anything Ansible-shaped:
	// no PLAY RECAP for ParseStdout to derive a completion event from.
	orch := &fakeOrchestrator{result: legacy.ContainerResult{Output: []byte("panic: something went very wrong\n"), ExitCode: 1}}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	err := adapter.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("expected an error for a non-zero exit with no parseable summary, got nil")
	}

	events := bus.jobEvents(t)
	last := events[len(events)-1]
	if last.Task != "task.completed" || last.Status != "failed" {
		t.Errorf("last published event = %+v, want a synthesized failed task.completed", last)
	}
}

func TestAdapter_Execute_PropagatesOrchestratorFailure(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{err: errors.New("deliberate: docker daemon unreachable")}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err == nil {
		t.Fatal("expected Execute to propagate the orchestrator's own error, got nil")
	}
}

func TestAdapter_Execute_PropagatesPublishFailure(t *testing.T) {
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{}
	adapter := legacy.NewAdapter(failingBus{}, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err == nil {
		t.Fatal("expected Execute to propagate the started-event publish failure, got nil")
	}
}

// TestWithNetworks_AttachesConfiguredNetworksToEveryContainerSpec proves
// the AdapterOption actually reaches ContainerSpec.Networks on a real
// Execute call, the seam this package's own Release Gate
// (cmd/runner/ansible_release_gate_test.go) depends on to put its
// ephemeral Ansible container on the same Docker network as its ephemeral
// sshd target.
func TestWithNetworks_AttachesConfiguredNetworksToEveryContainerSpec(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{result: legacy.ContainerResult{Output: []byte(realPlaybookOutput), ExitCode: 0}}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil, legacy.WithNetworks([]string{"my-net"}))

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if len(orch.lastSpec.Networks) != 1 || orch.lastSpec.Networks[0] != "my-net" {
		t.Errorf("ContainerSpec.Networks = %v, want [\"my-net\"]", orch.lastSpec.Networks)
	}
}

func TestAdapter_Execute_PassphraseProtectedKeyFailsBeforeRunningContainer(t *testing.T) {
	bus := &mockBus{}
	playbooks := writePlaybook(t, "upgrade.yml", "---\n- hosts: all\n")
	orch := &fakeOrchestrator{}
	adapter := legacy.NewAdapter(bus, playbooks, orch, "irrelevant", nil)

	payload := wire.DispatchPayload{
		JobID: "job-1", RunbookID: "upgrade.yml", DeviceName: "sw1", DeviceHost: "10.0.0.1",
		Secrets: credential.Flatten(credential.Credential{
			Username: "svc", PrivateKeyPEM: []byte("fake-key"), Passphrase: "secret-passphrase",
		}),
	}
	err := adapter.Execute(context.Background(), payload)
	if !errors.Is(err, legacy.ErrPassphraseProtectedKey) {
		t.Fatalf("error = %v, want ErrPassphraseProtectedKey", err)
	}
	if orch.lastSpec.Image != "" {
		t.Errorf("orchestrator was invoked despite a passphrase-protected key, which must be rejected before any container runs")
	}
}
