// Package view_test: WrapNotFound turns a store's not-found error into the
// view's own, and leaves every other error alone.
package view_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestWrapNotFound covers each shape a write port hands it.
func TestWrapNotFound(t *testing.T) {
	storeNotFound := errors.New("store: not found")
	other := errors.New("store: disk on fire")
	for _, tc := range []struct {
		name         string
		err          error
		wantNotFound bool
		wantSame     bool
	}{
		{"nil stays nil", nil, false, true},
		{"the sentinel itself", storeNotFound, true, false},
		{"the sentinel wrapped", fmt.Errorf("reading 7: %w", storeNotFound), true, false},
		{"another error", other, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := view.WrapNotFound(tc.err, storeNotFound)
			if errors.Is(got, view.ErrNotFound) != tc.wantNotFound {
				t.Errorf("errors.Is(%v, ErrNotFound) = %v, want %v", got, !tc.wantNotFound, tc.wantNotFound)
			}
			if tc.wantSame && got != tc.err {
				t.Errorf("WrapNotFound(%v) = %v, want it unchanged", tc.err, got)
			}
			if tc.wantNotFound && !errors.Is(got, storeNotFound) {
				t.Errorf("WrapNotFound(%v) = %v, want the store's error kept in the chain", tc.err, got)
			}
		})
	}
}
