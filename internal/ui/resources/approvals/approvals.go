// Package approvals is the Workflow Approvals view, registered as declared.
//
// An approval node halts a workflow graph until an authorised person
// approves or denies it, with an optional timeout. It needs a workflow
// engine with pause and resume, which internal/engine does not have, so
// this is the furthest of the declared views from being real.
//
// Declared rather than omitted, for the reason governance.go gives: the
// navigation is a statement about the shape of the product, and an AWX
// operator evaluating this platform reads the sidebar before anything
// else. Declared rather than faked, because a view rendering an empty
// table over nothing is indistinguishable from a working view with no
// records, which is the ambiguity this project has shipped twice.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package approvals

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "workflow-approvals"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "workflow", Label: "WORKFLOW", Kind: view.KindText, InList: true},
	{Name: "requested_by", Label: "REQUESTED BY", Kind: view.KindText, InList: true},
	{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true},
	{Name: "expires", Label: "EXPIRES", Kind: view.KindTimestamp, InList: true},
}

// Register adds the declared Workflow Approvals view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Workflow Approvals",
		NavLabel: "WORKFLOW APPROVALS",
		NavOrder: 35,
		NavGroup: view.NavGroupViews,
		Summary:  "Runs paused until a person decides.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}
