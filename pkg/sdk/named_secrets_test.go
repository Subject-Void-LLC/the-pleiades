// Tests for resolving secret references through a run's named secrets.
package sdk_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// namedRC is a run holding named secrets and keeping what is registered.
type namedRC struct {
	registrarRC
	values map[string]string
	err    error
}

func (r *namedRC) NamedSecret(name, field string) (string, bool, error) {
	if r.err != nil {
		return "", false, r.err
	}
	v, ok := r.values[name+"."+field]
	return v, ok, nil
}

// TestResolveSecretRefs: text with no reference passes whatever the run
// holds; a run with no named secrets says why; a reference resolves and its
// value joins the run's masking set; a store failure and a missing secret
// are errors naming the reference only.
func TestResolveSecretRefs(t *testing.T) {
	if got, values, err := sdk.ResolveSecretRefs(plainRC{}, "hostname r1"); err != nil || got != "hostname r1" || values != nil {
		t.Fatalf("plain text: %q, %v, %v", got, values, err)
	}
	if _, _, err := sdk.ResolveSecretRefs(plainRC{}, "key {{ secret.a.b }}"); !errors.Is(err, sdk.ErrNoNamedSecrets) {
		t.Fatalf("a run with no named secrets: %v", err)
	}

	rc := &namedRC{values: map[string]string{"dmvpn.psk": "Shared-Key-123"}}
	got, values, err := sdk.ResolveSecretRefs(rc, "pre-shared-key {{ secret.dmvpn.psk }}")
	if err != nil || got != "pre-shared-key Shared-Key-123" || !reflect.DeepEqual(values, []string{"Shared-Key-123"}) || !reflect.DeepEqual(rc.got, []string{"Shared-Key-123"}) {
		t.Fatalf("resolve: %q, %v, %v, registered %v", got, values, err, rc.got)
	}

	if _, _, err := sdk.ResolveSecretRefs(rc, "{{ secret.dmvpn.other }}"); err == nil || !strings.Contains(err.Error(), "add-secret dmvpn --field other") {
		t.Fatalf("a missing field: %v", err)
	}
	rc.err = errors.New("store unreadable")
	if _, _, err := sdk.ResolveSecretRefs(rc, "{{ secret.dmvpn.psk }}"); err == nil || !strings.Contains(err.Error(), "store unreadable") {
		t.Fatalf("a store failure: %v", err)
	}
}
