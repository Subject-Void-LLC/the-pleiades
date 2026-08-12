package resources_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// newTestAccessStore builds a real access store over an in-memory database,
// seeded with two of each entity, plus the activity stream it writes to.
//
// The returned store is the *audited* one, so every write assertion in this
// suite runs through the decorator rather than around it. The seeding below
// goes through the raw store underneath it, for two reasons: seeding
// happens outside any request and so carries no identity for the decorator
// to attribute it to, and starting the stream empty means an assertion
// about what a write recorded is not reading somebody else's fixture.
//
// Seeded rather than empty, because several conformance assertions open the
// first record of a view and a view with no rows would pass them by having
// nothing to get wrong.
//
// Two rather than one, because one is the count at which a paging assertion
// cannot fail. A single record means no page can have a page after it, so
// every reader's truncation branch goes unrun and the control that renders
// a next page is never rendered at all. Two is the smallest number that
// makes a second page reachable at limit=1, which is what the paging
// conformance assertion needs to be about anything.
func newTestAccessStore(t *testing.T) (access.Store, activity.Store) {
	t.Helper()
	// No t.Cleanup, deliberately. The view registry is process-wide and
	// registered exactly once, so the ports it captures outlive whichever
	// test happened to trigger that registration. Closing this with the
	// first test's cleanup left every later test reading a closed database,
	// which surfaced as every access view answering 500. The in-memory
	// database is released when the process exits.
	client := enttest.Open(t, "sqlite3", "file:uiaccessfixture?mode=memory&cache=shared&_fk=1")

	// Wrapped in the audited store, exactly as the composition root wraps
	// it. That makes every write assertion in this suite -- create, edit,
	// delete, on four views -- run through the decorator rather than around
	// it, which is the surface the decorator exists to cover and the one a
	// handler-side recording call would have missed.
	stream := activity.NewEntStore(client)
	raw := access.NewEntStore(client)
	store := access.NewAuditedStore(raw, stream, uiActor, slog.New(slog.DiscardHandler))
	ctx := context.Background()

	for _, suffix := range []string{"", "-second"} {
		org, err := raw.CreateOrganization(ctx, access.Organization{Name: "conformance" + suffix})
		if err != nil {
			t.Fatalf("seeding an organization: %v", err)
		}
		user, err := raw.CreateUser(ctx, access.User{Email: "conformance" + suffix + "@example.com"})
		if err != nil {
			t.Fatalf("seeding a user: %v", err)
		}
		team, err := raw.CreateTeam(ctx, access.Team{
			Name: "conformance-team" + suffix, OrganizationID: org.ID, UserIDs: []int{user.ID},
		})
		if err != nil {
			t.Fatalf("seeding a team: %v", err)
		}
		// Both are system-scope Allows so that either team is deletable
		// without the last-grant guard refusing, which several assertions
		// depend on.
		if _, err := raw.CreateBinding(ctx, access.Binding{
			TeamID: team.ID, Role: auth.RoleAdmin, ScopeType: auth.ScopeSystem, Effect: auth.EffectAllow,
		}); err != nil {
			t.Fatalf("seeding a grant: %v", err)
		}

		// A grant naming this organization directly, which is what its own
		// Access section renders, plus one at device scope carrying the
		// same id. The pair is the point: scope_id has no foreign key and
		// its values are per level, so a section that matched the id
		// without the level would show the device grant on the
		// organization's page.
		for _, scope := range []auth.ScopeType{auth.ScopeOrganization, auth.ScopeDevice} {
			if _, err := raw.CreateBinding(ctx, access.Binding{
				TeamID: team.ID, Role: auth.RoleOperator, ScopeType: scope,
				ScopeID: org.ID, Effect: auth.EffectAllow,
			}); err != nil {
				t.Fatalf("seeding a %s grant: %v", scope, err)
			}
		}
	}
	return store, stream
}

// uiActor is the ActorSource the UI runs with: the authenticated identity
// the session middleware placed in the request context. It is the same
// closure the Controller's composition root installs, so this suite is
// exercising the real attribution path rather than a stand-in.
func uiActor(ctx context.Context) string {
	if identity, ok := api.IdentityFromContext(ctx); ok {
		return identity.Subject
	}
	return ""
}
