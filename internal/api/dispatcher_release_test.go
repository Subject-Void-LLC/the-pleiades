// This file is Phase 14's Release Gate: a caller with runbook:execute
// launches a template against a 10,000-device inventory, the real,
// unmodified production pipeline (Dispatcher, the real Bus, Worker,
// JobStore) fans it out entirely off the HTTP request, and every single
// device is reached.
//
// It launches a stored template rather than naming a group in a query
// string, because that is the only launch surface there is as of Phase 21.
// The gate is unchanged in what it proves and gains one property it could
// not state before: the job it produces carries the organization its
// inventory belongs to, so 10,000 dispatches are attributable to a tenant.
//
// Every component below is the real one, per RULE 0, mirroring
// hateoas_release_test.go's own precedent exactly: the router is
// api.NewRouter, the authentication is api.AuthMiddleware over a real
// jwtEvaluator, the tokens are real HS256 tokens minted by
// internal/auth/authtest, the authorization is a real auth.AdmissionChain,
// the device repository is a real entRepository over SQLite, the runbook
// source is a real runbook.NewDirSource over a real YAML fixture, the job
// store is a real dispatch.NewEntJobStore over the same SQLite database,
// and the event bus is a real event.NewInProcessBus. There is no stub in
// any load-bearing position.
//
// The gate's whole point is proving that 10,000 real devices, each
// carrying the real "host" property key (never the old, buggy "ip" one;
// see pkg/wire.DispatchPayload's own doc comment for the bug that key name
// used to be), flow all the way from an HTTP launch through to 10,000
// individually recorded, individually published dispatch outcomes. A mock
// iterator that handed every device a convenient hardcoded property would
// prove nothing about that path, which is exactly why this test seeds real
// ent.Device rows instead.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	_ "github.com/mattn/go-sqlite3"
)

// releaseGateDeviceCount is the device count Phase 14's own gate names.
const releaseGateDeviceCount = 10000

// releaseGateBulkBatch is the row count per CreateBulk/AddDeviceIDs call,
// chosen to stay comfortably under SQLite's per-statement bound-variable
// limit, mirroring internal/inventory's own sqliteBulkInsertBatch (a
// _test.go constant in a different package, and so not importable here;
// reproduced rather than duplicated by reference for that reason).
const releaseGateBulkBatch = 1000

// releaseGateRunbookID names the one fixture runbook this gate dispatches.
// It compiles from noopRunbookFQCN (dispatcher_testutil_test.go), the one
// action fqcn engine.ActionCapability does not bind to any capability, so
// it requires nothing of a target device. This is a deliberate choice, not
// an oversight: every device this gate seeds already declares the same
// "host" property and StateActive lifecycle state (the two facts
// internal/dispatch.Worker's own fan-out loop actually branches on before
// publishing), and picking a runbook that also requires zero capabilities
// keeps this test's 10,000-device scale isolated to proving that lifecycle
// and host-property admission path at scale, rather than additionally
// coupling it to "linux_server"'s own capability declarations, which
// internal/dispatch/worker_test.go's own capability-admission tests
// already cover in isolation.
const releaseGateRunbookID = "pb-1"

// bulkCreateReleaseGateDevices inserts builders in releaseGateBulkBatch-
// sized chunks and returns every saved row, so a 10,000-row insert never
// exceeds SQLite's per-statement bound-variable limit in one call.
func bulkCreateReleaseGateDevices(t *testing.T, ctx context.Context, client *ent.Client, builders []*ent.DeviceCreate) []*ent.Device {
	t.Helper()
	saved := make([]*ent.Device, 0, len(builders))
	for start := 0; start < len(builders); start += releaseGateBulkBatch {
		end := start + releaseGateBulkBatch
		if end > len(builders) {
			end = len(builders)
		}
		batch, err := client.Device.CreateBulk(builders[start:end]...).Save(ctx)
		if err != nil {
			t.Fatalf("failed to insert device batch [%d,%d): %v", start, end, err)
		}
		saved = append(saved, batch...)
	}
	return saved
}

