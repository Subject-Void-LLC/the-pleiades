package ent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// FuzzGroupCreation fuzzes Group.name with adversarial strings, matching
// FuzzDeviceCreation's shape: the claim under test is that no name ever
// causes a panic, never that every name succeeds.
func FuzzGroupCreation(f *testing.F) {
	f.Add("prod")
	f.Add("")
	f.Add(strings.Repeat("long-group-name-", 100))
	f.Add("xSS <script>alert(1)</script>")
	f.Add("'; DROP TABLE groups; --")
	f.Add("unicode-é中文-\U0001F600")

	f.Fuzz(func(t *testing.T, name string) {
		client := enttest.Open(t, "sqlite3", "file:groupfuzz?mode=memory&cache=shared&_fk=1")
		defer client.Close()
		ctx := context.Background()

		// The test passes as long as there is no panic; errors (empty
		// name, duplicate name on a second call with the same fuzzed
		// value) are an expected, gracefully handled outcome.
		if _, err := client.Group.Create().SetName(name).Save(ctx); err != nil {
			t.Logf("expected error handled gracefully: %v", err)
		}
	})
}

// FuzzOrganizationCreation is FuzzGroupCreation's twin for Organization.name.
func FuzzOrganizationCreation(f *testing.F) {
	f.Add("acme-corp")
	f.Add("")
	f.Add(strings.Repeat("long-org-name-", 100))
	f.Add("xSS <script>alert(1)</script>")
	f.Add("'; DROP TABLE organizations; --")
	f.Add("unicode-é中文-\U0001F600")

	f.Fuzz(func(t *testing.T, name string) {
		client := enttest.Open(t, "sqlite3", "file:orgfuzz?mode=memory&cache=shared&_fk=1")
		defer client.Close()
		ctx := context.Background()

		if _, err := client.Organization.Create().SetName(name).Save(ctx); err != nil {
			t.Logf("expected error handled gracefully: %v", err)
		}
	})
}
