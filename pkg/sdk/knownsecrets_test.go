// Tests for KnownSecretValues.
package sdk

import (
	"slices"
	"testing"
)

// knownContext answers KnownSecrets with values.
type knownContext struct {
	RunbookContext
	values []string
}

// KnownSecretValues implements KnownSecrets.
func (c knownContext) KnownSecretValues() []string { return c.values }

func TestKnownSecretValues(t *testing.T) {
	if got := KnownSecretValues(knownContext{values: []string{"a-named-secret"}}); !slices.Equal(got, []string{"a-named-secret"}) {
		t.Errorf("KnownSecretValues = %v, want the context's", got)
	}
	var silent struct{ RunbookContext }
	if got := KnownSecretValues(silent); got != nil {
		t.Errorf("a context that does not say hands out %v", got)
	}
}
