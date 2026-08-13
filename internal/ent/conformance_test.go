// This file is the adapter conformance suite: one set of assertions run
// identically against every dialect OpenDatabase supports.
//
// It deliberately covers what actually differs between dialects rather
// than re-testing ent. Enum columns, JSON columns, unique constraints,
// the many-to-many join the dispatcher's group targeting depends on,
// cascade deletes, and the affected-row semantics of a conditional UPDATE
// are each a place where SQLite and PostgreSQL could legitimately behave
// differently, and each is something this project's correctness rests on.
package ent_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/activityentry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/template"
)

// openConformanceClient opens a client against a fresh database for the
// given backend and closes it when the test ends.
func openConformanceClient(t *testing.T, backend storeBackend) (*ent.Client, string) {
	t.Helper()
	dsn := backend.newDSN(t)
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("OpenDatabase(%s): %v", backend.name, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, dsn
}

// TestConformance_OpenIsIdempotent proves opening the same database twice
// is a clean no-op rather than an error or a second migration run.
//
// This is migrate.Apply's stated guarantee, and it is exactly where a
// dialect-specific mistake in the version-record statement surfaces: the
// first open writes the schema_migrations rows, and the second is the
// only thing that reads them back and decides there is nothing to do.
func TestConformance_OpenIsIdempotent(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			dsn := backend.newDSN(t)
			ctx := context.Background()

			first, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("first OpenDatabase: %v", err)
			}
			if err := first.Close(); err != nil {
				t.Fatalf("closing the first client: %v", err)
			}

			second, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("second OpenDatabase against an already-migrated database: %v", err)
			}
			defer second.Close()

			// The reopened client must be usable, not merely constructed.
			if _, err := second.Device.Query().Count(ctx); err != nil {
				t.Fatalf("querying through the reopened client: %v", err)
			}
		})
	}
}

// TestConformance_DeviceRoundTrip proves every column type the Device
// schema uses survives a write and a read on this dialect.
//
// The JSON columns (properties, tags) are the interesting ones: SQLite
// stores them as text and PostgreSQL as jsonb, which are genuinely
// different storage engines for the same Go value.
func TestConformance_DeviceRoundTrip(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			properties := map[string]interface{}{
				"host":       "10.0.0.1",
				"port":       float64(2222),
				"enabled":    true,
				"interfaces": []interface{}{"Gi0/0", "Gi0/1"},
			}
			created := client.Device.Create().
				SetName("rtr1").
				SetType("cisco_router").
				SetProperties(properties).
				SetTags([]string{"edge", "production"}).
				SaveX(ctx)

			read := client.Device.GetX(ctx, created.ID)

			if read.Name != "rtr1" || read.Type != "cisco_router" {
				t.Fatalf("name/type round trip failed: got %q/%q", read.Name, read.Type)
			}
			// The schema default, not a value this test set.
			if read.State != "active" {
				t.Fatalf("state default = %q, want %q", read.State, "active")
			}
			if read.DeviceID == "" {
				t.Fatal("device_id was not populated by its DefaultFunc")
			}
			if got := read.Properties["host"]; got != "10.0.0.1" {
				t.Fatalf("properties[host] = %v, want 10.0.0.1", got)
			}
			if got := read.Properties["port"]; got != float64(2222) {
				t.Fatalf("properties[port] = %#v, want float64(2222); a dialect that reshapes JSON numbers would break capability resolution", got)
			}
			if got := read.Properties["enabled"]; got != true {
				t.Fatalf("properties[enabled] = %#v, want true", got)
			}
			if len(read.Tags) != 2 || read.Tags[0] != "edge" || read.Tags[1] != "production" {
				t.Fatalf("tags round trip failed: %#v", read.Tags)
			}
			if read.CreatedAt.IsZero() || read.UpdatedAt.IsZero() {
				t.Fatal("the timestamp mixin left created_at or updated_at zero")
			}
		})
	}
}

