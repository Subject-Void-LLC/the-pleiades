// Tests for registering a secret a method obtained while it ran.
package sdk_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// plainRC is a run that can hold no registered secret.
type plainRC struct{}

func (plainRC) InjectSecrets() map[string]string { return nil }
func (plainRC) SetStat(string, any) error        { return nil }
func (plainRC) EmitFact(string, any) error       { return nil }

// registrarRC is a run that keeps what is registered.
type registrarRC struct {
	plainRC
	got []string
}

func (r *registrarRC) RegisterSecret(values ...string) { r.got = append(r.got, values...) }

// TestRegisterSecret hands values to a run that can hold them, and to one
// that cannot does nothing at all rather than fail the method.
func TestRegisterSecret(t *testing.T) {
	rc := &registrarRC{}
	sdk.RegisterSecret(rc, "header-value", "password")
	if want := []string{"header-value", "password"}; !reflect.DeepEqual(rc.got, want) {
		t.Errorf("registered %v, want %v", rc.got, want)
	}
	sdk.RegisterSecret(plainRC{}, "ignored")
}
