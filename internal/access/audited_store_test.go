package access_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the audited store against a real database and a real
// activity store, per RULE 0. The behaviour under test is "a write leaves a
// record", which a mock recorder could report without a row existing
// anywhere, so the recorder here is the real ent-backed one and the
// assertion is a query against the stream.

// auditedFixture is a wrapped store, the raw store underneath it, and the
// stream it writes to.
type auditedFixture struct {
	audited access.Store
	raw     access.Store
	stream  activity.Store
}

// testActor is the subject every audited write in this file is attributed
// to unless a case overrides it.
const testActor = "ada@example.com"

// actorKey carries the acting subject in a test context, standing in for
// the authenticated identity the Controller's own source reads.
type actorKey struct{}

// contextActor is the ActorSource under test: it reads whatever the caller
// put in the context, and returns empty when nobody did, which is the case
// ErrUnattributed exists for.
func contextActor(ctx context.Context) string {
	subject, _ := ctx.Value(actorKey{}).(string)
	return subject
}

// actingAs returns a context attributed to subject.
func actingAs(subject string) context.Context {
	return context.WithValue(context.Background(), actorKey{}, subject)
}

// newAuditedFixture builds the real stack: a real database, the real access
// store, the real activity store, and the decorator over both.
func newAuditedFixture(t *testing.T) auditedFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:audited%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })

	raw := access.NewEntStore(client)
	stream := activity.NewEntStore(client)
	return auditedFixture{
		audited: access.NewAuditedStore(raw, stream, contextActor, slog.New(slog.DiscardHandler)),
		raw:     raw,
		stream:  stream,
	}
}

// entries reads the whole stream, newest first.
func (f auditedFixture) entries(t *testing.T) []activity.Entry {
	t.Helper()
	entries, err := f.stream.List(context.Background(), activity.Query{})
	if err != nil {
		t.Fatalf("listing the activity stream: %v", err)
	}
	return entries
}

// only asserts the stream holds exactly one entry and returns it.
func (f auditedFixture) only(t *testing.T) activity.Entry {
	t.Helper()
	entries := f.entries(t)
	if len(entries) != 1 {
		t.Fatalf("the stream holds %d entries, want exactly 1", len(entries))
	}
	return entries[0]
}

