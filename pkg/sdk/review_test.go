// What a result held for a person looks like to a caller.
package sdk_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestHeldError_IsHeldAndNothingElse: a held result is told from a failed
// one through errors.Is however it is wrapped, says why, and is not mistaken
// for any other error.
func TestHeldError_IsHeldAndNothingElse(t *testing.T) {
	held := fmt.Errorf("ai.chat: %w", &sdk.HeldError{Key: "k", Reason: "it cost more than 1"})
	if !errors.Is(held, sdk.ErrHeld) {
		t.Error("a wrapped HeldError is not ErrHeld")
	}
	var as *sdk.HeldError
	if !errors.As(held, &as) || as.Key != "k" || !strings.Contains(as.Error(), "it cost more than 1") {
		t.Errorf("HeldError = %+v", as)
	}
	if errors.Is(errors.New("plain"), sdk.ErrHeld) || errors.Is(held, errors.New("other")) {
		t.Error("an error that is not held reads as held")
	}
}