// TestConformance_DeviceNameIsUnique proves the unique index on
// devices.name is really enforced by this dialect's schema.
//
// A migration that silently dropped a unique index would otherwise be
// invisible until two devices with one name broke inventory lookups.
func TestConformance_DeviceNameIsUnique(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)

			_, err := client.Device.Create().SetName("rtr1").SetType("cisco_router").Save(ctx)
			if err == nil {
				t.Fatal("a second device with a duplicate name was accepted, so the unique index is not enforced")
			}
		})
	}
}

// TestConformance_GroupMembershipPredicate proves the exact predicate the
// dispatcher's group targeting uses returns exactly the group's members.
//
// internal/inventory's entRepository.GetGroup filters with
// device.HasGroupsWith(group.NameEQ(...)), which compiles to an EXISTS
// subquery over the group_devices join table. If that behaved differently
// here than on the other dialect, a dispatch would fan out to the wrong
// devices, which is the single most consequential thing this schema does.
func TestConformance_GroupMembershipPredicate(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			edge1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
			edge2 := client.Device.Create().SetName("rtr2").SetType("cisco_router").SaveX(ctx)
			core1 := client.Device.Create().SetName("rtr3").SetType("cisco_router").SaveX(ctx)
			// A device in no group at all, which must never be selected.
			client.Device.Create().SetName("rtr4").SetType("cisco_router").SaveX(ctx)

			client.Group.Create().SetName("edge").AddDevices(edge1, edge2).SaveX(ctx)
			client.Group.Create().SetName("core").AddDevices(core1).SaveX(ctx)

			selected := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("edge"))).
				Order(device.ByName()).
				AllX(ctx)

			if len(selected) != 2 {
				t.Fatalf("group edge selected %d devices, want 2", len(selected))
			}
			if selected[0].Name != "rtr1" || selected[1].Name != "rtr2" {
				t.Fatalf("group edge selected %q and %q, want rtr1 and rtr2", selected[0].Name, selected[1].Name)
			}

			// An unknown group must fail closed to zero devices, never to
			// every device.
			none := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("does-not-exist"))).
				AllX(ctx)
			if len(none) != 0 {
				t.Fatalf("an unknown group selected %d devices, want 0; a selector that fails open would dispatch to the whole fleet", len(none))
			}
		})
	}
}

// TestConformance_DeletingAGroupLeavesItsDevices proves the join table's
// cascade really made it into this dialect's DDL: removing a group must
// remove its membership rows and leave the devices themselves alone.
func TestConformance_DeletingAGroupLeavesItsDevices(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			d1 := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
			g := client.Group.Create().SetName("edge").AddDevices(d1).SaveX(ctx)

			client.Group.DeleteOne(g).ExecX(ctx)

			if count := client.Device.Query().CountX(ctx); count != 1 {
				t.Fatalf("deleting a group left %d devices, want 1; the cascade reached the devices themselves", count)
			}
			orphaned := client.Device.Query().
				Where(device.HasGroupsWith(group.NameEQ("edge"))).
				CountX(ctx)
			if orphaned != 0 {
				t.Fatalf("deleting a group left %d membership rows behind", orphaned)
			}
		})
	}
}

