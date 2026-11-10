// This file holds lockAnnouncement to the one rule it cannot be trusted
// without: it only means anything inside a transaction.
package announce

import (
	"context"
	"strings"
	"testing"
)

// TestLockAnnouncementRefusesToRunOutsideATransaction proves the guard.
// Outside a transaction the write that takes the lock would commit at once
// and hold nothing, so a caller that forgot the transaction would get a fold
// that races and no sign that it does.
func TestLockAnnouncementRefusesToRunOutsideATransaction(t *testing.T) {
	_, err := lockAnnouncement(context.Background(), nil, 1)
	if err == nil || !strings.Contains(err.Error(), "inside a transaction") {
		t.Fatalf("lockAnnouncement() outside a transaction = %v, want a refusal", err)
	}
}
