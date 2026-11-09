// Package resources_test's fake project store coverage: the fake behaves
// like the store it stands for where a test depends on it, as LESSONS 424
// asks, and an id is the one place it used to differ.
package resources_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestFakeProjectStore_NeverHandsOutAnIDTwice deletes a project between two
// creates. Ids used to be the slice's length plus one, and Delete shrinks the
// slice, so the third project got the second one's id and a Get by that id
// answered with whichever came first.
func TestFakeProjectStore_NeverHandsOutAnIDTwice(t *testing.T) {
	ctx := context.Background()
	store := newFakeProjectStore()

	first, err := store.Create(ctx, project.Project{Name: "first", OrganizationID: 1, SCMType: project.SCMManual})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(ctx, project.Project{Name: "second", OrganizationID: 1, SCMType: project.SCMManual})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	third := store.seedRaw(project.Project{Name: "third", OrganizationID: 1, SCMType: project.SCMManual})

	seen := map[int]string{}
	for _, p := range []project.Project{second, third} {
		if other, taken := seen[p.ID]; taken {
			t.Fatalf("id %d was handed to both %q and %q", p.ID, other, p.Name)
		}
		seen[p.ID] = p.Name
	}
	if got, _ := store.Get(ctx, third.ID); got.Name != "third" {
		t.Errorf("Get(%d) = %q, want third", third.ID, got.Name)
	}
}
