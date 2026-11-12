// Package capability: the definition digest's one failure path, which no
// definition can reach.
package capability

import (
	"errors"
	"strings"
	"testing"
)

// TestDefinitionDigest_PanicsRatherThanCollide: were the digest form ever
// to fail to encode, DefinitionDigest panics, since digesting nothing would
// give every definition one digest and every grant would hold across any
// change.
func TestDefinitionDigest_PanicsRatherThanCollide(t *testing.T) {
	real := marshalDigestForm
	t.Cleanup(func() { marshalDigestForm = real })
	marshalDigestForm = func(any) ([]byte, error) { return nil, errors.New("an injected failure") }
	defer func() {
		r := recover()
		if s, ok := r.(string); !ok || !strings.Contains(s, "an injected failure") {
			t.Fatalf("recovered %v, want a panic naming the failure", r)
		}
	}()
	DefinitionDigest(DataDefinition{Name: "acme.widget"})
	t.Fatal("DefinitionDigest returned a digest when its form did not encode")
}
