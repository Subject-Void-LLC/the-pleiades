// This file holds lockType to the one rule it cannot be trusted without: it
// only means anything inside a transaction.
package credstore

import (
	"context"
	"strings"
	"testing"
)

// TestLockTypeRefusesToRunOutsideATransaction proves the guard. Outside a
// transaction the write that takes the lock would commit at once and hold
// nothing, so a caller that forgot the transaction would get a check that
// races and no sign that it does.
func TestLockTypeRefusesToRunOutsideATransaction(t *testing.T) {
	s := &entStore{}
	_, err := s.lockType(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "inside a transaction") {
		t.Fatalf("lockType() outside a transaction = %v, want a refusal", err)
	}
}
