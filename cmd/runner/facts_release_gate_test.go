// The Walk tier's Release Gate for persisted facts (Phase 41): a job's task
// gathers facts on a real device through a real Runner; the Runner publishes
// them over real NATS; the Controller's consumer stores them in its database
// for the device it dispatched; and they are read back through the device
// resource's facts route, as the device itself reports them. A stored value
// cannot be edited in place: the schema gives no way to.
package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// gateRunnerID is the name the gates' Runner labels the facts it publishes
// with (native.WithRunnerID), as cmd/runner passes its host's name.
const gateRunnerID = "gate-runner"

// factsWalkRunbook gathers the device's facts.
const factsWalkRunbook = `# Gathers the device's facts. Read only; nothing to undo.
id: facts-walk
tasks:
  - name: gather
    facts.gather: {}
`

// factsWalkGate is one facts job run end to end: a real Runner gathers on a
// real sshd device, publishes over real NATS, and the Controller's consumer
// (with subOpts) stores what it sends; the Worker dispatches with workerOpts.
type factsWalkGate struct {
	h         *releaseGateHarness
	client    *ent.Client
	factStore *entstore.Store
	job       *dispatch.Job
	quiet     *slog.Logger
}

// factsWalkGateDevice and factsWalkGateName are the gate device's ID and
// name, apart, so a digest bound to the one and checked against the other
// would fail here rather than only where they differ in production.
const (
	factsWalkGateDevice = "facts-walk-id-1"
	factsWalkGateName   = "facts-walk-1"
)

// runFactsWalkGate runs runbookYAML (facts-walk.yaml) as a Controller job and
// waits for it to complete; prepare runs on the device first, as root.
func runFactsWalkGate(t *testing.T, runbookYAML, prepare string, workerOpts []dispatch.WorkerOption, subOpts []entstore.SubscriberOption) factsWalkGate {
	t.Helper()
	files := map[string]string{"facts-walk.yaml": runbookYAML}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, files)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if prepare != "" {
		h.onDevice(t, prepare)
	}

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	factStore := entstore.New(client)

	const deviceID, deviceName = factsWalkGateDevice, factsWalkGateName
	client.Device.Create().SetName(deviceName).SetType("linux_server").SetDeviceID(deviceID).
		SetProperties(map[string]any{"host": h.sshHost, "port": h.sshPort}).SaveX(ctx)
	device := &sshGateDevice{
		Stub: &inventorytest.Stub{
			StubID: deviceID, StubName: deviceName, StubState: pkginventory.StateActive,
			Caps:  []capability.Name{capability.NameSSHTransport, capability.NameFactGatherer},
			Props: map[string]pkginventory.PropertyValue{"host": h.sshHost},
		},
		port: h.sshPort,
	}
	runbookDir := t.TempDir()
	writeRunbooks(t, runbookDir, files)
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, &gateRepository{devices: []pkginventory.InventoryItem{device}}, runbooks, h.bus, gateCredentials{}, workerOpts...)
	subCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := dispatch.NewResultConsumer(store, quiet).Subscribe(subCtx, h.bus); err != nil {
		t.Fatal(err)
	}
	admit := func(ctx context.Context, jobID, device string) (journal.Admission, error) {
		state, err := store.DispatchState(ctx, jobID, device)
		switch {
		case err != nil:
			return journal.AdmitUnrecorded, err
		case state == dispatch.DispatchSent:
			return journal.AdmitDispatched, nil
		case state == dispatch.DispatchNotSent:
			return journal.AdmitNotDispatched, nil
		default:
			return journal.AdmitUnrecorded, nil
		}
	}
	if err := entstore.NewSubscriber(factStore, quiet, admit, subOpts...).Subscribe(subCtx, h.bus); err != nil {
		t.Fatal(err)
	}

	job := &dispatch.Job{RunbookID: "facts-walk", GroupName: "gate", Actor: "release-gate", Kind: "runbook"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.HandleJobRequested(*evt); err != nil {
		t.Fatal(err)
	}
	if !jobReaches(store, job.JobID, "completed", 2*time.Minute) {
		t.Fatalf("job never completed; devices:\n%s\nRunner events:\n%s", jobRows(store, job.JobID), jobEvents(t, h, job.JobID))
	}
	return factsWalkGate{h: h, client: client, factStore: factStore, job: job, quiet: quiet}
}

