// Package instancegroups is the Instance Groups view, registered as declared.
//
// Runner affinity, and it is declared together with capacity
// accounting rather than before it on purpose: a group that cannot say
// how much work it can take is a label, and the reason to pin work to a
// group is usually that the group is a scarce resource.
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
package instancegroups

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "instance-groups"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "kind", Label: "KIND", Kind: view.KindBadge, InList: true},
	{Name: "capacity", Label: "CAPACITY", Kind: view.KindText, InList: true},
	{Name: "running", Label: "RUNNING", Kind: view.KindText, InList: true},
}

// Register adds the declared Instance Groups view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Instance Groups",
		NavLabel: "INSTANCE GROUPS",
		NavOrder: 150,
		NavGroup: view.NavGroupAdministration,
		Summary:  "Which runners execute which work.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}
