// Tests for named secrets through the run's context.
package engine_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestNamedSecrets_AContextWithNoStoreSaysWhy: a context with no credential
// store (a Runner's) answers a reference with the reason it cannot resolve
// one, never with "no such secret", which would send a person to add one.
func TestNamedSecrets_AContextWithNoStoreSaysWhy(t *testing.T) {
	rc := engine.NewRunbookContext(nil)
	if _, _, err := sdk.ResolveSecretRefs(rc, "key {{ secret.dmvpn.psk }}"); !errors.Is(err, sdk.ErrNoNamedSecrets) {
		t.Fatalf("err = %v, want ErrNoNamedSecrets", err)
	}
	if got, _, err := sdk.ResolveSecretRefs(rc, "hostname r1"); err != nil || got != "hostname r1" {
		t.Fatalf("text with no reference: %q, %v", got, err)
	}
}