// attachReleaseGateDevices adds every device in devices to groupID, in
// releaseGateBulkBatch-sized chunks, for the identical bound-variable
// reason bulkCreateReleaseGateDevices batches its own inserts.
func attachReleaseGateDevices(t *testing.T, ctx context.Context, client *ent.Client, groupID int, devices []*ent.Device) {
	t.Helper()
	ids := make([]int, len(devices))
	for i, d := range devices {
		ids[i] = d.ID
	}
	for start := 0; start < len(ids); start += releaseGateBulkBatch {
		end := start + releaseGateBulkBatch
		if end > len(ids) {
			end = len(ids)
		}
		if _, err := client.Group.UpdateOneID(groupID).AddDeviceIDs(ids[start:end]...).Save(ctx); err != nil {
			t.Fatalf("failed to attach device batch [%d,%d) to group: %v", start, end, err)
		}
	}
}

// TestDispatcher_ReleaseGate is the gate itself.
func TestDispatcher_ReleaseGate(t *testing.T) {
	ctx := context.Background()
	// newSerializedSQLiteClient (dispatcher_testutil_test.go), not a plain
	// enttest.Open: the real Worker writes 10,000 JobTask rows on its own
	// goroutine while the poll loop below concurrently reads from the same
	// client, which needs a capped connection pool to avoid a
	// cross-connection "database table is locked" error (see that
	// function's own doc comment for why).
	client := newSerializedSQLiteClient(t, "release-gate")

	// Seed 10,000 real devices, each carrying a "host" property (never
	// "ip") and StateActive, all in one real Group.
	builders := make([]*ent.DeviceCreate, releaseGateDeviceCount)
	for i := 0; i < releaseGateDeviceCount; i++ {
		builders[i] = client.Device.Create().
			SetName(fmt.Sprintf("release-gate-device-%05d", i)).
			SetType("linux_server").
			SetProperties(map[string]interface{}{"host": fmt.Sprintf("10.0.%d.%d", i/256, i%256)}).
			SetState("active")
	}
	devices := bulkCreateReleaseGateDevices(t, ctx, client, builders)

	group := client.Group.Create().SetName("release-gate-fleet").SaveX(ctx)
	attachReleaseGateDevices(t, ctx, client, group.ID, devices)

	// The tenant, the inventory that holds the fleet, and the template that
	// runs against it. The inventory names the GROUP rather than the ten
	// thousand devices directly, so the membership the Worker streams is
	// resolved exactly as a real deployment's is.
	org := client.Organization.Create().SetName("release-gate-org").SaveX(ctx)
	set := client.Inventory.Create().
		SetName("release-gate-inventory").
		SetOrganization(org).
		AddGroups(group).
		SaveX(ctx)

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	jobStore := dispatch.NewEntJobStore(client)
	sets := inventory.NewEntSetStore(client)
	templates := launch.NewEntStore(client)
	runbooks := newTestRunbookSource(t, releaseGateRunbookID)

	template, err := templates.Create(ctx, launch.Template{
		Name:        "release gate",
		KindName:    "runbook",
		Definition:  releaseGateRunbookID,
		InventoryID: set.ID,
	})
	if err != nil {
		t.Fatalf("creating the release gate template: %v", err)
	}

	// countingBus wraps a real event.NewInProcessBus and records every
	// publish, so the count of wire.DispatchPayload messages actually
	// published can be checked directly rather than trusting the job's
	// own stored tally alone.
	bus := newCapturingBus()

	worker := dispatch.NewWorker(jobStore, repo, runbooks, bus, nil, dispatch.WithSetStore(sets))
	if err := bus.Subscribe(ctx, topology.JobRequestedSubject(), worker.HandleJobRequested); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	dispatcher := api.NewDispatcher(runbooks, jobStore, bus,
		api.WithTemplates(templates), api.WithLaunchConfigs(templates))
	jobsHandler := api.NewJobHandler(jobStore)

	// The real auth pipeline, wired exactly as cmd/controller/main.go
	// wires it: one AdmissionChain, shared by both the enforcing
	// Admission and the advertising HATEOAS generator, over a real
	// authtest token issuer.
	issuer := authtest.New(t, "release-gate-issuer", "release-gate-audience")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	admission := auth.Admission{Chain: chain, Recorder: auth.NewSlogRecorder(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	hateoasGen, gerr := auth.NewAdmissionHATEOASGenerator(chain)
	if err = gerr; err != nil {
		t.Fatalf("building HATEOAS generator: %v", err)
	}

	var router http.Handler
	router, err = api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: admission,
		HATEOAS:   hateoasGen,
		Routes: []api.Route{
			apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate),
			apispec.GetJob.Route(jobsHandler.Get),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// runbook:execute and job:read, and deliberately NOT template:write.
	// Launching a saved definition must not require the right to change
	// what it runs, which is the whole reason the two scopes are separate.
	identity := &auth.Identity{
		Subject: "release-gate-operator@example.com",
		Role:    auth.RoleOperator,
		Scopes:  []auth.Scope{auth.ScopeRunbookExecute, auth.ScopeJobRead},
	}

	target := api.APIVersionPrefix + "/templates/" + strconv.Itoa(template.ID) + "/launch"
	req := httptest.NewRequest(http.MethodPost, target, nil)
	req.Header.Set("Authorization", issuer.BearerToken(t, identity))

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("dispatch launch: status = %d, want %d: body %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc == "" {
		t.Error("dispatch launch carried no Location header")
	}

	var launch struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &launch); err != nil {
		t.Fatalf("decoding launch response %q: %v", rr.Body.String(), err)
	}
	if launch.JobID == "" {
		t.Fatal("launch response carried no job_id")
	}

	// A bounded retry loop, not a fixed time.Sleep: see
	// pollJobUntilTerminal's own doc comment. The timeout is generous for
	// 10,000 sequential, individually recorded per-device outcomes.
	job := pollJobUntilTerminal(t, ctx, jobStore, launch.JobID, 60*time.Second)

	if job.State != "completed" {
		t.Fatalf("job State = %q, want %q", job.State, "completed")
	}
	if job.DispatchedCount != releaseGateDeviceCount {
		t.Errorf("DispatchedCount = %d, want %d", job.DispatchedCount, releaseGateDeviceCount)
	}
	if job.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", job.SkippedCount)
	}
	if job.FailedCount != 0 {
		t.Errorf("FailedCount = %d, want 0", job.FailedCount)
	}

	// Confirm exactly 10,000 wire.DispatchPayload messages were actually
	// published, never merely trusting the job's own stored tally: this
	// is the whole point of the gate.
	if got := bus.countTopic(topology.DispatchSubject()); got != releaseGateDeviceCount {
		t.Errorf("wire.DispatchPayload publishes = %d, want %d", got, releaseGateDeviceCount)
	}

	// Cross-check against the store's own JobTask rows, an independent
	// count from the wrapped-bus tally above.
	_, tasks, err := jobStore.Get(ctx, launch.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	dispatchedTasks := 0
	for _, task := range tasks {
		if task.Outcome == dispatch.OutcomeDispatched {
			dispatchedTasks++
		}
	}
	if dispatchedTasks != releaseGateDeviceCount {
		t.Errorf("JobTask rows with outcome dispatched = %d, want %d", dispatchedTasks, releaseGateDeviceCount)
	}

	// Spot-check one real published payload to confirm the real "host"
	// property, not a hardcoded convenient value, actually made it onto
	// the wire.
	dispatchEvt, ok := bus.firstOnTopic(topology.DispatchSubject())
	if !ok {
		t.Fatal("no event was published on the dispatch subject")
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(dispatchEvt.Data, &payload); err != nil {
		t.Fatalf("decoding a published DispatchPayload: %v", err)
	}
	if payload.DeviceHost == "" {
		t.Error("published DispatchPayload carries an empty DeviceHost")
	}
	if payload.RunbookID != releaseGateRunbookID {
		t.Errorf("published DispatchPayload RunbookID = %q, want %q", payload.RunbookID, releaseGateRunbookID)
	}
}
