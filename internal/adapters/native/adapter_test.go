package native

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// mockBus is a minimal event.Bus fake: Adapter only ever calls Publish,
// never Subscribe.
type mockBus struct {
	published []struct {
		topic string
		evt   event.Event
	}
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.published = append(m.published, struct {
		topic string
		evt   event.Event
	}{topic, evt})
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *mockBus) Close() error {
	return nil
}

// lastJobEvent decodes the most recently published wire.JobEvent.
func (m *mockBus) lastJobEvent(t *testing.T) wire.JobEvent {
	t.Helper()
	if len(m.published) == 0 {
		t.Fatal("no events published")
	}
	var evt wire.JobEvent
	if err := json.Unmarshal(m.published[len(m.published)-1].evt.Data, &evt); err != nil {
		t.Fatalf("failed to unmarshal job event: %v", err)
	}
	return evt
}

// failingBus is an event.Bus whose Publish always errors, so
// TestAdapter_Execute_PropagatesPublishFailure can prove Execute no longer
// swallows a publish failure the way the fake adapter this package
// replaced used to.
type failingBus struct{}

func (failingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	return errors.New("deliberate publish failure")
}

func (failingBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (failingBus) Close() error { return nil }

// writeRunbook writes a minimal, real, engine.Builder-compilable runbook
// YAML file (RULE 0: this exercises the same YAML surface
// internal/runbook.DirSource actually compiles in production, not a stub
// DAG built by hand) and returns a runbook.Source rooted at its directory.
func writeRunbook(t *testing.T, id, content string) runbook.Source {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, id+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture: %v", err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}
	return src
}

func TestAdapter_Execute_NoopReportsChanged(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n    params:\n      changed: true\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}

	if len(bus.published) != 2 {
		t.Fatalf("published %d events, want 2 (started, task.completed)", len(bus.published))
	}
	wantSubject := topology.LogSubject("job-1")
	for _, p := range bus.published {
		if p.topic != wantSubject {
			t.Errorf("published topic = %q, want %q", p.topic, wantSubject)
		}
	}

	final := bus.lastJobEvent(t)
	if final.Status != "changed" {
		t.Errorf("final status = %q, want %q", final.Status, "changed")
	}
	if final.Host != "10.0.0.1" {
		t.Errorf("final host = %q, want %q", final.Host, "10.0.0.1")
	}
}

func TestAdapter_Execute_NoopWithNoChangedParamReportsOK(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}

	final := bus.lastJobEvent(t)
	if final.Status != "ok" {
		t.Errorf("final status = %q, want %q (Convergence: report changed only when something changed)", final.Status, "ok")
	}
}

// TestAdapter_Execute_ExtraVarsReachWhenCEL proves payload.ExtraVars
// reaches a real runbook's when_cel condition through the whole Execute
// method (engine.WithVariables' own doc comment on where "vars" binds),
// not just through engine.Executor's own tests in isolation. This is
// AWX_PARITY_ROADMAP.md Section 3b.1's own "ExtraVars folded into the
// runbook's variable context": a conditional task only runs and reports
// changed when the dispatched ExtraVars satisfy its condition, observed
// through Execute's real final job.log event, the same real per-dispatch
// bus every other test in this file already asserts against.
//
// The condition uses CEL's has() guard (has(vars.env) && vars.env ==
// 'prod'), not a bare vars.env == 'prod': cel-go's map field selection
// errors on a genuinely absent key ("no such key") rather than reading as
// false, the identical behavior this engine's own "stat"/"nodes" roots
// already have for an unregistered key, so a real runbook referencing an
// optional extra var needs this same guard regardless of which root it
// reads.
func TestAdapter_Execute_ExtraVarsReachWhenCEL(t *testing.T) {
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: prod-only\n    fqcn: noop\n    when_cel: \"has(vars.env) && vars.env == 'prod'\"\n    params:\n      changed: true\n")

	run := func(extraVars map[string]any) wire.JobEvent {
		bus := &mockBus{}
		adapter, err := NewAdapter(bus, runbooks, nil)
		if err != nil {
			t.Fatalf("NewAdapter: %v", err)
		}
		payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1", ExtraVars: extraVars}
		if err := adapter.Execute(context.Background(), payload); err != nil {
			t.Fatalf("Execute() returned unexpected error: %v", err)
		}
		return bus.lastJobEvent(t)
	}

	prod := run(map[string]any{"env": "prod"})
	if prod.Status != "changed" {
		t.Errorf("ExtraVars{env: prod} final status = %q, want %q (the when_cel-gated task should have run)", prod.Status, "changed")
	}

	dev := run(map[string]any{"env": "dev"})
	if dev.Status != "ok" {
		t.Errorf("ExtraVars{env: dev} final status = %q, want %q (the when_cel-gated task should have been skipped)", dev.Status, "ok")
	}

	none := run(nil)
	if none.Status != "ok" {
		t.Errorf("no ExtraVars at all: final status = %q, want %q (has(vars.env) should read false, not error the run)", none.Status, "ok")
	}
}

func TestAdapter_Execute_UnregisteredFQCNFails(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: pkg.apt.install\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	err = adapter.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("Execute() = nil error, want an error (pkg.apt.install is StatusDeclared, not implemented)")
	}

	final := bus.lastJobEvent(t)
	if final.Status != "failed" {
		t.Errorf("final status = %q, want %q", final.Status, "failed")
	}
}

func TestAdapter_Execute_UnknownRunbookReturnsError(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "does-not-exist", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	err = adapter.Execute(context.Background(), payload)
	if !errors.Is(err, runbook.ErrNotFound) {
		t.Fatalf("Execute() error = %v, want errors.Is(err, runbook.ErrNotFound)", err)
	}

	// The "started" event still went out before runbook resolution failed:
	// Execute publishes it first, deliberately, so a caller sees a job
	// genuinely began even when it fails immediately afterward.
	if len(bus.published) != 1 {
		t.Fatalf("published %d events, want 1 (started only)", len(bus.published))
	}
}

func TestAdapter_Execute_PropagatesPublishFailure(t *testing.T) {
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n")
	adapter, err := NewAdapter(failingBus{}, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	if err := adapter.Execute(context.Background(), payload); err == nil {
		t.Fatal("Execute() = nil error, want a propagated publish error (\"stop discarding publish errors\")")
	}
}

func TestAdapter_Execute_AlreadyCanceledContextReturnsPromptly(t *testing.T) {
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	err = adapter.Execute(ctx, payload)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// TestSummarize_MasksSecretsInFailureMessages proves summarize (the
// function that builds Execute's final job.log message) masks every
// secret it's given out of a failing node's error text, since a task's
// own error can echo back device output that happens to contain a secret
// (internal/engine/action_ssh.go's transportActionExecutor already
// applies this identical masking for the Crawl-tier CLI, for the identical
// reason).
func TestSummarize_MasksSecretsInFailureMessages(t *testing.T) {
	result := engine.RunResult{
		Nodes: []engine.NodeResult{
			{NodeID: "tasks[0]", Err: errors.New("auth failed: password hunter2 rejected by device")},
		},
	}

	status, message := summarize(result, false, []string{"hunter2"})
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if strings.Contains(message, "hunter2") {
		t.Errorf("message %q leaks the raw secret %q", message, "hunter2")
	}
}
