// Package pki declares the mTLS mesh's certificate authority contract.
//
// # Nothing implements this yet, and nothing calls it
//
// PKIEngine has no implementation and no caller anywhere in this module,
// so this package is in no binary's dependency graph and contributes
// nothing to any shipped artifact. That is a declared Build-Once shape
// (PLAN.md Section 25), not an oversight and not dead code left behind
// by a deletion: writing the contract down before the first consumer is
// what stops two phases from each growing their own bespoke certificate
// signer that a third has to reconcile. lock.CapacityCounter carries the
// same status and says so in the same way.
//
// The disclosure lives here rather than only in coverage-floor.json and
// docs/10-running-in-production.md, so a reader who opens the source
// learns it from the source.
package pki

import "context"

// PKIEngine handles the mTLS mesh. V1 is internal, V2 is HashiCorp Vault.
type PKIEngine interface {
	// SignCSR takes a Certificate Signing Request from a Runner and returns a 72-hour cert.
	SignCSR(ctx context.Context, csrPEM []byte) (certPEM []byte, err error)
}
