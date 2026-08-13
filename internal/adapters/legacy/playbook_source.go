package legacy

import (
	"context"
)

// PlaybookSource resolves a dispatched wire.DispatchPayload.RunbookID to
// the raw bytes of the legacy Ansible playbook it names. A distinct port
// from internal/runbook.Source (which resolves the same kind of id to a
// compiled *engine.DAG): which one a given RunbookID is resolved through
// is decided per dispatch by the kind it carries, via
// internal/adapters/routing.
//
// Get-only, deliberately narrower than internal/playbook.Source: this
// adapter executes exactly one playbook per dispatch and has no business
// listing a catalog. The concrete implementation lives in
// internal/playbook (DirSource satisfies this interface structurally),
// moved out of this package so that the Controller can list playbooks
// without importing the testcontainers-backed orchestration this package
// carries (FAILURE_PATTERNS.md #109).
type PlaybookSource interface {
	Get(ctx context.Context, id string) ([]byte, error)
}
