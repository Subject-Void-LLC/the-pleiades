// The identity code's failure branches, which no original input reaches (Phase
// 136b): a manifest set that does not encode, or does not decode again, must
// panic rather than digest "null", and a group digest that fails must reach
// BuiltinIdentity's caller.
package collection

import (
	"errors"
	"strings"
	"testing"
)

// TestManifestSetDigest_PanicsRatherThanCollide: each step of the canonical
// form that fails panics with the invariant named.
func TestManifestSetDigest_PanicsRatherThanCollide(t *testing.T) {
	original := marshalJSON
	t.Cleanup(func() { marshalJSON = original })
	for name, fake := range map[string]func(calls *int) func(any) ([]byte, error){
		"the first encode fails": func(*int) func(any) ([]byte, error) {
			return func(any) ([]byte, error) { return nil, errors.New("no") }
		},
		"the encoding does not decode": func(*int) func(any) ([]byte, error) {
			return func(any) ([]byte, error) { return []byte("{"), nil }
		},
		"the second encode fails": func(calls *int) func(any) ([]byte, error) {
			return func(v any) ([]byte, error) {
				*calls++
				if *calls == 2 {
					return nil, errors.New("no")
				}
				return original(v)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			marshalJSON = fake(&calls)
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), "collection: ") {
					t.Errorf("recovered %v, want a panic naming the invariant", r)
				}
			}()
			ManifestSetDigest(map[string]Manifest{"file.copy": {}})
			t.Error("no panic")
		})
	}
}

// TestBuiltinIdentity_PassesOnAGroupDigestFailure: a failing GroupDigest is
// the caller's error, never an identity with an empty digest.
func TestBuiltinIdentity_PassesOnAGroupDigestFailure(t *testing.T) {
	defer CatalogForTest()()
	if err := InstallCatalog([]byte(`{"file.copy":{"status":"implemented"}}`)); err != nil {
		t.Fatal(err)
	}
	original := groupDigest
	t.Cleanup(func() { groupDigest = original })
	groupDigest = func(string) (string, error) { return "", ErrGroupNotLinked }
	if id, err := BuiltinIdentity("file.copy", "0.4.0"); !errors.Is(err, ErrGroupNotLinked) || !id.IsZero() {
		t.Errorf("BuiltinIdentity = %+v, %v; want the group digest's error and no identity", id, err)
	}
}
