// Package dispatch: the per-kind definition sources the Worker's fan-out
// prepares a job through.
package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// PreparedDefinition is what fan-out needs to know about the thing a job
// runs, resolved once per job rather than once per device.
type PreparedDefinition struct {
	// Required is the capability set every device is admitted against.
	// Empty means no capability gate: lifecycle and addressability still
	// apply per device, but nothing about the definition itself narrows
	// the fleet.
	Required []capability.Name

	// Interruptible is whether the run tolerates being stopped mid-flight,
	// carried onto every device's dispatch payload.
	Interruptible bool
}

// DefinitionSource prepares one kind's definitions for fan-out.
//
// It returns the sanitised reason alongside the error, the exact
// two-audience split targetSelector already draws (worker.go): the error
// is for the operator reading logs and may name storage internals, the
// reason is for whoever is polling the job and must not. reason is
// meaningful only when err is non-nil.
//
// The Worker holds one of these per kind, keyed by the kind the job
// carries, mirroring internal/adapters/routing's map on the Runner side.
// It used to hold a runbook.Source and resolve every job through it
// unconditionally, which meant a playbook-kind job failed fan-out inside
// the Controller with "runbook not found" before the Runner's own adapter
// selection was ever consulted: one of the four independent walls that
// made the playbook kind a facade, each behind the others.
type DefinitionSource interface {
	Prepare(ctx context.Context, definition string) (PreparedDefinition, string, error)
}

// runbookDefinitionSource prepares the native kind through the compiled
// runbook source, exactly what the fan-out always did.
type runbookDefinitionSource struct {
	src runbook.Source
}

// Prepare implements DefinitionSource.
func (s runbookDefinitionSource) Prepare(ctx context.Context, definition string) (PreparedDefinition, string, error) {
	rb, err := s.src.Get(ctx, definition)
	if err != nil {
		// The reason names only the id, never err's own text: err can
		// carry a filesystem path or another storage-layer detail
		// (runbook.Source.Get's own doc comment), and Job.failure_reason
		// is a caller-readable audit field, not a server log.
		reason := fmt.Sprintf("runbook %q could not be resolved", definition)
		if errors.Is(err, runbook.ErrNotFound) {
			reason = fmt.Sprintf("runbook %q not found", definition)
		}
		return PreparedDefinition{}, reason, err
	}
	return PreparedDefinition{Required: rb.Required, Interruptible: rb.Interruptible}, "", nil
}

// PlaybookGetter is the slice of internal/playbook.Source fan-out needs:
// resolution, not listing.
type PlaybookGetter interface {
	Get(ctx context.Context, id string) ([]byte, error)
}

// NewPlaybookDefinitionSource prepares the playbook kind over src.
//
// Preparation is existence, nothing more. A playbook's capability
// requirements are deliberately empty: the platform never parses a legacy
// playbook (it is handed verbatim to a real ansible-playbook process in
// the Runner's sandbox), so it has no basis for deriving a capability set,
// and inventing one would skip devices on a guess. Admission for a
// playbook job is therefore lifecycle plus addressability, and whether
// Ansible can actually manage each device is Ansible's own per-host
// verdict, reported through the job's events exactly as it would be on a
// laptop. Interruptible is false for the same reason: nothing here can
// promise a safe stopping point inside a playbook it has never read.
func NewPlaybookDefinitionSource(src PlaybookGetter) DefinitionSource {
	return playbookDefinitionSource{src: src}
}

type playbookDefinitionSource struct {
	src PlaybookGetter
}

// Prepare implements DefinitionSource.
func (s playbookDefinitionSource) Prepare(ctx context.Context, definition string) (PreparedDefinition, string, error) {
	if _, err := s.src.Get(ctx, definition); err != nil {
		reason := fmt.Sprintf("playbook %q could not be resolved", definition)
		if errors.Is(err, playbook.ErrNotFound) {
			reason = fmt.Sprintf("playbook %q not found", definition)
		}
		return PreparedDefinition{}, reason, err
	}
	return PreparedDefinition{}, "", nil
}
