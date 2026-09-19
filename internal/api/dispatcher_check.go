// Package api: the template check route, a launch made a check.
package api

import (
	"fmt"
	"maps"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// CheckFromTemplate serves POST /templates/{id}/check: LaunchFromTemplate,
// with the run made a check.
//
// It is its own route rather than a mode in the launch body because of a
// Controller that predates check mode. Such a Controller answers this
// route 404, where it would have taken a launch body asking for a check,
// found no mode field it knew, and run for real. A client following the
// template's "check" link reaches a check or nothing.
func (d *Dispatcher) CheckFromTemplate(w http.ResponseWriter, r *http.Request) {
	d.serveLaunch(w, r, true)
}

// forceCheck returns overrides asking for a check. Overrides that already
// ask for a check are kept as they are. Overrides asking for anything else
// are refused rather than overruled (launch.ErrMode): a body saying
// "execute" sent to the check route is a confused client, and quietly
// checking instead would leave the caller believing something ran that
// did not.
//
// The caller's map is copied, never written: it came from the request.
func forceCheck(overrides map[string]any) (map[string]any, error) {
	if value, set := overrides[launch.ModeField]; set && value != string(collection.ModeCheck) {
		return nil, fmt.Errorf("%w: the check route runs a check, but the overrides ask for mode %v", launch.ErrMode, value)
	}
	out := maps.Clone(overrides)
	if out == nil {
		out = map[string]any{}
	}
	out[launch.ModeField] = string(collection.ModeCheck)
	return out, nil
}

// LaunchOption adjusts one launch (Dispatcher.LaunchTemplate).
type LaunchOption func(*launchOptions)

// launchOptions is what LaunchOptions set. The zero value is the
// fail-closed launch: one that may not run an external program's check.
type launchOptions struct {
	mayRunForReal bool
}

// MayRunForReal records whether whoever asked for this launch may run
// the template for real, which is what entitles a check of it to run an
// external Collection program's Check (dispatch.Job.ExternalChecks):
// nothing has proven a third party's Check only reads, so a caller who
// may only check (runbook:check without runbook:execute) gets those tasks
// reported unchecked instead. A launch that does not say is treated as
// one that may not.
func MayRunForReal(may bool) LaunchOption {
	return func(o *launchOptions) { o.mayRunForReal = may }
}
