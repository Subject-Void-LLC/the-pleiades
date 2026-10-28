// Package main_test: a launch's extra variables keep their number types
// on the way to a Runner, through the real Agent, the real native adapter
// and real NATS, so a runbook doing arithmetic on one behaves as it does
// on the Crawl tier.
package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// extraVarsTypesRunbook does arithmetic on vars.n in an assert and in a
// when, which CEL allows on an int and refuses ("no such overload") on a
// float64 added to an int.
const extraVarsTypesRunbook = `# Asserts an integer extra variable reads as one; changes nothing.
id: extra-vars-types
tasks:
  - name: n is an integer
    assert:
      that:
        - vars.n + 1 == 4
        - type(vars.n) == int
  - name: arithmetic in a when
    noop:
      changed: true
    when:
      - vars.n * 2 == 6
`

// TestExtraVars_AnIntegerStaysAnIntegerOnTheRunner launches extra_vars
// {"n": 3} as the Controller holds them, decoded from the API's JSON body
// (and again from the job's stored row) by encoding/json, which makes 3 a
// float64, and dispatches them over real NATS to a real Agent and native
// adapter. Before pkg/wire decoded a dispatch's extra_vars by the child
// pipe's number rule, the assert failed on the Runner with CEL's "no such
// overload" while the same runbook passed under pleiades run -e n=3.
func TestExtraVars_AnIntegerStaysAnIntegerOnTheRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("starts NATS")
	}
	ctx := context.Background()
	url := testsupport.StartNATS(t).URL()
	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}
	// A defer, so the client closes before StartNATS's cleanup stops the
	// server (agent_nats_test.go says why).
	defer bus.Close()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extra-vars-types.yaml"), []byte(extraVarsTypesRunbook), 0o644); err != nil { // #nosec G306 -- a test runbook fixture, not secret material
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := native.NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil, runner.WithResultReporting(bus))
	agentCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = agent.Run(agentCtx) }()

	// The extra variables as the Controller has them after its API and its
	// database decoded them: n is a float64.
	var extraVars map[string]any
	if err := json.Unmarshal([]byte(`{"n": 3}`), &extraVars); err != nil {
		t.Fatal(err)
	}
	h := &releaseGateHarness{bus: bus, js: js, adapter: adapter}
	payload := wire.DispatchPayload{
		JobID: uuid.New().String(), RunbookID: "extra-vars-types",
		DeviceID: "vars-device", DeviceName: "vars-device", DeviceHost: "10.0.0.1",
		ExtraVars: extraVars,
	}
	results := h.results(t, payload.JobID)
	evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
	if err != nil {
		t.Fatal(err)
	}
	pubCtx := event.WithIdempotencyKey(ctx, topology.DispatchIdentity(payload.JobID, payload.DeviceID))
	if err := bus.Publish(pubCtx, topology.DispatchSubject(payload.DeviceID), *evt); err != nil {
		t.Fatal(err)
	}
	if r := result(t, results, 60*time.Second, "the extra-vars job"); r.Outcome != "completed" {
		t.Fatalf("the job reported %q, %q; want completed, with vars.n read as an integer", r.Outcome, r.Reason)
	}
}