// TestConformance_JobFencing proves the conditional-UPDATE semantics the
// dispatcher's fan-out correctness rests on.
//
// BeginFanOut claims a job by moving it out of the pending state and
// bumping a fencing token, and it decides whether it won by counting the
// rows its conditional UPDATE affected. A dialect that reported that
// count differently would let two workers both believe they own one job,
// which is the failure this token exists to prevent.
//
// It lives here rather than in internal/dispatch's own tests so that
// internal/dispatch stays container free while still having its most
// dialect-sensitive behavior proven on both backends.
func TestConformance_JobFencing(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			store := dispatch.NewEntJobStore(client)
			job := &dispatch.Job{RunbookID: "ping", GroupName: "edge", Actor: "conformance"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("creating a job: %v", err)
			}

			claimed, fence, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("first BeginFanOut: %v", err)
			}
			if !claimed {
				t.Fatal("the first BeginFanOut did not claim a pending job")
			}

			// A second claim against a job already being fanned out, and
			// well inside its lease, must lose.
			reclaimed, _, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("second BeginFanOut: %v", err)
			}
			if reclaimed {
				t.Fatal("a second BeginFanOut claimed a job already being fanned out, so two workers would both fan it out")
			}

			// A write carrying a stale fence must be rejected.
			err = store.RecordTask(ctx, job.JobID, fence-1, dispatch.JobTask{
				DeviceID:   "device-1",
				DeviceName: "rtr1",
				Outcome:    dispatch.OutcomeDispatched,
			})
			if !errors.Is(err, dispatch.ErrFenced) {
				t.Fatalf("RecordTask with a stale fence returned %v, want ErrFenced", err)
			}

			// The current fence must still be accepted, so the guard
			// rejects staleness rather than everything.
			if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
				DeviceID:   "device-1",
				DeviceName: "rtr1",
				Outcome:    dispatch.OutcomeDispatched,
			}); err != nil {
				t.Fatalf("RecordTask with the current fence: %v", err)
			}
		})
	}
}

// TestConformance_JobEnumsRoundTrip proves both enum columns survive a
// write and a read on this dialect.
//
// ent renders these as varchar with a check rather than as a native
// PostgreSQL enum type, which is what makes them portable; this test is
// what would notice if that ever changed.
func TestConformance_JobEnumsRoundTrip(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			store := dispatch.NewEntJobStore(client)
			job := &dispatch.Job{RunbookID: "ping", GroupName: "edge", Actor: "conformance"}
			if err := store.Create(ctx, job); err != nil {
				t.Fatalf("creating a job: %v", err)
			}
			if job.State != "pending" {
				t.Fatalf("a new job's state = %q, want the schema default %q", job.State, "pending")
			}

			_, fence, err := store.BeginFanOut(ctx, job.JobID, dispatch.DefaultFanOutLeaseTTL)
			if err != nil {
				t.Fatalf("BeginFanOut: %v", err)
			}

			// One task per outcome, so every value of the enum is
			// actually written rather than just the first.
			outcomes := []dispatch.Outcome{
				dispatch.OutcomeDispatched,
				dispatch.OutcomeSkipped,
				dispatch.OutcomeFailed,
			}
			for i, outcome := range outcomes {
				if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
					DeviceID:   fmt.Sprintf("device-%d", i),
					DeviceName: fmt.Sprintf("rtr%d", i),
					Outcome:    outcome,
					Reason:     string(outcome) + " reason",
				}); err != nil {
					t.Fatalf("RecordTask(%s): %v", outcome, err)
				}
			}

			if err := store.Complete(ctx, job.JobID, fence, 1, 1, 1); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			read, tasks, err := store.Get(ctx, job.JobID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if read.State != "completed" {
				t.Fatalf("state = %q, want completed", read.State)
			}
			if len(tasks) != len(outcomes) {
				t.Fatalf("got %d tasks, want %d", len(tasks), len(outcomes))
			}
			seen := make(map[dispatch.Outcome]bool, len(tasks))
			for _, task := range tasks {
				seen[task.Outcome] = true
			}
			for _, outcome := range outcomes {
				if !seen[outcome] {
					t.Fatalf("outcome %q did not survive the round trip", outcome)
				}
			}
		})
	}
}