func TestAuditedStore_ACreateNamesTheActorAndTheObject(t *testing.T) {
	f := newAuditedFixture(t)

	org, err := f.audited.CreateOrganization(actingAs(testActor), access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	entry := f.only(t)
	switch {
	case entry.Actor != testActor:
		t.Errorf("entry is attributed to %q, want %q", entry.Actor, testActor)
	case entry.Action != activity.ActionCreated:
		t.Errorf("entry records %q, want %q", entry.Action, activity.ActionCreated)
	case entry.ObjectKind != activity.KindOrganization:
		t.Errorf("entry names kind %q, want %q", entry.ObjectKind, activity.KindOrganization)
	case entry.ObjectID != org.ID:
		t.Errorf("entry names object %d, want %d", entry.ObjectID, org.ID)
	case entry.ObjectName != "Network":
		t.Errorf("entry names %q, want %q", entry.ObjectName, "Network")
	}
}

func TestAuditedStore_ADeleteKeepsTheNameTheObjectHadWhenItWasDeleted(t *testing.T) {
	f := newAuditedFixture(t)
	ctx := actingAs(testActor)

	org, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Servers"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := f.audited.DeleteOrganization(ctx, org.ID); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}

	entries := f.entries(t)
	if len(entries) != 2 {
		t.Fatalf("create then delete left %d entries, want 2", len(entries))
	}

	// The deletion is the newest entry, and it still names Servers even
	// though nothing in the database is called that any more. An entry
	// that joined to the row live would read "deleted organization 1"
	// here, which is the line an auditor least wants to find.
	deletion := entries[0]
	if deletion.Action != activity.ActionDeleted {
		t.Fatalf("newest entry records %q, want %q", deletion.Action, activity.ActionDeleted)
	}
	if deletion.ObjectName != "Servers" {
		t.Errorf("the deletion entry names %q, want %q captured before the row was removed",
			deletion.ObjectName, "Servers")
	}
}

func TestAuditedStore_AnUnattributedWriteIsRefusedAndChangesNothing(t *testing.T) {
	f := newAuditedFixture(t)

	// No actor in the context at all: the shape a background path or a
	// composition root that forgot to wire a source would produce.
	_, err := f.audited.CreateOrganization(context.Background(), access.Organization{Name: "Network"})
	if !errors.Is(err, access.ErrUnattributed) {
		t.Fatalf("CreateOrganization with no actor returned %v, want ErrUnattributed", err)
	}

	// The side effect is the assertion, not the status. A refusal that
	// still wrote the row would be worse than no refusal: the change would
	// exist with the caller told it had not.
	orgs, err := f.raw.ListOrganizations(context.Background(), access.Query{})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(orgs) != 0 {
		t.Errorf("a refused write created %d organizations", len(orgs))
	}
	if entries := f.entries(t); len(entries) != 0 {
		t.Errorf("a refused write left %d entries in the stream", len(entries))
	}
}

func TestAuditedStore_AFailedWriteRecordsNothingAndReportsTheFailure(t *testing.T) {
	f := newAuditedFixture(t)
	ctx := actingAs(testActor)

	if _, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Network"}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	// A duplicate name: a real constraint refusal from the schema, not a
	// simulated one.
	_, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Network"})
	if !errors.Is(err, access.ErrExists) {
		t.Fatalf("duplicate CreateOrganization returned %v, want ErrExists", err)
	}

	// One entry, from the write that succeeded. A decorator that recorded
	// before checking the error would claim an organization was created
	// twice.
	if entries := f.entries(t); len(entries) != 1 {
		t.Errorf("a failed write left %d entries, want the 1 from the successful write", len(entries))
	}
}

// failingRecorder is a recorder that always fails.
//
// A double, and deliberately so: the behaviour under test is what the
// decorator does when recording fails, and a real ent store cannot be made
// to fail on demand without also breaking the write whose survival is the
// whole assertion.
type failingRecorder struct{ err error }

func (r failingRecorder) Record(context.Context, activity.Entry) error { return r.err }

func TestAuditedStore_AFailedRecordingDoesNotUndoTheWrite(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:audited%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })

	raw := access.NewEntStore(client)
	audited := access.NewAuditedStore(raw, failingRecorder{err: errors.New("the stream is unreachable")},
		contextActor, slog.New(slog.DiscardHandler))

	org, err := audited.CreateOrganization(actingAs(testActor), access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("CreateOrganization returned %v: a failed recording must not be reported as a failed write, "+
			"since the write happened and a caller that retried would make it twice", err)
	}
	if org.ID == 0 {
		t.Fatal("CreateOrganization returned no organization")
	}

	if _, err := raw.GetOrganization(context.Background(), org.ID); err != nil {
		t.Errorf("the organization is not in the database after a failed recording: %v", err)
	}
}

func TestAuditedStore_ReadsAreNotRecorded(t *testing.T) {
	f := newAuditedFixture(t)
	ctx := actingAs(testActor)

	org, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	// An activity stream that recorded reads would be a request log, and
	// would bury the handful of changes an investigation is looking for
	// under every page view that ever happened.
	if _, err := f.audited.GetOrganization(ctx, org.ID); err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if _, err := f.audited.ListOrganizations(ctx, access.Query{}); err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}

	if entries := f.entries(t); len(entries) != 1 {
		t.Errorf("reads left %d entries, want only the 1 from the create", len(entries))
	}
}

func TestAuditedStore_AnAttestationIsRecordedAsItsOwnAction(t *testing.T) {
	f := newAuditedFixture(t)
	ctx := actingAs(testActor)

	org, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := f.audited.AttestOrganization(ctx, org.ID, testActor); err != nil {
		t.Fatalf("AttestOrganization: %v", err)
	}

	// Its own verb rather than an update. An attestation is the claim the
	// whole ownership record rests on, and finding it in the stream as
	// "updated organization Network" would make the one action anybody
	// audits indistinguishable from a typo fix.
	newest := f.entries(t)[0]
	if newest.Action != activity.ActionAttested {
		t.Errorf("the attestation was recorded as %q, want %q", newest.Action, activity.ActionAttested)
	}
}

