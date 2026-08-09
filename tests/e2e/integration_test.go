package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGrandIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up Postgres Container
	pgContainer, err := testpg.Run(ctx,
		"postgres:15-alpine",
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("password"),
		testpg.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %s", err)
	}
	defer pgContainer.Terminate(ctx)

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get pg connection string: %s", err)
	}

	// 2. Init DB and Repository
	client, err := ent.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open ent client: %s", err)
	}
	defer client.Close()

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("failed to create schema: %s", err)
	}

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())

	// Seed an inventory group. Phase 7 made GetGroup's Selector push a
	// real Group edge down to SQL rather than a decorative "group"
	// properties key nothing ever filtered on (entRepository.GetGroup),
	// so the two devices are attached to a real Group named "edge" here,
	// the mechanism a Selector{GroupName: "edge"} dispatch now actually
	// matches against.
	t.Log("Seeding inventory...")
	rtr1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SetProperties(map[string]interface{}{"host": "10.0.0.1"}).SaveX(ctx)
	rtr2 := client.Device.Create().SetName("rtr2").SetType("cisco_router").SetProperties(map[string]interface{}{"host": "10.0.0.2"}).SaveX(ctx)
	client.Group.Create().SetName("edge").AddDevices(rtr1, rtr2).SaveX(ctx)

	// 3. Spin up NATS Container with JetStream
	req := testcontainers.ContainerRequest{
		Image:        "nats:latest",
		ExposedPorts: []string{"4222/tcp"},
		Cmd:          []string{"-js"},
		WaitingFor:   wait.ForLog("Server is ready"),
	}
	natsContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start nats container: %s", err)
	}
	defer natsContainer.Terminate(ctx)

	natsEndpoint, err := natsContainer.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("failed to get nats endpoint: %s", err)
	}

	nc, err := nats.Connect("nats://" + natsEndpoint)
	if err != nil {
		t.Fatalf("failed to connect to nats: %s", err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("failed to get jetstream: %s", err)
	}

	// 4. Setup the single Pleiades stream and the shared dispatch consumer,
	// both topology-owned (internal/topology) rather than hand-declared
	// here: this used to create two independent streams ("RUNBOOKS",
	// "JOBS") with the pre-Phase-2 subject literals
	// ("runbooks.dispatch", "jobs.logs.>"), which is exactly the drift
	// topology.EnsureStream/DispatchConsumerConfig now prevent.
	bus, err := event.NewNatsBus(ctx, "nats://"+natsEndpoint)
	if err != nil {
		t.Fatalf("failed to init event bus: %s", err)
	}
	if _, err := topology.EnsureStream(ctx, js); err != nil {
		t.Fatalf("failed to ensure stream: %s", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("failed to create consumer: %s", err)
	}

	// 5. Start Runner Agent
	adapter := native.NewAdapter(bus)
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault, nil, nil)
	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	// 6. Build a real runbook.Source and durable JobStore, and subscribe
	// internal/dispatch.Worker to job.requested, mirroring
	// cmd/controller/main.go's own composition root (RULE 0): Phase 14
	// moved per-device fan-out out of the HTTP request path and into this
	// background worker, so DispatchRunbook itself now only persists a Job
	// and publishes one job.requested event; the Worker subscribed here is
	// what actually streams the "edge" group, admits or skips each
	// device, and publishes the per-device wire.DispatchPayload the
	// Runner Agent above consumes.
	runbookDir := t.TempDir()
	// "ios_backup" requires capability.NameCiscoIOS
	// (engine.ActionCapability); devices/cisco.Router grants that
	// capability to every "cisco_router"-typed device automatically
	// (router.go's own NewRouter), so both devices seeded above satisfy
	// it without any extra fixture wiring.
	runbookYAML := "id: ping\ntasks:\n  - name: backup\n    fqcn: ios_backup\n"
	if err := os.WriteFile(filepath.Join(runbookDir, "ping.yaml"), []byte(runbookYAML), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture: %s", err)
	}
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("failed to init runbook source: %s", err)
	}

	jobStore := dispatch.NewEntJobStore(client)
	worker := dispatch.NewWorker(jobStore, repo, runbooks, bus)
	if err := bus.Subscribe(ctx, topology.JobRequestedSubject(), worker.HandleJobRequested); err != nil {
		t.Fatalf("failed to subscribe job fan-out worker: %s", err)
	}

	// 7. Start API Dispatcher
	dispatcher := api.NewDispatcher(runbooks, jobStore, bus)

	// 8. Make API Request, authenticated with a real signed token through
	// the real AuthMiddleware rather than a hand-injected identity: this
	// package cannot reach api.IdentityKeyForTest (an export_test.go
	// symbol, visible only inside package api's own test binary), which
	// is exactly the cross-package gap HANDOFF_DOCUMENT.md's Phase 11
	// session named authtest as the fix for. Going through the real
	// middleware here is also strictly more representative of what a
	// caller actually experiences (AGENTS.md RULE 0) than constructing an
	// *auth.Identity by hand ever was.
	issuer := authtest.New(t, "pleiades-e2e-issuer", "pleiades-e2e-audience")
	handler := api.AuthMiddleware(issuer.Evaluator())(http.HandlerFunc(dispatcher.DispatchRunbook))

	httpReq := httptest.NewRequest("POST", "/api/v1/jobs/dispatch?group=edge&runbook=ping", nil)
	httpReq.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{
		Subject: "admin1",
		Role:    auth.RoleAdmin,
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httpReq)

	// A launch is now 202 Accepted, not 200: DispatchRunbook only persists
	// a Job and publishes job.requested here, it does not wait for
	// fan-out (dispatcher.go's own jobAcceptedResponse doc comment).
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Status string `json:"status"`
		JobID  string `json:"job_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse dispatch response: %s", err)
	}
	if resp.Status != "accepted" {
		t.Fatalf("expected response status %q, got %q", "accepted", resp.Status)
	}

	t.Logf("Launched JobID: %s", resp.JobID)

	// 9. Fan-out happens asynchronously in internal/dispatch.Worker now,
	// off this request entirely, so this test polls the JobStore directly
	// until the Worker's own job.requested handler reaches a terminal
	// state. This is a test-side wait mechanism, not the production
	// mechanism under test (a caller would instead poll GET /jobs/{id});
	// polling is bounded so a real regression fails this test outright
	// instead of hanging it.
	var job *dispatch.Job
	deadline := time.Now().Add(10 * time.Second)
	for {
		job, _, err = jobStore.Get(ctx, resp.JobID)
		if err != nil {
			t.Fatalf("failed to poll job %s: %s", resp.JobID, err)
		}
		if job.State == "completed" || job.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach a terminal state within the deadline, last state %q", resp.JobID, job.State)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if job.State != "completed" {
		t.Fatalf("expected job state %q, got %q", "completed", job.State)
	}
	if job.DispatchedCount != 2 {
		t.Fatalf("expected 2 dispatched, got %d", job.DispatchedCount)
	}

	// 10. Verify the Runner picked them up and streamed logs
	time.Sleep(2 * time.Second) // Give the agent time to execute

	logConsumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.LogViewerConsumerConfig(resp.JobID))
	if err != nil {
		t.Fatalf("failed to create log consumer: %s", err)
	}

	msgs, err := logConsumer.Fetch(6, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("failed to fetch logs: %s", err)
	}

	count := 0
	for _ = range msgs.Messages() {
		count++
	}

	if count != 6 {
		t.Fatalf("expected 6 log events, got %d", count)
	}

	t.Log("Grand Integration Test Passed!")
}