// TestConformance_ConcurrentWrites proves the dialect tolerates several
// writers at once through one pool.
//
// On SQLite this exercises the busy timeout the adapter sets, and on
// PostgreSQL it exercises the pool itself. Either way, the failure this
// catches is a write that silently does not happen.
func TestConformance_ConcurrentWrites(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			const writers = 8
			errs := make(chan error, writers)
			var wg sync.WaitGroup
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := client.Device.Create().
						SetName(fmt.Sprintf("rtr%d", i)).
						SetType("cisco_router").
						Save(ctx)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)

			for err := range errs {
				if err != nil {
					t.Fatalf("a concurrent write failed: %v", err)
				}
			}
			if count := client.Device.Query().CountX(ctx); count != writers {
				t.Fatalf("after %d concurrent writes the table holds %d rows", writers, count)
			}
		})
	}
}

// TestConformance_ContactsCascadeWithTheirOwner proves the ON DELETE
// CASCADE on both of a Contact's owner edges is really emitted by each
// dialect's migration, rather than only by the one that happened to be
// generated first.
//
// It is the only cascade in this schema and it is load bearing. A Contact
// attaches to exactly one of an Organization or a Team, an invariant the
// repository enforces because ent cannot express it. Under ent's default of
// nulling the reference, deleting an owner would leave a row attached to
// neither, which is precisely the record the write path refuses to accept:
// the database would manufacture a state no caller could have created.
//
// It lives here rather than in internal/access so the assertion runs on both
// backends. internal/access is container free and would only ever prove it
// for SQLite, and a foreign key clause is exactly the kind of thing two
// dialects disagree about.
func TestConformance_ContactsCascadeWithTheirOwner(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			org := client.Organization.Create().SetName("acme").SaveX(ctx)
			team := client.Team.Create().SetName("netops").SetOrganization(org).SaveX(ctx)

			client.Contact.Create().
				SetName("Platform owner").
				SetRole("owner").
				SetEmail("platform@example.com").
				SetOrganization(org).
				SaveX(ctx)
			client.Contact.Create().
				SetName("Netops rota").
				SetRole("escalation").
				SetPhone("+1-555-0100").
				SetDisplayOrder(10).
				SetTeam(team).
				SaveX(ctx)

			if n := client.Contact.Query().CountX(ctx); n != 2 {
				t.Fatalf("seeded %d contacts, want 2", n)
			}

			// The team goes first: an organization holding a team cannot be
			// deleted at all, because a team's organization edge is
			// required. That refusal is separate and correct, and tripping
			// it here would hide whether the cascade works.
			client.Team.DeleteOne(team).ExecX(ctx)
			if n := client.Contact.Query().CountX(ctx); n != 1 {
				t.Fatalf("deleting a team left %d contacts, want 1: its own contact outlived it, owned by nothing", n)
			}

			client.Organization.DeleteOne(org).ExecX(ctx)
			if n := client.Contact.Query().CountX(ctx); n != 0 {
				t.Fatalf("deleting an organization left %d contacts, each attached to neither an organization nor a team", n)
			}
		})
	}
}