func TestAuditedStore_AGrantIsDescribedByWhatItGrants(t *testing.T) {
	f := newAuditedFixture(t)
	ctx := actingAs(testActor)

	org, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	team, err := f.audited.CreateTeam(ctx, access.Team{Name: "Edge Operators", OrganizationID: org.ID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := f.audited.CreateBinding(ctx, access.Binding{
		TeamID:    team.ID,
		Role:      auth.RoleOperator,
		ScopeType: auth.ScopeOrganization,
		ScopeID:   org.ID,
		Effect:    auth.EffectAllow,
	}); err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}

	// A binding has no name of its own, so "role binding 1" would tell a
	// reader nothing about what was granted to whom. This is the one entity
	// where the description has to be composed rather than copied.
	newest := f.entries(t)[0]
	for _, want := range []string{"operator", "Edge Operators", "Network"} {
		if !strings.Contains(newest.ObjectName, want) {
			t.Errorf("the grant is described as %q, which does not say %q", newest.ObjectName, want)
		}
	}
}

func TestAuditedStore_NilCollaboratorsLeaveTheStoreUsable(t *testing.T) {
	f := newAuditedFixture(t)

	// Wiring auditing with a missing collaborator returns the undecorated
	// store rather than a value that silently drops every write, or a nil
	// that panics on first use. It is a loud warning and a working control
	// plane, not a broken one.
	if got := access.NewAuditedStore(f.raw, nil, contextActor, slog.New(slog.DiscardHandler)); got == nil {
		t.Fatal("NewAuditedStore with no recorder returned nil rather than the undecorated store")
	}
	unaudited := access.NewAuditedStore(f.raw, nil, nil, slog.New(slog.DiscardHandler))
	if _, err := unaudited.CreateOrganization(context.Background(), access.Organization{Name: "Network"}); err != nil {
		t.Errorf("the undecorated store refused a write: %v", err)
	}
}

// auditedCall performs one mutating call through the decorator.
type auditedCall struct {
	// run makes the call. It receives a fixture already carrying an
	// organization, a team, a user, a binding and a contact, so a case can
	// name only what it is about.
	run func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed)

	// wantKind and wantAction are what the resulting entry must say.
	wantKind   string
	wantAction activity.Action
}

// auditSeed is one of each entity, for the cases that need something to act
// on.
type auditSeed struct {
	orgID     int
	teamID    int
	userID    int
	bindingID int
	contactID int
}

