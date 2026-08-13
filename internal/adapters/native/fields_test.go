package native

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestTaskTimeout proves the "timeout" field's own declared semantics:
// seconds converted to a time.Duration, and zero (whether from an absent
// field or an explicit 0) means "no timeout," never a zero-length
// deadline that would abandon a task instantly.
func TestTaskTimeout(t *testing.T) {
	tests := []struct {
		name   string
		fields launch.Fields
		want   time.Duration
	}{
		{name: "unset field", fields: nil, want: 0},
		{name: "explicit zero", fields: launch.Fields{"timeout": 0}, want: 0},
		{name: "45 seconds", fields: launch.Fields{"timeout": 45}, want: 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskTimeout(tt.fields); got != tt.want {
				t.Errorf("taskTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}