// TestConformance_ActivityOutlivesWhatItDescribes proves an activity entry
// survives the deletion of the object it is about, on both dialects.
//
// This is the assertion behind ActivityEntry carrying a kind string and a
// bare integer instead of edges. An edge would mean a foreign key, and a
// foreign key means the deletion of an object either cascades away its own
// audit trail or is refused by it. "Who deleted this, and when" is exactly
// the entry somebody needs after the object is gone, so the trail has to
// outlive its subject.
//
// It lives here rather than in internal/activity for the reason the cascade
// test above states: internal/activity is container free and would only ever
// prove it for SQLite, and referential behaviour is precisely what two
// dialects disagree about. The relationship being asserted is a negative --
// that no constraint exists -- which is the kind that gets introduced by
// accident later, by somebody adding the edge that looks obviously missing.
func TestConformance_ActivityOutlivesWhatItDescribes(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			org := client.Organization.Create().SetName("acme").SaveX(ctx)

			client.ActivityEntry.Create().
				SetActor("ada@example.com").
				SetAction("created").
				SetObjectKind("organization").
				SetObjectID(org.ID).
				SetObjectName("acme").
				SaveX(ctx)
			client.ActivityEntry.Create().
				SetActor("grace@example.com").
				SetAction("deleted").
				SetObjectKind("organization").
				SetObjectID(org.ID).
				SetObjectName("acme").
				SaveX(ctx)

			client.Organization.DeleteOne(org).ExecX(ctx)

			entries := client.ActivityEntry.Query().
				Where(activityentry.ObjectKindEQ("organization"), activityentry.ObjectIDEQ(org.ID)).
				Order(ent.Asc(activityentry.FieldID)).
				AllX(ctx)
			if len(entries) != 2 {
				t.Fatalf("deleting the organization left %d of its 2 activity entries: "+
					"the audit trail did not outlive what it describes", len(entries))
			}

			// The name is still there too. An entry that resolved the name
			// live would now describe nothing, which is the same loss in a
			// quieter form.
			if entries[1].ObjectName != "acme" {
				t.Errorf("the deletion entry names %q after the object was removed, want %q captured at the time",
					entries[1].ObjectName, "acme")
			}
			if entries[1].ObjectID != org.ID {
				t.Errorf("the deletion entry names object %d, want the deleted organization's own id %d",
					entries[1].ObjectID, org.ID)
			}
		})
	}
}

// TestConformance_LaunchTemplateRoundTrip proves the Phase 21 entities
// really exist on both dialects, with the columns and constraints the Go
// code expects.
//
// It exists because nothing else would have caught a migration that applied
// cleanly and produced the wrong schema. The other conformance tests here
// cover the entities that predate this phase; a template's columns are
// exercised by no query anywhere until the API and the UI arrive, so a
// missing column would first surface at run time in a deployment rather
// than in a build.
//
// The doc comment on internal/ent/migrate/parity_test.go says the stronger
// claim, that each dialect's committed migrations bring a database all the
// way to the schema ent currently desires, "lives behind the integration
// build tag in parity_integration_test.go". That file does not exist. Until
// it does, per-entity round trips like this one, run against both real
// backends, are what actually holds the claim up.
func TestConformance_LaunchTemplateRoundTrip(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			org := client.Organization.Create().SetName("acme").SaveX(ctx)
			inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)

			tmpl := client.Template.Create().
				SetName("patch the edge routers").
				SetKind("runbook").
				SetDefinition("patch-edge").
				SetDefaults(map[string]any{"limit": "edge-*", "forks": 5}).
				SetPrompts([]string{"limit"}).
				SetRequiredCaps([]string{"SSHCapable"}).
				SetSurveyEnabled(true).
				SetOrganization(org).
				SetInventory(inv).
				SaveX(ctx)

			client.SurveyQuestion.Create().
				SetVariable("target_version").
				SetLabel("Version").
				SetQuestionType("multiplechoice").
				SetRequired(true).
				SetChoices([]string{"17.3", "17.6"}).
				SetDisplayOrder(1).
				SetTemplate(tmpl).
				SaveX(ctx)

			client.SavedLaunchConfig.Create().
				SetName("nightly").
				SetFields(map[string]any{"limit": "edge-01"}).
				SetAnswers(map[string]any{"target_version": "17.6"}).
				SetTemplate(tmpl).
				SaveX(ctx)

			// The JSON columns survive a round trip with their shapes
			// intact. A map that came back as a string, or a list that came
			// back as a map, would still "work" until something read it.
			read := client.Template.Query().
				Where(template.IDEQ(tmpl.ID)).
				WithSurveyQuestions().
				WithSavedConfigs().
				WithOrganization().
				WithInventory().
				OnlyX(ctx)

			if read.Defaults["limit"] != "edge-*" {
				t.Errorf("defaults came back as %#v", read.Defaults)
			}
			if len(read.Prompts) != 1 || read.Prompts[0] != "limit" {
				t.Errorf("prompts came back as %#v", read.Prompts)
			}
			if len(read.Edges.SurveyQuestions) != 1 {
				t.Fatalf("the template carries %d questions, want 1", len(read.Edges.SurveyQuestions))
			}
			if choices := read.Edges.SurveyQuestions[0].Choices; len(choices) != 2 {
				t.Errorf("the question's choices came back as %#v", choices)
			}
			if read.Edges.Organization == nil || read.Edges.Inventory == nil {
				t.Fatal("the template lost one of its two required edges")
			}

			// A name is unique within an organization, not globally: two
			// tenants both having a "patch the edge routers" template is
			// the ordinary case, and the constraint belongs to the schema
			// rather than to the Go code above it.
			other := client.Organization.Create().SetName("other").SaveX(ctx)
			otherInv := client.Inventory.Create().SetName("edge").SetOrganization(other).SaveX(ctx)
			if _, err := client.Template.Create().
				SetName("patch the edge routers").
				SetKind("runbook").SetDefinition("patch-edge").
				SetOrganization(other).SetInventory(otherInv).
				Save(ctx); err != nil {
				t.Errorf("a second tenant could not reuse a template name: %v", err)
			}
			if _, err := client.Template.Create().
				SetName("patch the edge routers").
				SetKind("runbook").SetDefinition("patch-edge").
				SetOrganization(org).SetInventory(inv).
				Save(ctx); err == nil {
				t.Error("one organization holds two templates with the same name")
			}
		})
	}
}

