package api

import (
	"context"
	"log/slog"
)

// export_test.go exists only when this package's own test binary is
// built, and is never linked into any production binary or any other
// package's test binary. IdentityKeyForTest exposes identityKey to this
// package's black-box api_test tests, which sit in the same directory and
// so may still import it, for a direct-handler-call test that wants an
// authenticated context without standing up a real token.
//
// Phase 12 removed this same constant from middleware.go, where it lived
// as ordinary, always-linked production code reachable by any importer.
// The move alone does not make cross-package tests (tests/e2e, a
// different package in a different directory) safe: they never had
// access to an export_test.go symbol regardless of where it lived, which
// is exactly why the old doc comment on this constant called moving it
// here "not enough on its own." internal/auth/authtest is the real fix
// for that case: a token AuthMiddleware actually validates, rather than a
// context value nothing checks.
const IdentityKeyForTest = identityKey

// ContextWithLoggerForTest attaches logger to ctx under the same key
// StructuredLoggerMiddleware uses, so a test that calls Respond directly
// can capture the server-side lines it emits on a write failure.
//
// Those branches are only reachable with a ResponseWriter that fails, and
// a failing writer cannot be routed through a real router, so there is no
// way to exercise them through the normal middleware chain.
func ContextWithLoggerForTest(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}