// TestAuditedStore_EveryMutatingMethodIsAudited is the completeness guard.
//
// The table below is checked against access.Store's real method set by
// reflection, so a method added to the port later fails this test until
// somebody decides what it records. That is the guard the decorator's
// shape cannot provide on its own: every method here is written out rather
// than promoted from an embedded interface precisely so a new one breaks
// the build, but a new method someone adds *and* implements as a bare
// delegation would compile and audit nothing. This is what catches that.
func TestAuditedStore_EveryMutatingMethodIsAudited(t *testing.T) {
	calls := map[string]auditedCall{
		"CreateOrganization": {wantKind: activity.KindOrganization, wantAction: activity.ActionCreated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
				if _, err := f.audited.CreateOrganization(ctx, access.Organization{Name: "Another"}); err != nil {
					t.Fatalf("CreateOrganization: %v", err)
				}
			}},
		"UpdateOrganization": {wantKind: activity.KindOrganization, wantAction: activity.ActionUpdated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.UpdateOrganization(ctx, access.Organization{ID: seed.orgID, Name: "Renamed"}); err != nil {
					t.Fatalf("UpdateOrganization: %v", err)
				}
			}},
		"DeleteOrganization": {wantKind: activity.KindOrganization, wantAction: activity.ActionDeleted,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				// The seeded organization owns everything else, so this
				// case makes its own to delete.
				org, err := f.raw.CreateOrganization(ctx, access.Organization{Name: "Disposable"})
				if err != nil {
					t.Fatalf("seeding a second organization: %v", err)
				}
				if err := f.audited.DeleteOrganization(ctx, org.ID); err != nil {
					t.Fatalf("DeleteOrganization: %v", err)
				}
			}},
		"AttestOrganization": {wantKind: activity.KindOrganization, wantAction: activity.ActionAttested,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.AttestOrganization(ctx, seed.orgID, testActor); err != nil {
					t.Fatalf("AttestOrganization: %v", err)
				}
			}},
		"CreateTeam": {wantKind: activity.KindTeam, wantAction: activity.ActionCreated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if _, err := f.audited.CreateTeam(ctx, access.Team{Name: "Another", OrganizationID: seed.orgID}); err != nil {
					t.Fatalf("CreateTeam: %v", err)
				}
			}},
		"UpdateTeam": {wantKind: activity.KindTeam, wantAction: activity.ActionUpdated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.UpdateTeam(ctx, access.Team{ID: seed.teamID, Name: "Renamed", OrganizationID: seed.orgID}); err != nil {
					t.Fatalf("UpdateTeam: %v", err)
				}
			}},
		"DeleteTeam": {wantKind: activity.KindTeam, wantAction: activity.ActionDeleted,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.DeleteTeam(ctx, seed.teamID); err != nil {
					t.Fatalf("DeleteTeam: %v", err)
				}
			}},
		"AttestTeam": {wantKind: activity.KindTeam, wantAction: activity.ActionAttested,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.AttestTeam(ctx, seed.teamID, testActor); err != nil {
					t.Fatalf("AttestTeam: %v", err)
				}
			}},
		"CreateUser": {wantKind: activity.KindUser, wantAction: activity.ActionCreated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
				if _, err := f.audited.CreateUser(ctx, access.User{Email: "grace@example.com"}); err != nil {
					t.Fatalf("CreateUser: %v", err)
				}
			}},
		"UpdateUser": {wantKind: activity.KindUser, wantAction: activity.ActionUpdated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.UpdateUser(ctx, access.User{ID: seed.userID, Email: "renamed@example.com"}); err != nil {
					t.Fatalf("UpdateUser: %v", err)
				}
			}},
		"DeleteUser": {wantKind: activity.KindUser, wantAction: activity.ActionDeleted,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.DeleteUser(ctx, seed.userID); err != nil {
					t.Fatalf("DeleteUser: %v", err)
				}
			}},
		"CreateBinding": {wantKind: activity.KindBinding, wantAction: activity.ActionCreated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if _, err := f.audited.CreateBinding(ctx, access.Binding{
					TeamID: seed.teamID, Role: auth.RoleViewer,
					ScopeType: auth.ScopeOrganization, ScopeID: seed.orgID, Effect: auth.EffectDeny,
				}); err != nil {
					t.Fatalf("CreateBinding: %v", err)
				}
			}},
		"UpdateBinding": {wantKind: activity.KindBinding, wantAction: activity.ActionUpdated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.UpdateBinding(ctx, access.Binding{
					ID: seed.bindingID, TeamID: seed.teamID, Role: auth.RoleViewer,
					ScopeType: auth.ScopeOrganization, ScopeID: seed.orgID, Effect: auth.EffectAllow,
				}); err != nil {
					t.Fatalf("UpdateBinding: %v", err)
				}
			}},
		"DeleteBinding": {wantKind: activity.KindBinding, wantAction: activity.ActionDeleted,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.DeleteBinding(ctx, seed.bindingID); err != nil {
					t.Fatalf("DeleteBinding: %v", err)
				}
			}},
		"CreateContact": {wantKind: activity.KindContact, wantAction: activity.ActionCreated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if _, err := f.audited.CreateContact(ctx, access.Contact{
					Name: "Second Escalation", Role: access.ContactEscalation,
					Email: "oncall@example.com", OrganizationID: seed.orgID,
				}); err != nil {
					t.Fatalf("CreateContact: %v", err)
				}
			}},
		"UpdateContact": {wantKind: activity.KindContact, wantAction: activity.ActionUpdated,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.UpdateContact(ctx, access.Contact{
					ID: seed.contactID, Name: "Renamed Owner", Role: access.ContactOwner,
					Email: "owner@example.com", OrganizationID: seed.orgID,
				}); err != nil {
					t.Fatalf("UpdateContact: %v", err)
				}
			}},
		"DeleteContact": {wantKind: activity.KindContact, wantAction: activity.ActionDeleted,
			run: func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
				if err := f.audited.DeleteContact(ctx, seed.contactID); err != nil {
					t.Fatalf("DeleteContact: %v", err)
				}
			}},
	}

	// The reflection half: whatever access.Store declares is what has to be
	// covered above. A method added to the port with no case here fails
	// with the method's own name, which is the only message that leads
	// somebody straight to the decision they skipped.
	storeType := reflect.TypeOf((*access.Store)(nil)).Elem()
	for i := 0; i < storeType.NumMethod(); i++ {
		name := storeType.Method(i).Name
		if !mutatingMethod(name) {
			continue
		}
		if _, covered := calls[name]; !covered {
			t.Errorf("access.Store declares mutating method %q and no case here proves it is audited", name)
		}
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			f := newAuditedFixture(t)
			ctx := actingAs(testActor)
			seed := seedForAudit(t, f, ctx)

			before := len(f.entries(t))
			call.run(t, f, ctx, seed)
			entries := f.entries(t)

			if len(entries) != before+1 {
				t.Fatalf("%s left %d entries, want exactly one more than the %d before it",
					name, len(entries), before)
			}
			newest := entries[0]
			if newest.Actor != testActor {
				t.Errorf("%s recorded actor %q, want %q", name, newest.Actor, testActor)
			}
			if newest.ObjectKind != call.wantKind {
				t.Errorf("%s recorded kind %q, want %q", name, newest.ObjectKind, call.wantKind)
			}
			if newest.Action != call.wantAction {
				t.Errorf("%s recorded action %q, want %q", name, newest.Action, call.wantAction)
			}
			if newest.ObjectID <= 0 {
				t.Errorf("%s recorded object id %d, which identifies nothing", name, newest.ObjectID)
			}
			if strings.TrimSpace(newest.ObjectName) == "" {
				t.Errorf("%s recorded no object name, so the entry reads as a bare primary key", name)
			}
		})
	}
}

