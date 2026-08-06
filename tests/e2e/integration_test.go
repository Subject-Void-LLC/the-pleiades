package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/adapters/native"
	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type mockEvaluator struct{}

func (m *mockEvaluator) ValidateToken(ctx context.Context, rawToken string) (*auth.Identity, error) {
	return &auth.Identity{Subject: "admin1", Role: auth.RoleAdmin}, nil
}

func (m *mockEvaluator) CheckAccess(ctx context.Context, id *auth.Identity, requiredScopes ...string) error {
	return nil
}

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
	rtr1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SetProperties(map[string]interface{}{"ip": "10.0.0.1"}).SaveX(ctx)
	rtr2 := client.Device.Create().SetName("rtr2").SetType("cisco_router").SetProperties(map[string]interface{}{"ip": "10.0.0.2"}).SaveX(ctx)
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
	agent := runner.NewAgent(consumer, adapter, js, topology.MaxDeliverDefault, nil)
	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	// 6. Start API Dispatcher
	eval := &mockEvaluator{}
	dispatcher := api.NewDispatcher(repo, eval, bus)

	// 7. Make API Request
	httpReq := httptest.NewRequest("POST", "/api/v1/jobs/dispatch?group=edge&runbook=ping", nil)
	id := &auth.Identity{
		Subject: "admin1",
		Role:    auth.RoleAdmin,
	}
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), api.IdentityKeyForTest, id))

	rr := httptest.NewRecorder()
	dispatcher.DispatchRunbook(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		JobID      string `json:"job_id"`
		Dispatched int    `json:"dispatched"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse dispatch response: %s", err)
	}

	if resp.Dispatched != 2 {
		t.Fatalf("expected 2 dispatched, got %d", resp.Dispatched)
	}

	t.Logf("Dispatched JobID: %s", resp.JobID)

	// 8. Verify the Runner picked them up and streamed logs
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
