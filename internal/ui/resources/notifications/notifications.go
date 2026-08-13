// Package notifications is the Notifications view, registered as declared.
//
// Bound per trigger point (started, success, error) to anything
// launchable, with a templated message, so it waits on the same
// Launchable abstraction schedules do and reuses the renderer the
// credential injectors need.
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
package notifications

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "notifications"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "type", Label: "TYPE", Kind: view.KindBadge, InList: true},
	{Name: "target", Label: "TARGET", Kind: view.KindText, InList: true},
	{Name: "triggers", Label: "TRIGGERS", Kind: view.KindText, InList: true},
}

// Register adds the declared Notifications view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Notifications",
		NavLabel: "NOTIFICATIONS",
		NavOrder: 145,
		NavGroup: view.NavGroupAdministration,
		Summary:  "How people find out a job started, succeeded or failed.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}
