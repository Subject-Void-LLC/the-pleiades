// Package jobtemplate registers the job template as a launchable type.
//
// This is AWX's JobTemplate, whose run is a job, and it is the type every
// launch in this build was before there was more than one. The key is AWX's
// own string so that importing an AWX schedule resolves its
// unified_job_template without translation.
//
// It declares no launcher. A job template is launched by the Controller's
// Dispatcher, which resolves the template, folds a saved configuration over
// its defaults, records the job and publishes the fan-out; wiring that here
// would make every consumer of the launchable registry depend on the whole
// dispatch path. cmd/controller composes the pairing.
package jobtemplate

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// Type is the registry key: AWX's own subclass name, defined beside the
// other keys in internal/launchable so that a store writing this type does
// not have to import this package and register it as a side effect.
const Type = launchable.TypeJobTemplate

// init registers the type. Reached only by a blank import from
// types/builtins.go, so a type is never half-present: either the package is
// imported and the type is launchable, or it is not there at all.
func init() {
	launchable.MustRegister(launchable.Descriptor{
		Type:  Type,
		Label: "Job template",

		// One run of a job template is a job, which is what the Jobs list
		// holds and what a job id names.
		UnifiedJobType: launchable.UnifiedJobJob,

		// The same scope the launch route itself requires
		// (apispec.LaunchTemplate). Stated here so that a schedule, and
		// later a workflow node, cannot be a way around it: arranging for
		// something to run repeatedly must need at least what running it
		// once needs.
		LaunchScope: auth.ScopeRunbookExecute,

		// A template is the one thing a saved launch configuration is
		// meaningful against: the overrides it holds are keyed by the
		// template's own promptable fields, and its survey answers by that
		// template's survey.
		AcceptsSavedConfig: true,
	})
}
