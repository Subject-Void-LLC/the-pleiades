// This file holds Descriptor.AlsoCandidates to its contract: every endpoint
// a descriptor reaches that declares an AlsoScope (an operation, a record
// action or a section's row action) is offered under that second scope, and
// nothing else is, so the UI can require both scopes as the API router does.
package view_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestDescriptor_AlsoCandidatesOffersEverySecondScope builds a descriptor
// whose update operation, record action and row action each declare a read
// scope beside their write scope, and one action without an endpoint, and
// requires exactly those three under their AlsoScope.
func TestDescriptor_AlsoCandidatesOffersEverySecondScope(t *testing.T) {
	d := rowActionDescriptor()
	d.Ops.Update = &apispec.UpdateInventory
	d.Actions = []view.RecordAction{
		{Name: "copy", Label: "Copy", Endpoint: &apispec.CopyTemplate},
		{Name: "nothing", Label: "Nothing"},
	}

	// pair is an affordance's relation and scope, the two fields that
	// decide what the UI asks the generator.
	type pair struct {
		rel   auth.LinkRel
		scope auth.Scope
	}
	var got []pair
	for _, a := range d.AlsoCandidates() {
		got = append(got, pair{a.Rel, a.Scope})
	}
	want := []pair{
		{apispec.UpdateInventory.Rel, auth.ScopeInventoryRead},
		{apispec.CopyTemplate.Rel, auth.ScopeTemplateRead},
		{apispec.SetCredentialTypeInputs.Rel, auth.ScopeCredentialRead},
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("AlsoCandidates() = %v, missing %v", got, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("AlsoCandidates() offered %v, which no endpoint here declares as a second scope", g)
		}
	}
}
