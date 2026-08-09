package lock_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

func TestModeString(t *testing.T) {
	tests := []struct {
		mode lock.Mode
		want string
	}{
		{lock.ModeExclusive, "exclusive"},
		{lock.ModeShared, "shared"},
		{lock.Mode(99), "Mode(99)"},
	}
	for _, tc := range tests {
		if got := tc.mode.String(); got != tc.want {
			t.Errorf("Mode(%d).String() = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestContentionPolicyString(t *testing.T) {
	tests := []struct {
		policy lock.ContentionPolicy
		want   string
	}{
		{lock.PolicyReject, "reject"},
		{lock.PolicyQueue, "queue"},
		{lock.PolicyPriority, "priority"},
		{lock.ContentionPolicy(99), "ContentionPolicy(99)"},
	}
	for _, tc := range tests {
		if got := tc.policy.String(); got != tc.want {
			t.Errorf("ContentionPolicy(%d).String() = %q, want %q", tc.policy, got, tc.want)
		}
	}
}