func TestFactsReleaseGate_AControllerJobStoresWhatItsTaskObserved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	g := runFactsWalkGate(t, factsWalkRunbook, "", nil, nil)
	h, client, factStore, job, quiet, ctx := g.h, g.client, g.factStore, g.job, g.quiet, context.Background()
	const deviceName = factsWalkGateName

	// The device resource's facts route, over the Controller's own device
	// repository and the store the consumer writes.
	devices := api.NewDeviceHandler(inventory.NewEntRepository(client, inventory.NewItemFactory()), inventory.NewItemFactory(), quiet)
	devices.WithFacts(factStore)
	router := chi.NewRouter()
	router.Get("/inventory/devices/{name}/facts", devices.Facts)
	router.Get("/inventory/devices/{name}/fact-chain", devices.FactChain)
	var body struct {
		Device string `json:"device"`
		Facts  []struct {
			Key   string           `json:"key"`
			Value any              `json:"value"`
			Kind  string           `json:"kind"`
			By    facts.Provenance `json:"by"`
		} `json:"facts"`
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(200 * time.Millisecond) {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inventory/devices/"+deviceName+"/facts", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("the facts route: %d %s", rr.Code, rr.Body.String())
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Facts) > 0 || time.Now().After(deadline) {
			break
		}
	}
	kernel := ""
	for _, f := range body.Facts {
		if f.Key == "ansible_kernel" {
			kernel, _ = f.Value.(string)
		}
	}
	if want := h.onDevice(t, "uname -r"); body.Device != deviceName || kernel != want {
		t.Fatalf("the route read device %q with ansible_kernel %q; the device says %q (%d facts)", body.Device, kernel, want, len(body.Facts))
	}
	if n := client.Fact.Query().CountX(ctx); n == 0 || n != len(body.Facts) {
		t.Errorf("%d rows for %d current facts after one gather", n, len(body.Facts))
	}
	update := reflect.TypeOf(&ent.FactUpdateOne{})
	for _, setter := range []string{"SetKey", "SetPayload", "SetHash"} {
		if _, ok := update.MethodByName(setter); ok {
			t.Errorf("a stored fact can be edited in place through %s", setter)
		}
	}
	if strings.TrimSpace(kernel) == "" {
		t.Error("no kernel was stored")
	}

	// Phase 41b: each value says which job, attempt, method and Runner
	// observed it, and the run's order is the device lease's fencing token;
	// the kinds are the method's declaration.
	kinds := map[string]string{}
	for _, f := range body.Facts {
		kinds[f.Key] = f.Kind
		by := f.By
		if by.Job != job.JobID || by.Attempt < 1 || by.Method != "facts.gather" || by.Runner != gateRunnerID || by.Node == "" ||
			!strings.HasPrefix(by.Order.Source, "lease:") || by.Order.Token == 0 {
			t.Errorf("%s: provenance %+v, want this job, its attempt, facts.gather, the gate's Runner and the lease's token", f.Key, by)
		}
	}
	if kinds["ansible_kernel"] != "stable" || kinds["ansible_uptime_seconds"] != "volatile" {
		t.Errorf("kinds = %v, want the kernel stable and the uptime volatile as declared", kinds)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/inventory/devices/"+deviceName+"/fact-chain", nil))
	var chain facts.Verdict
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &chain) != nil || chain.Break != nil || chain.Values == 0 {
		t.Errorf("the fact chain: %d %s", rr.Code, rr.Body.String())
	}
}

// factsDigestRunbook describes two files: one anyone may read, one only its
// owner may.
const factsDigestRunbook = `# Describes two files. Read only; nothing to undo.
id: facts-walk
tasks:
  - name: gather
    facts.gather:
      gather_subset:
        - "!all"
        - "!min"
      files:
        - /etc/hostname
        - /tmp/walk-mine
`

// TestFactsReleaseGate_APrivateFilesDigestIsStoredOnlyKeyed: on the Walk tier
// a digest of a file not everyone may read leaves the Runner sealed to the
// Controller's key and is stored keyed under the Controller's own: the stored
// value is exactly the Controller's HMAC of the file's real digest, and no row
// holds the digest itself. A file anyone may read keeps its plain digest.
func TestFactsReleaseGate_APrivateFilesDigestIsStoredOnlyKeyed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	open, keyer, err := entstore.DigestKeys([]byte("a 32-byte facts digest secret!!!"))
	if err != nil {
		t.Fatal(err)
	}
	g := runFactsWalkGate(t, factsDigestRunbook,
		"printf 'pin1234\\n' > /tmp/walk-mine && chown "+releaseGateSSHUser+" /tmp/walk-mine && chmod 600 /tmp/walk-mine",
		[]dispatch.WorkerOption{dispatch.WithFactSealKey(open.Public().String())},
		[]entstore.SubscriberOption{entstore.WithDigestKeys(open, keyer)})
	ctx := context.Background()
	var current []facts.Entry
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(200 * time.Millisecond) {
		current, err = g.factStore.Current(ctx, factsWalkGateDevice)
		if err != nil {
			t.Fatal(err)
		}
		if len(current) >= 2 || time.Now().After(deadline) {
			break
		}
	}
	values := map[string]map[string]any{}
	for _, e := range current {
		values[e.Key], _ = e.Value.(map[string]any)
	}
	raw := sha256.Sum256([]byte("pin1234\n"))
	rawHex := hex.EncodeToString(raw[:])
	mine := values["file.%2Ftmp%2Fwalk-mine"]
	want, _ := keyer.Seal(factsWalkGateDevice, "file.%2Ftmp%2Fwalk-mine", rawHex)
	if mine == nil || mine["hmac_sha256"] != want || mine["hmac_key"] != keyer.ID() || mine["world_readable"] != false ||
		mine["sha256_sealed"] != nil || mine["sha256_raw"] != nil || mine["sha256"] != nil || mine["size"] != nil {
		t.Errorf("the private file is stored as %v, want the Controller's keyed digest %s alone", mine, want)
	}
	hostname := strings.Fields(g.h.onDevice(t, "sha256sum /etc/hostname"))[0]
	if pub := values["file.%2Fetc%2Fhostname"]; pub == nil || pub["sha256"] != hostname || pub["world_readable"] != true {
		t.Errorf("/etc/hostname is stored as %v, want its plain digest %s", pub, hostname)
	}
	rows, _ := json.Marshal(g.client.Fact.Query().AllX(ctx))
	if strings.Contains(string(rows), rawHex) {
		t.Error("a stored row holds the private file's raw digest")
	}
}
