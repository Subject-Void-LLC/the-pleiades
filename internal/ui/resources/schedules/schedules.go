// Package schedules is the Schedules view, registered as declared.
//
// An RFC5545 recurrence attached to anything launchable, which is why it
// waits on the Launchable abstraction rather than only on a parser: a
// schedule must attach to a template, a project sync or a workflow with
// one mechanism, or every kind grows its own scheduler.
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
package schedules

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "schedules"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "runs", Label: "RUNS", Kind: view.KindText, InList: true},
	{Name: "rrule", Label: "RECURRENCE", Kind: view.KindText, InList: true},
	{Name: "timezone", Label: "TIMEZONE", Kind: view.KindText, InList: true},
	{Name: "next_run", Label: "NEXT RUN", Kind: view.KindTimestamp, InList: true},
	{Name: "enabled", Label: "ENABLED", Kind: view.KindBadge, InList: true},
}

// Register adds the declared Schedules view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Schedules",
		NavLabel: "SCHEDULES",
		NavOrder: 25,
		NavGroup: view.NavGroupViews,
		Summary:  "When automation runs without somebody pressing launch.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
	})
}