// TestConformance_TemplateChildrenCascadeButJobsDoNot pins the two opposite
// referential decisions this phase makes, on both dialects.
//
// A survey question and a saved configuration belong to their template and
// are meaningless without it, so they cascade. A job does not: it carries
// template_id as a plain column with no foreign key, because a job is a
// historical record and "what did this template run" is precisely the
// question somebody has after the template is gone. Getting either
// backwards is invisible until the day somebody deletes a template.
func TestConformance_TemplateChildrenCascadeButJobsDoNot(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			client, _ := openConformanceClient(t, backend)
			ctx := context.Background()

			org := client.Organization.Create().SetName("acme").SaveX(ctx)
			inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
			tmpl := client.Template.Create().
				SetName("patch").SetKind("runbook").SetDefinition("patch-edge").
				SetOrganization(org).SetInventory(inv).SaveX(ctx)

			client.SurveyQuestion.Create().
				SetVariable("v").SetLabel("V").SetQuestionType("text").
				SetTemplate(tmpl).SaveX(ctx)
			client.SavedLaunchConfig.Create().SetName("nightly").SetTemplate(tmpl).SaveX(ctx)

			client.Job.Create().
				SetRunbookID("patch-edge").
				SetGroupName("").
				SetActor("ada@example.com").
				SetTemplateID(tmpl.ID).
				SetTemplateName("patch").
				SetKind("runbook").
				SetInventoryID(inv.ID).
				SetOrganizationID(org.ID).
				SaveX(ctx)

			client.Template.DeleteOne(tmpl).ExecX(ctx)

			if n := client.SurveyQuestion.Query().CountX(ctx); n != 0 {
				t.Errorf("deleting a template left %d survey questions attached to nothing", n)
			}
			if n := client.SavedLaunchConfig.Query().CountX(ctx); n != 0 {
				t.Errorf("deleting a template left %d saved configurations attached to nothing", n)
			}

			// The job survived, and still says what it ran.
			jobs := client.Job.Query().AllX(ctx)
			if len(jobs) != 1 {
				t.Fatalf("deleting a template took %d of its jobs with it", 1-len(jobs))
			}
			if jobs[0].TemplateName != "patch" {
				t.Errorf("the surviving job names template %q, want the name captured at launch", jobs[0].TemplateName)
			}
			if jobs[0].TemplateID == nil || *jobs[0].TemplateID != tmpl.ID {
				t.Error("the surviving job lost the id of the template it ran")
			}
		})
	}
}
