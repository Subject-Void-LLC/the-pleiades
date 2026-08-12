// Package governance is the Governance view, registered as declared.
//
// It is registered rather than omitted because the shape is real and the
// navigation should say so: policy, promotion gates and approval flows are
// Run-tier concepts this platform intends to have. It is declared rather
// than implemented because nothing backs it -- no ent schema, no package,
// no port, no endpoint -- and a view that rendered an empty table over
// nothing would be indistinguishable from a working view with no records
// yet. That ambiguity is the failure this project has shipped twice
// already, which is why view.Register refuses a declared descriptor that
// carries handlers.
//
// The whole cost of a declared view is this file. The shared template
// renders the honest panel from Status alone.
package governance

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "governance"

// fields declare the shape a real implementation would fill. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "policy", Label: "POLICY", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "scope", Label: "SCOPE", Kind: view.KindText, InList: true},
	{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true},
}

// Register adds the declared Governance view.
//
// It takes no dependencies, because it has none: there is no port to
// adapt. When one exists, this signature grows and the descriptor gains
// handlers and a Status of implemented -- and nothing else in the UI
// changes.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Governance",
		NavLabel: "GOVERNANCE",
		NavOrder: 130,
		NavGroup: view.NavGroupAdministration,
		Summary:  "Policy, promotion gates and approvals.",
		Status:   view.StatusDeclared,
		IDField:  "policy",
		Fields:   fields,
		// No Ops. There is no endpoint to name, and naming one that does
		// not exist is precisely what Register refuses.
	})
}
