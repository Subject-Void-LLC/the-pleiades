// This file is the one predicate that decides whether a caller may point
// something at a launchable.
//
// It exists as one function because the same question is asked in two
// places: a picker deciding what to offer, and a write path deciding what to
// accept. Those must agree. A rule that only shaped the form would not be
// enforced at all, since the same field is accepted over the API where there
// are no options to validate against, and a rule stated twice is a rule that
// will eventually disagree with itself.
//
// The other half of the same idea is what a control offers. A chooser that
// listed everything and let the failure surface at the first fire would be
// offering a choice that can only fail, so the picker filters with Admits
// and the store checks with Admits, and neither invents its own test.
package launchable

import (
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// Refusal-shaped failures, each distinguishable because each maps onto a
// different status and a different message.
var (
	// ErrNotPermitted is a caller who may not launch this type. It is a 403:
	// the target exists and the request was understood.
	ErrNotPermitted = errors.New("launchable: you may not launch this")

	// ErrCrossTenant is a target in another organization. Also a 403, and
	// deliberately not a 404: this platform's own tenancy is not yet
	// enforced per request (no request carries a tenant), so pretending the
	// target does not exist would be a promise the rest of the system does
	// not keep.
	ErrCrossTenant = errors.New("launchable: that belongs to another organization")

	// ErrSavedConfigRefused is a saved launch configuration offered to a
	// type that cannot use one.
	ErrSavedConfigRefused = errors.New("launchable: this kind of launchable takes no saved configuration")

	// ErrBusy is a launchable that cannot start another run right now: a
	// project already syncing. It is not a failure of the thing that asked,
	// which matters to a schedule: a fire that lands on a busy target is
	// recorded as skipped and the schedule moves on, rather than being
	// retried into a loop.
	ErrBusy = errors.New("launchable: this is already running")
)

// Reach is what one caller may point at, in one organization.
//
// Grants is the caller's own scope test, which is Identity.HasScope in
// production. It is a func rather than an *auth.Identity so that a
// mechanism with no identity can still be expressed honestly: the scheduler
// firing a schedule it saved earlier grants everything, because the
// authorization decision was made when the schedule was written and is not
// re-made at 03:00 by a process nobody is logged into.
type Reach struct {
	OrganizationID int
	Grants         func(auth.Scope) bool
}

// ReachOf builds the Reach of an authenticated caller within one
// organization.
func ReachOf(identity *auth.Identity, organizationID int) Reach {
	reach := Reach{OrganizationID: organizationID}
	if identity != nil {
		reach.Grants = identity.HasScope
	}
	return reach
}

// Admits reports whether this caller may point something at target,
// returning the reason if not.
//
// Three conditions, in the order whose message is most useful: the type has
// to be one this build knows, the caller has to hold the scope that type
// declares, and the target has to be in the caller's own organization. A
// Reach with no Grants admits nothing, which is the refusing zero value: a
// caller nobody authenticated is not a caller who may launch everything.
func (r Reach) Admits(target Target) error {
	d, err := Describe(target)
	if err != nil {
		return err
	}

	if r.Grants == nil || !r.Grants(d.LaunchScope) {
		return fmt.Errorf("%w: launching a %s needs %s", ErrNotPermitted, d.Label, d.LaunchScope)
	}

	// Organization zero on the Reach means "not narrowed to one tenant",
	// which is what an administrative caller and the scheduler both are
	// today. A target with no organization is a broken row rather than a
	// wildcard, and is refused.
	if r.OrganizationID != 0 && target.OrganizationID != r.OrganizationID {
		return fmt.Errorf("%w: %s belongs to organization %d", ErrCrossTenant, target.Name, target.OrganizationID)
	}
	if target.OrganizationID == 0 {
		return fmt.Errorf("%w: %s belongs to no organization", ErrCrossTenant, target.Name)
	}
	return nil
}

// Everything is the Reach of a mechanism acting on a decision somebody
// already made: the scheduler firing a schedule that was authorized when it
// was saved.
//
// It is a named constructor rather than a zero value so that it reads as a
// deliberate claim at the call site, and so that a Reach nobody filled in
// stays the refusing one.
func Everything() Reach {
	return Reach{Grants: func(auth.Scope) bool { return true }}
}

// AdmitsSavedConfig reports whether this type may carry a saved launch
// configuration with the given id, where zero means none.
//
// Checked here rather than at each consumer for the same reason the launch
// scope is: the answer belongs to the type, and a project sync silently
// ignoring a configuration somebody chose would be worse than refusing it.
func (d Descriptor) AdmitsSavedConfig(savedConfigID int) error {
	if savedConfigID != 0 && !d.AcceptsSavedConfig {
		return fmt.Errorf("%w: a %s runs with no launch-time overrides", ErrSavedConfigRefused, d.Label)
	}
	return nil
}

// TargetField is the name of the control that names what something launches,
// and the field every refusal about it is attributed to.
//
// It is AWX's own field name on a schedule, unified_job_template, and it lives
// here so that the API, the form, the schedule's store and each type's own
// launcher all blame one string. A refusal attributed to a field name nothing
// renders is a refusal a person never sees.
const TargetField = "unified_job_template"

// SavedConfigField is the same, for the control that chooses a saved launch
// configuration.
const SavedConfigField = "saved_config"

// Refusal is one reason a launch was refused, attributed to the field that
// caused it.
//
// The field matters because these refusals are made at a write, and a
// message with nowhere to land renders as a form-wide failure that says
// nothing about which control to change. An empty Field means the refusal
// belongs to the record rather than to one of its values.
type Refusal struct {
	Field   string
	Message string

	// Err is what caused the refusal, when the cause is an error a caller
	// might test for. A Refusal is a message for a person, and dropping the
	// cause to carry one would make every such refusal opaque to code: a
	// handler mapping launch.ErrNotFound onto a 404 has to be able to see it
	// through the wrapping.
	Err error
}

// Error implements error.
func (r Refusal) Error() string { return r.Message }

// Unwrap exposes the cause, so errors.Is reaches through a Refusal.
func (r Refusal) Unwrap() error { return r.Err }
