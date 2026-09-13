// Package projects is the Projects view, registered as declared.
//
// The keystone relationship a template has and the one this platform is
// missing most visibly: a template's playbook is chosen from a project's
// synced tree, and a project belongs to an organization, which is what
// makes a content catalog tenanted rather than deployment-wide.
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
package projects

import "github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"

// Name is this view's registration key and URL segment.
const Name = "projects"

// fields declare the shape a real implementation fills. They render
// nothing today; they exist so the contract is written down where the
// implementation will need it rather than in a document beside it.
var fields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true},
	{Name: "scm_type", Label: "TYPE", Kind: view.KindBadge, InList: true},
	{Name: "scm_url", Label: "SOURCE", Kind: view.KindText, InList: true},
	{Name: "revision", Label: "REVISION", Kind: view.KindText, InList: true},
	{Name: "status", Label: "LAST SYNC", Kind: view.KindBadge, InList: true},
}

// Register adds the declared Projects view. It takes no dependencies
// because it has none: there is no port to adapt yet.
func Register() error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Projects",
		NavLabel: "PROJECTS",
		NavOrder: 55,
		NavGroup: view.NavGroupResources,
		Summary:  "Where automation content comes from: a synced repository or a directory.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields:   fields,
		// AWX's project tabs, declared in full. A project is the keystone
		// relationship this platform is missing, so the shape of what it
		// will carry is worth writing down where the implementation will
		// need it rather than in a document beside it.
		Sections: []view.Section{
			view.Planned("Access",
				"The role bindings that reach this project's content.",
				"Access has no project-scoped binding yet: auth.ScopeType has system, organization, inventory, group and device, and no project.",
				[]view.Field{
					{Name: "team", Label: "TEAM", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
					{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
					{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true},
				}),
			view.Planned("Templates",
				"The templates that run content from this project.",
				"A template names a definition from the catalog rather than a project, so nothing records which project a definition came from yet.",
				[]view.Field{
					{Name: "template", Label: "TEMPLATE", Kind: view.KindText, InList: true, MobilePrimary: true, References: "templates"},
					{Name: "definition", Label: "RUNS", Kind: view.KindText, InList: true},
					{Name: "last_ran", Label: "LAST RAN", Kind: view.KindText, InList: true},
				}),
			view.Planned("Schedules",
				"When this project re-syncs from its source.",
				"A schedule attaches to a template, which is the only Launchable kind there is. A project sync is not one yet.",
				[]view.Field{
					{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "schedules"},
					{Name: "rrule", Label: "RECURRENCE", Kind: view.KindText, InList: true},
					{Name: "next_run", Label: "NEXT RUN", Kind: view.KindText, InList: true},
					{Name: "enabled", Label: "ENABLED", Kind: view.KindBadge, InList: true},
				}),
			view.Planned("Notifications",
				"Who is told when a sync from this project succeeds or fails.",
				"Notification policies have no backing entity in this build. The Notification Engine owns them and nothing here has a port to it.",
				[]view.Field{
					{Name: "target", Label: "TARGET", Kind: view.KindText, InList: true, MobilePrimary: true},
					{Name: "on", Label: "ON", Kind: view.KindText, InList: true},
				}),
		},
	})
}
