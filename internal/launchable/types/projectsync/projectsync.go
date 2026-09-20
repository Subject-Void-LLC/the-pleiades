// Package projectsync registers the project as a launchable type, whose run
// is a sync of its source.
//
// This is AWX's Project, and the reason it is a launchable type at all is
// that its run is a job in every sense that matters: it has a status, a
// start, a finish and an outcome somebody reads afterward. AWX calls that a
// project_update and lists it beside a playbook run, which is why one
// schedule mechanism covers both there and why it now covers both here.
//
// The key is "project", AWX's subclass name, rather than "project_sync": the
// launchable is the project, and the sync is what a run of it does.
//
// It declares no launcher, for the same reason jobtemplate does not: a sync
// is started by internal/project's Runner, and cmd/controller composes the
// pairing.
package projectsync

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// Type is the registry key: AWX's own subclass name, defined beside the
// other keys in internal/launchable for the reason jobtemplate's own Type
// gives.
const Type = launchable.TypeProject

// init registers the type, reached only by a blank import from
// types/builtins.go.
func init() {
	launchable.MustRegister(launchable.Descriptor{
		Type:  Type,
		Label: "Project sync",

		// AWX's name for one run of a project.
		UnifiedJobType: launchable.UnifiedJobProjectUpdate,

		// The same scope the sync route requires (apispec.SyncProject).
		// Syncing is a write rather than a read because it runs a network
		// operation as the deployment, against an address the project names,
		// and writes the result to local disk; scheduling one must need no
		// less.
		LaunchScope: auth.ScopeProjectWrite,

		// A sync takes no launch-time overrides. There is nothing to
		// override: what to fetch and from where is the project's own
		// record, and a survey would have nothing to answer. Offered
		// otherwise, a saved configuration would be values somebody chose
		// that silently never applied.
		AcceptsSavedConfig: false,
	})
}
