// The Controller's refusal of a seeded login, on real NATS (decided
// 2026-11-11): a job whose runbook would seed a device's login into a
// machine it creates fails at the Controller's fan-out, and no message
// reaches the stream a Runner reads. A Runner's own refusal (the engine's
// seedLogin) would come only after the dispatch had crossed the stream, and
// a seed is to reach a Runner only through the claim-time credential fetch,
// which is not built yet.
package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// seededLoginRunbook clones a VirtualBox machine seeded with lab-web's
// login: virt.vbox.vm.clone's manifest sets SeedsLogin on its required login
// parameter.
const seededLoginRunbook = `id: seeded
tasks:
  - name: clone a machine with lab-web's login
    virt.vbox.vm.clone:
      base: ubuntu-base
      name: lab-web
      login: lab-web
`

// TestController_RefusesASeededLoginBeforePublish runs the Controller's
// fan-out Worker, over the real ent store and the module catalog the
// Controller installs, against real NATS JetStream. The seeding job fails
// with a reason naming the task, the method and the device, and the stream
// holds no message on any dispatch or check subject. The control, a job of
// a runbook that seeds nothing on the same device, puts exactly one dispatch
// on the stream, so the count is one that can see a dispatch.
func TestController_RefusesASeededLoginBeforePublish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the real-NATS seeded login gate in short mode")
	}
	ctx := context.Background()
	// The shipped Controller links no Collection and reads every manifest
	// from the module catalog it embeds; the manifest read here is that one.
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if _, registered := collection.Lookup("virt.vbox.vm.clone"); !registered {
		if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
			t.Fatal(err)
		}
	}
	if m, ok := collection.ManifestOf("virt.vbox.vm.clone"); !ok || m.SeedsLogin != "login" {
		t.Fatalf("virt.vbox.vm.clone's manifest seeds %q (known %v), so this would test nothing", m.SeedsLogin, ok)
	}

	url := testsupport.StartNATS(t).URL()
	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + t.TempDir() + "/controller.db"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store := dispatch.NewEntJobStore(client)
	repo := &gateRepository{devices: []pkginventory.InventoryItem{&inventorytest.Stub{
		StubID: "vbox-host", StubName: "vbox-host", StubState: pkginventory.StateActive,
		// What virt.vbox.vm.clone needs to be admitted, so without the
		// refusal the job would be dispatched rather than skipped.
		Caps:  []capability.Name{capability.NameWinRM, capability.NameVirtualBox},
		Props: map[string]pkginventory.PropertyValue{"host": "192.0.2.10"},
	}}}
	dir := t.TempDir()
	for name, body := range map[string]string{"seeded.yaml": seededLoginRunbook, "plain.yaml": "id: plain\ntasks:\n  - name: nothing\n    noop: {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, gateCredentials{})
	launch := func(id string) *dispatch.Job {
		t.Helper()
		job := &dispatch.Job{RunbookID: id, GroupName: "gate", Actor: "release-gate"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		evt, err := event.WrapPayload(uuid.New().String(), "job.requested", map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.HandleJobRequested(*evt); err != nil {
			t.Fatalf("%s: HandleJobRequested: %v", id, err)
		}
		got, _, err := store.Get(ctx, job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	onStream := func() uint64 {
		t.Helper()
		stream, err := js.Stream(ctx, topology.StreamName)
		if err != nil {
			t.Fatal(err)
		}
		var n uint64
		for _, filter := range []string{topology.DispatchSubjectAll(), topology.CheckSubjectAll()} {
			info, err := stream.Info(ctx, jetstream.WithSubjectFilter(filter))
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range info.State.Subjects {
				n += count
			}
		}
		return n
	}

	seeded := launch("seeded")
	if seeded.State != "failed" {
		t.Errorf("the seeding job is %q, want failed", seeded.State)
	}
	for _, want := range []string{"virt.vbox.vm.clone", `device "lab-web"`, "seeds the login"} {
		if !strings.Contains(seeded.FailureReason, want) {
			t.Errorf("failure reason %q does not name %q", seeded.FailureReason, want)
		}
	}
	if n := onStream(); n != 0 {
		t.Fatalf("%d messages reached the dispatch and check subjects for a seeding job, want none", n)
	}

	if plain := launch("plain"); plain.State == "failed" {
		t.Fatalf("control: the plain job failed: %s", plain.FailureReason)
	}
	if n := onStream(); n != 1 {
		t.Errorf("control: %d messages on the dispatch and check subjects, want the plain job's one", n)
	}
}
