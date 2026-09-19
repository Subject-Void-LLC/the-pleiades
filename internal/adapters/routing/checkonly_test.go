// Package routing: tests of CheckOnly.
package routing

import (
	"context"
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
