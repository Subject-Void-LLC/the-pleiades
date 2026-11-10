// This file holds the UI's affordances to every scope position an endpoint
// declares: a control whose endpoint needs two further scopes (relaunching a
// job needs job:read and template:read beside runbook:execute) is offered
// only to a session holding all three. The generator reads two affordances
// for one relation in one list as either, so the handler asks once per
// position; a handler that asked only the first position would offer
// Relaunch to a session missing template:read.
package web

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestAffordances_RequireEveryFurtherScope drops each of a two-further-scope
// endpoint's scopes in turn and requires the control to be withheld, and
// offered when all are held.
func TestAffordances_RequireEveryFurtherScope(t *testing.T) {
	issuer := authtest.New(t, "ui-also", "ui-also")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}
	h := New(Config{HATEOAS: generator, Admission: auth.Admission{Chain: chain}})
	if len(apispec.RelaunchJob.AlsoScopes) != 2 {
		t.Fatalf("RelaunchJob.AlsoScopes = %v, want two further scopes for this test to mean anything", apispec.RelaunchJob.AlsoScopes)
	}
	d := view.Descriptor{Name: "probe", Actions: []view.RecordAction{
		{Name: "relaunch", Label: "Relaunch", Endpoint: &apispec.RelaunchJob},
	}}
	rel := apispec.RelaunchJob.Rel
	all := append([]auth.Scope{apispec.RelaunchJob.Scope}, apispec.RelaunchJob.AlsoScopes...)

	offered := func(scopes []auth.Scope) bool {
		id := &auth.Identity{Subject: "probe", Role: auth.RoleOperator, Scopes: scopes}
		return h.affordances(t.Context(), id, d).Can(rel)
	}
	for skip := range all {
		held := append(append([]auth.Scope(nil), all[:skip]...), all[skip+1:]...)
		if offered(held) {
			t.Errorf("Relaunch offered without %s (holding %v)", all[skip], held)
		}
	}
	if !offered(all) {
		t.Errorf("Relaunch withheld from a session holding %v", all)
	}
}
