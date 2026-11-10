// This file holds lockSet to the one rule it cannot be trusted without: it
// only means anything inside a transaction.
package inventory

import (
	"context"
	"strings"
	"testing"
)

// TestLockSetRefusesToRunOutsideATransaction proves the guard. Outside a
// transaction the write that takes the lock would commit at once and hold
// nothing, so two PATCHes of one inventory could again write back each
// other's membership, with no sign that they do.
func TestLockSetRefusesToRunOutsideATransaction(t *testing.T) {
	_, err := lockSet(context.Background(), nil, 1)
	if err == nil || !strings.Contains(err.Error(), "inside a transaction") {
		t.Fatalf("lockSet() outside a transaction = %v, want a refusal", err)
	}
}
