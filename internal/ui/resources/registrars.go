package resources

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/credentials"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/dashboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/devices"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/governance"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/jobs"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/runbooks"
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
		func(Deps) error { return credentials.Register() },
		func(d Deps) error { return dashboard.Register(d.Jobs) },
		func(d Deps) error { return devices.Register(d.Inventory, d.Factory) },
		func(Deps) error { return governance.Register() },
		func(d Deps) error { return jobs.Register(d.Jobs, d.Dispatcher) },
		func(d Deps) error { return runbooks.Register(d.Runbooks) },
	}
}
