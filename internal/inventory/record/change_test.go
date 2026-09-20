// Package record_test: tests of ChangeState and ChangeTags.
package record_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestChangeStateAndTags covers the two mutators a device update uses:
// a real change bumps the version once and records one revision holding
// the old and new values, while setting what a device already has (the
// same state, or the same tags in another order) changes nothing and
// records nothing, so a no-op update leaves the audit trail alone.
func TestChangeStateAndTags(t *testing.T) {
	b := record.NewBase(record.Record{
		ID: "d1", Name: "web1", Type: "linux_server",
		State: inventory.StateQuarantined, Tags: []inventory.Tag{"web", "prod"}, Version: 3,
	}, nil)

	if b.ChangeState(inventory.StateQuarantined) || b.ChangeTags([]inventory.Tag{"prod", "web"}) {
		t.Fatal("setting the state and tags a device already has reported a change")
	}
	if b.Version() != 3 || len(b.History()) != 0 {
		t.Fatalf("a no-op change left version %d and history %v", b.Version(), b.History())
	}

	if !b.ChangeState(inventory.StateActive) {
		t.Fatal("a state change reported no change")
	}
	if !b.ChangeTags([]inventory.Tag{"web"}) {
		t.Fatal("a tag change reported no change")
	}
	if b.State() != inventory.StateActive || !reflect.DeepEqual(b.Tags(), []inventory.Tag{"web"}) || b.Version() != 5 {
		t.Fatalf("after both changes: state %s, tags %v, version %d", b.State(), b.Tags(), b.Version())
	}
	want := []inventory.Revision{
		{Version: 4, Field: record.RevisionFieldState, OldValue: "quarantined", NewValue: "active"},
		{Version: 5, Field: record.RevisionFieldTags, OldValue: []string{"web", "prod"}, NewValue: []string{"web"}},
	}
	got := b.History()
	for i := range got {
		got[i].ChangedAt = want[i].ChangedAt
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history = %+v, want %+v", got, want)
	}
	if b.BaseVersion() != 3 {
		t.Errorf("BaseVersion moved to %d; a repository needs the loaded version to detect a lost update", b.BaseVersion())
	}
}
