// Package routing: tests of CheckOnly.
package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// recordingExecutor keeps the payload it was handed.
type recordingExecutor struct{ got wire.DispatchPayload }

// Execute records payload.
func (r *recordingExecutor) Execute(_ context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	r.got = payload
	return wire.Outcome{}, nil
}

// TestCheckOnly proves everything through CheckOnly runs as a check,
// including a payload claiming to be a real run.
func TestCheckOnly(t *testing.T) {
	for _, claimed := range []string{"", "execute", "check", "anything"} {
		next := &recordingExecutor{}
		if _, err := CheckOnly(next).Execute(context.Background(), wire.DispatchPayload{Mode: claimed}); err != nil {
			t.Fatal(err)
		}
		if next.got.Mode != "check" {
			t.Errorf("a payload claiming %q ran in mode %q, want check", claimed, next.got.Mode)
		}
	}
}

// TestRollbackOnly proves the rollback loop runs a rollback and refuses,
// as unroutable, anything else arriving on its subject.
func TestRollbackOnly(t *testing.T) {
	next := &recordingExecutor{}
	rb := &wire.Rollback{Of: "job-x", Steps: []wire.RollbackStep{{Node: "tasks[0]"}}}
	if _, err := RollbackOnly(next).Execute(context.Background(), wire.DispatchPayload{RunbookID: "r", Rollback: rb}); err != nil {
		t.Fatal(err)
	}
	if next.got.Rollback != rb {
		t.Error("the rollback did not reach the adapter")
	}
	next = &recordingExecutor{}
	_, err := RollbackOnly(next).Execute(context.Background(), wire.DispatchPayload{RunbookID: "r"})
	if !errors.Is(err, ErrNoAdapter) || next.got.RunbookID != "" {
		t.Errorf("a dispatch with no rollback: err %v, reached the adapter %v", err, next.got.RunbookID != "")
	}
}