// mutatingMethod reports whether a port method name is a write.
func mutatingMethod(name string) bool {
	for _, prefix := range []string{"Create", "Update", "Delete", "Attest"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// seedForAudit creates one of each entity through the *raw* store, so the
// stream starts empty and every entry a case produces is its own.
func seedForAudit(t *testing.T, f auditedFixture, ctx context.Context) auditSeed {
	t.Helper()

	org, err := f.raw.CreateOrganization(ctx, access.Organization{Name: "Network"})
	if err != nil {
		t.Fatalf("seeding organization: %v", err)
	}
	team, err := f.raw.CreateTeam(ctx, access.Team{Name: "Edge Operators", OrganizationID: org.ID})
	if err != nil {
		t.Fatalf("seeding team: %v", err)
	}
	user, err := f.raw.CreateUser(ctx, access.User{Email: "kay@example.com", TeamIDs: []int{team.ID}})
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	binding, err := f.raw.CreateBinding(ctx, access.Binding{
		TeamID: team.ID, Role: auth.RoleOperator,
		ScopeType: auth.ScopeOrganization, ScopeID: org.ID, Effect: auth.EffectAllow,
	})
	if err != nil {
		t.Fatalf("seeding binding: %v", err)
	}
	contact, err := f.raw.CreateContact(ctx, access.Contact{
		Name: "Primary Owner", Role: access.ContactOwner,
		Email: "owner@example.com", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("seeding contact: %v", err)
	}

	if entries := f.entries(t); len(entries) != 0 {
		t.Fatalf("seeding through the raw store left %d entries in the stream", len(entries))
	}
	return auditSeed{orgID: org.ID, teamID: team.ID, userID: user.ID, bindingID: binding.ID, contactID: contact.ID}
}

// TestAuditedStore_EveryReadStillReaches is the other half of the
// completeness guard.
//
// Every method here is written out by hand rather than promoted from an
// embedded interface, which is what makes a new method on Store break the
// build. The cost of that choice is ten delegating one-liners, and a
// delegating one-liner is exactly the kind of code a transposed field name
// hides in. This calls every read through the decorator and asserts it
// returns what the raw store does, and checks the table against Store's
// real method set so a read added later is covered too.
func TestAuditedStore_EveryReadStillReaches(t *testing.T) {
	reads := map[string]func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed){
		"GetOrganization": func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
			got, err := f.audited.GetOrganization(ctx, seed.orgID)
			if err != nil || got.ID != seed.orgID || got.Name != "Network" {
				t.Errorf("GetOrganization = (%+v, %v), want the seeded organization", got, err)
			}
		},
		"ListOrganizations": func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
			got, err := f.audited.ListOrganizations(ctx, access.Query{})
			if err != nil || len(got) != 1 {
				t.Errorf("ListOrganizations = (%d records, %v), want the 1 seeded", len(got), err)
			}
		},
		"GetTeam": func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
			got, err := f.audited.GetTeam(ctx, seed.teamID)
			if err != nil || got.ID != seed.teamID || got.Name != "Edge Operators" {
				t.Errorf("GetTeam = (%+v, %v), want the seeded team", got, err)
			}
		},
		"ListTeams": func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
			got, err := f.audited.ListTeams(ctx, access.TeamQuery{})
			if err != nil || len(got) != 1 {
				t.Errorf("ListTeams = (%d records, %v), want the 1 seeded", len(got), err)
			}
		},
		"GetUser": func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
			got, err := f.audited.GetUser(ctx, seed.userID)
			if err != nil || got.ID != seed.userID || got.Email != "kay@example.com" {
				t.Errorf("GetUser = (%+v, %v), want the seeded user", got, err)
			}
		},
		"ListUsers": func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
			got, err := f.audited.ListUsers(ctx, access.Query{})
			if err != nil || len(got) != 1 {
				t.Errorf("ListUsers = (%d records, %v), want the 1 seeded", len(got), err)
			}
		},
		"GetBinding": func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
			got, err := f.audited.GetBinding(ctx, seed.bindingID)
			if err != nil || got.ID != seed.bindingID || got.TeamName != "Edge Operators" {
				t.Errorf("GetBinding = (%+v, %v), want the seeded grant", got, err)
			}
		},
		"ListBindings": func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
			got, err := f.audited.ListBindings(ctx, access.BindingQuery{})
			if err != nil || len(got) != 1 {
				t.Errorf("ListBindings = (%d records, %v), want the 1 seeded", len(got), err)
			}
		},
		"GetContact": func(t *testing.T, f auditedFixture, ctx context.Context, seed auditSeed) {
			got, err := f.audited.GetContact(ctx, seed.contactID)
			if err != nil || got.ID != seed.contactID || got.Name != "Primary Owner" {
				t.Errorf("GetContact = (%+v, %v), want the seeded contact", got, err)
			}
		},
		"ListContacts": func(t *testing.T, f auditedFixture, ctx context.Context, _ auditSeed) {
			got, err := f.audited.ListContacts(ctx, access.ContactQuery{})
			if err != nil || len(got) != 1 {
				t.Errorf("ListContacts = (%d records, %v), want the 1 seeded", len(got), err)
			}
		},
	}

	storeType := reflect.TypeOf((*access.Store)(nil)).Elem()
	for i := 0; i < storeType.NumMethod(); i++ {
		name := storeType.Method(i).Name
		if mutatingMethod(name) {
			continue
		}
		if _, covered := reads[name]; !covered {
			t.Errorf("access.Store declares read method %q and no case here proves it still reaches the store", name)
		}
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			f := newAuditedFixture(t)
			ctx := actingAs(testActor)
			seed := seedForAudit(t, f, ctx)

			read(t, f, ctx, seed)

			// A read that recorded would turn the stream into a request log.
			if entries := f.entries(t); len(entries) != 0 {
				t.Errorf("%s left %d entries in the stream", name, len(entries))
			}
		})
	}
}

