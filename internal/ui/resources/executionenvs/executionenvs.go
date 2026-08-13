// Package executionenvs is the Execution Environments view, registered as declared.
//
// Today the Runner takes one image from ANSIBLE_RUNNER_IMAGE in its
// composition root, which is exactly the fixed string this entity
// replaces with a record: named, organization-scoped, with a pull policy
// and an optional registry credential.
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
package executionenvs

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "execution-environments"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "image", Label: "IMAGE", Kind: view.KindText, InList: true},
	{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true},
	{Name: "pull", Label: "PULL POLICY", Kind: view.KindBadge, InList: true},
}

// Register adds the declared Execution Environments view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Execution Environments",
		NavLabel: "EXECUTION ENVIRONMENTS",
		NavOrder: 135,
		NavGroup: view.NavGroupAdministration,
		Summary:  "The container image a job runs inside.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}
