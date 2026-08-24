package archtest

import "testing"

// TestNoSecondRetryLoopOrCircuitBreaker asserts Phase 72's retry-loop
// consolidation (PLAN.md Section 1379's Build-Once table, "Retry policy
// (backoff, jitter, attempt cap, retryable classification, DLQ exit)")
// stays consolidated. Before Phase 72, internal/lock (queue.go's
// acquireWithContention, nats.go's CAS loops) and pkg/remoteexec
// (dial.go's dialWithRetry) each hand-rolled their own retry loop around
// the shared pkg/retry.Backoff delay math; this asserts both still
// import the shared pkg/retry.Do/Sleep loop pkg/remoteexec's own circuit
// breaker (pkg/remoteexec/breaker.go, unexported) sits behind, rather
// than a private copy silently regrowing in either package.
//
// This is an import-graph check, in TestOnlyDesignatedAdaptersImportConcreteDrivers's
// own style, not an AST inspection for "does this file contain a for
// loop with a sleep in it": the real, checkable claim is that the two
// packages known to have needed this consolidation still depend on
// where it now lives, the same shape every other archtest rule in this
// file uses to keep a known consolidation point from drifting back
// apart.
func TestNoSecondRetryLoopOrCircuitBreaker(t *testing.T) {
	const retryPkg = modulePath + "/pkg/retry"

	// internal/topology and internal/event joined this list in Phase 96a.
	// topology.ReconnectDelay is the CustomReconnectDelay callback every
	// NATS connection in the module now reconnects on, and
	// internal/event/dlq.go computes its NakWithDelay backoff the same
	// way. Both are delay computations with no loop of their own, because
	// nats.go and JetStream respectively already own the loop, which is
	// exactly the half of pkg/retry they should be consuming; a
	// hand-rolled exponent in either would have been invisible to this
	// rule before they were listed.
	for _, importPath := range []string{
		modulePath + "/internal/lock",
		modulePath + "/pkg/remoteexec",
		modulePath + "/internal/topology",
		modulePath + "/internal/event",
	} {
		pkgs := goList(t, false, importPath)
		if len(pkgs) != 1 {
			t.Fatalf("expected exactly one package listed for %s, got %d", importPath, len(pkgs))
		}

		found := false
		for _, imp := range pkgs[0].Imports {
			if imp == retryPkg {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not import %s: this package hand-rolled its own retry loop before Phase 72 consolidated it there; if it genuinely no longer retries anything, remove it from this test's list rather than letting the shared loop silently stop covering it", importPath, retryPkg)
		}
	}
}