// TestAuditedStore_NoMutationRecordsWhenTheWriteFails covers the refusal
// branch of every mutating method.
//
// One assertion, seventeen times, and it is the one that matters most: an
// audit trail that records attempts rather than changes is worse than none,
// because every entry in it has to be independently checked against reality
// before it can be believed.
func TestAuditedStore_NoMutationRecordsWhenTheWriteFails(t *testing.T) {
	failures := mutatingCalls()
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			f := newAuditedFixture(t)
			ctx := actingAs(testActor)

			if err := fail(f, ctx); err == nil {
				t.Fatalf("%s was expected to fail and did not, so this case proves nothing", name)
			}
			if entries := f.entries(t); len(entries) != 0 {
				t.Errorf("%s failed and still recorded %d entries", name, len(entries))
			}
		})
	}

	// The same reflection check: a mutating method with no failure case
	// here has an untested refusal branch.
	storeTypeF := reflect.TypeOf((*access.Store)(nil)).Elem()
	for i := 0; i < storeTypeF.NumMethod(); i++ {
		name := storeTypeF.Method(i).Name
		if !mutatingMethod(name) {
			continue
		}
		if _, covered := failures[name]; !covered {
			t.Errorf("access.Store declares mutating method %q and no case here proves a failed write records nothing", name)
		}
	}
}

