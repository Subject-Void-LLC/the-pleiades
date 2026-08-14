package resources

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/approvals"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/contacts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/credentials"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/credentialtypes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/dashboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/devices"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/executionenvs"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/governance"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/grants"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/instancegroups"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/inventories"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/jobs"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/labels"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/notifications"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/organizations"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/projects"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/runbooks"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/schedules"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/teams"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/templates"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/users"
)

// registrars is the list every built-in view appears in exactly once.
//
// It is a real import rather than a blank one because these views register
// through a function that needs a live port. The hazard the package doc
// describes is unchanged: a view package absent from this list is a view
// package the binary never reaches, however complete and well-tested it
// is.
//
// Order here does not matter -- the sidebar sorts on NavOrder, and
// Register refuses a duplicate name whichever order it arrives in -- so
// they are listed alphabetically to keep the diff of adding one small.
func registrars() []Registrar {
	return []Registrar{
		func(d Deps) error { return activity.Register(d.Activity) },
		func(Deps) error { return approvals.Register() },
		func(d Deps) error { return contacts.Register(d.Access) },
		func(d Deps) error { return credentials.Register(d.Credentials) },
		func(d Deps) error { return credentialtypes.Register(d.Credentials, d.Render) },
		func(d Deps) error { return dashboard.Register(d.Jobs, d.Announce) },
		func(d Deps) error { return devices.Register(d.Inventory, d.Factory) },
		func(Deps) error { return executionenvs.Register() },
		func(Deps) error { return governance.Register() },
		func(d Deps) error { return grants.Register(d.Access) },
		func(Deps) error { return instancegroups.Register() },
		func(d Deps) error { return inventories.Register(d.Sets) },
		func(d Deps) error { return jobs.Register(d.Jobs) },
		func(Deps) error { return labels.Register() },
		func(Deps) error { return notifications.Register() },
		func(d Deps) error { return organizations.Register(d.Access) },
		func(Deps) error { return projects.Register() },
		func(d Deps) error { return runbooks.Register(d.Runbooks, d.Templates, d.Sets) },
		func(Deps) error { return schedules.Register() },
		func(d Deps) error { return teams.Register(d.Access) },
		func(d Deps) error {
			return templates.Register(d.Templates, d.Sets, d.Jobs, d.Dispatcher, d.Access, d.Catalog, d.Credentials)
		},
		func(d Deps) error { return users.Register(d.Access) },
	}
}
