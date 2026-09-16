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
		func(d Deps) error { return credentials.Register(d.Credentials, d.Sets) },
		func(d Deps) error { return credentialtypes.Register(d.Credentials, d.Sets, d.Render) },
		func(d Deps) error { return dashboard.Register(d.Jobs, d.Announce) },
		func(d Deps) error { return devices.Register(d.Inventory, d.Factory) },
		func(Deps) error { return executionenvs.Register() },
		func(Deps) error { return governance.Register() },
		func(d Deps) error { return grants.Register(d.Access) },
		func(Deps) error { return instancegroups.Register() },
		func(d Deps) error { return inventories.Register(d.Sets, d.Access) },
		func(d Deps) error {
			return jobs.Register(d.Jobs, jobRelauncher(d), jobCanceller(d), jobJournal(d), jobLogArchive(d))
		},
		func(Deps) error { return labels.Register() },
		func(Deps) error { return notifications.Register() },
		func(d Deps) error { return organizations.Register(d.Access) },
		func(d Deps) error {
			return projects.Register(d.Projects, d.ProjectSync, d.ProjectRunner, d.Sets, d.Credentials)
		},
		func(d Deps) error { return runbooks.Register(d.Runbooks, d.Templates, d.Sets) },
		func(d Deps) error { return schedules.Register(d.Schedules, d.Templates) },
		func(d Deps) error { return teams.Register(d.Access) },
		func(d Deps) error {
			return templates.Register(d.Templates, d.Sets, d.Jobs, d.Dispatcher, d.Access, d.Catalog, d.Credentials, d.Schedules)
		},
		func(d Deps) error { return users.Register(d.Access) },
	}
}

// jobCanceller hands the Jobs view the one path a cancel takes, or an
// untyped nil, by the identical conversion jobRelauncher below performs and
// for the identical reason.
//
// It is Deps.JobCanceller rather than Deps.Jobs deliberately. The store
// alone can settle the record, and the browser using it directly is exactly
// the bug this exists to prevent: a Cancel button that stopped the job on
// paper and left the runbook running on the device.
// jobJournal hands the Jobs view a journal reader, or an untyped nil.
//
// The same explicit conversion jobRelauncher makes, for the same reason: a
// nil *journal.EntStore in an interface parameter is a NON-nil interface
// holding a nil pointer, so the view's "is this wired" check would pass and
// it would draw a Tasks tab that panics on the first read. A deployment
// with no journal reader is real -- the conformance harness is one.
// jobLogArchive hands the Jobs view a log archive, or an untyped nil, for
// the typed-nil reason jobRelauncher documents.
func jobLogArchive(d Deps) jobs.LogArchive {
	if d.JobLogs == nil {
		return nil
	}
	return d.JobLogs
}

func jobJournal(d Deps) jobs.JournalReader {
	if d.JobJournal == nil {
		return nil
	}
	return d.JobJournal
}

func jobCanceller(d Deps) jobs.Canceler {
	if d.JobCanceller == nil {
		return nil
	}
	return d.JobCanceller
}

// jobRelauncher hands the Jobs view a relauncher, or an untyped nil.
//
// Deps.Dispatcher is a *api.Dispatcher, and passing a nil one straight into
// an interface parameter produces a NON-nil interface holding a nil
// pointer, so the view's own "is this wired" check would pass and the first
// relaunch would dereference it. A deployment with no dispatcher is real
// (the conformance harness is one), so this converts explicitly rather than
// relying on every callee to know the difference.
func jobRelauncher(d Deps) jobs.Relauncher {
	if d.Dispatcher == nil {
		return nil
	}
	return d.Dispatcher
}