// TestAuditedStore_NoMutatingMethodRunsUnattributed asserts the refusal
// reaches every write, not only the one it was first written for.
//
// The check sits at the top of each method, before the store is touched, so
// this is also the assertion that the ordering is right everywhere: a
// refusal placed after the write would leave the change made and the caller
// told it failed.
func TestAuditedStore_NoMutatingMethodRunsUnattributed(t *testing.T) {
	for name, call := range mutatingCalls() {
		t.Run(name, func(t *testing.T) {
			f := newAuditedFixture(t)

			// No actor in the context at all.
			if err := call(f, context.Background()); !errors.Is(err, access.ErrUnattributed) {
				t.Errorf("%s with no actor returned %v, want ErrUnattributed", name, err)
			}
			if entries := f.entries(t); len(entries) != 0 {
				t.Errorf("%s refused an unattributed write and still recorded %d entries", name, len(entries))
			}
		})
	}
}

// mutatingCalls is one call per mutating method of access.Store, each
// arranged so the underlying write would fail if it were reached.
//
// Shared by the two tests above because they assert different things about
// the same seventeen calls: one that a failed write records nothing, one
// that an unattributed write never reaches the store at all. Written once
// so the two cannot drift into covering different subsets.
func mutatingCalls() map[string]func(f auditedFixture, ctx context.Context) error {
	// An id no record has. Update, Delete and Attest all resolve a record
	// first, so this is a real store refusal rather than a simulated one.
	const missing = 999_999

	return map[string]func(f auditedFixture, ctx context.Context) error{
		"CreateOrganization": func(f auditedFixture, ctx context.Context) error {
			_, err := f.audited.CreateOrganization(ctx, access.Organization{Name: ""})
			return err
		},
		"UpdateOrganization": func(f auditedFixture, ctx context.Context) error {
			return f.audited.UpdateOrganization(ctx, access.Organization{ID: missing, Name: "Ghost"})
		},
		"DeleteOrganization": func(f auditedFixture, ctx context.Context) error {
			return f.audited.DeleteOrganization(ctx, missing)
		},
		"AttestOrganization": func(f auditedFixture, ctx context.Context) error {
			return f.audited.AttestOrganization(ctx, missing, testActor)
		},
		"CreateTeam": func(f auditedFixture, ctx context.Context) error {
			_, err := f.audited.CreateTeam(ctx, access.Team{Name: "Orphan", OrganizationID: missing})
			return err
		},
		"UpdateTeam": func(f auditedFixture, ctx context.Context) error {
			return f.audited.UpdateTeam(ctx, access.Team{ID: missing, Name: "Ghost", OrganizationID: missing})
		},
		"DeleteTeam": func(f auditedFixture, ctx context.Context) error {
			return f.audited.DeleteTeam(ctx, missing)
		},
		"AttestTeam": func(f auditedFixture, ctx context.Context) error {
			return f.audited.AttestTeam(ctx, missing, testActor)
		},
		"CreateUser": func(f auditedFixture, ctx context.Context) error {
			_, err := f.audited.CreateUser(ctx, access.User{Email: ""})
			return err
		},
		"UpdateUser": func(f auditedFixture, ctx context.Context) error {
			return f.audited.UpdateUser(ctx, access.User{ID: missing, Email: "ghost@example.com"})
		},
		"DeleteUser": func(f auditedFixture, ctx context.Context) error {
			return f.audited.DeleteUser(ctx, missing)
		},
		"CreateBinding": func(f auditedFixture, ctx context.Context) error {
			_, err := f.audited.CreateBinding(ctx, access.Binding{TeamID: missing})
			return err
		},
		"UpdateBinding": func(f auditedFixture, ctx context.Context) error {
			return f.audited.UpdateBinding(ctx, access.Binding{ID: missing, Role: auth.RoleViewer, Effect: auth.EffectAllow})
		},
		"DeleteBinding": func(f auditedFixture, ctx context.Context) error {
			return f.audited.DeleteBinding(ctx, missing)
		},
		"CreateContact": func(f auditedFixture, ctx context.Context) error {
			_, err := f.audited.CreateContact(ctx, access.Contact{Name: ""})
			return err
		},
		"UpdateContact": func(f auditedFixture, ctx context.Context) error {
			return f.audited.UpdateContact(ctx, access.Contact{ID: missing, Name: "Ghost",
				Role: access.ContactOwner, Email: "ghost@example.com", OrganizationID: missing})
		},
		"DeleteContact": func(f auditedFixture, ctx context.Context) error {
			return f.audited.DeleteContact(ctx, missing)
		},
	}

}
