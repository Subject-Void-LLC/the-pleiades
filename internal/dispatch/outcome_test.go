package dispatch_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/dispatch"
)

// TestOutcome_String is table-driven per AGENTS.md, covering every
// declared Outcome constant.
func TestOutcome_String(t *testing.T) {
	tests := []struct {
		name    string
		outcome dispatch.Outcome
		want    string
	}{
		{name: "dispatched", outcome: dispatch.OutcomeDispatched, want: "dispatched"},
		{name: "skipped", outcome: dispatch.OutcomeSkipped, want: "skipped"},
		{name: "failed", outcome: dispatch.OutcomeFailed, want: "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.outcome.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseOutcome covers every recognized value plus the rejection path,
// mirroring pkg/inventory.ParseLifecycleState's own table shape: an
// unrecognized value must return an error, never silently default to a
// particular outcome.
func TestParseOutcome(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    dispatch.Outcome
		wantErr bool
	}{
		{name: "dispatched", input: "dispatched", want: dispatch.OutcomeDispatched},
		{name: "skipped", input: "skipped", want: dispatch.OutcomeSkipped},
		{name: "failed", input: "failed", want: dispatch.OutcomeFailed},
		{name: "empty string is rejected", input: "", wantErr: true},
		{name: "unrecognized value is rejected", input: "bogus", wantErr: true},
		{name: "case sensitive: uppercase is rejected", input: "Dispatched", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dispatch.ParseOutcome(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseOutcome(%q) succeeded unexpectedly with %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOutcome(%q) returned unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseOutcome(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestErrJobNotFound_Sentinel proves ErrJobNotFound is usable with
// errors.Is the way JobStore's own doc comments promise, the same
// assurance runbook.ErrNotFound and inventory.ErrItemNotFound both
// document for their own callers.
func TestErrJobNotFound_Sentinel(t *testing.T) {
	wrapped := fmt.Errorf("job %s: %w", "some-id", dispatch.ErrJobNotFound)
	if !errors.Is(wrapped, dispatch.ErrJobNotFound) {
		t.Fatalf("errors.Is did not recognize a wrapped ErrJobNotFound")
	}
}
