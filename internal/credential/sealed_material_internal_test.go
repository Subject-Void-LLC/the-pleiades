// Package credential: the credential file's naming rule that tells
// sealedUnderMasterKey which of its fields hold ciphertext.
package credential

import (
	"reflect"
	"strings"
	"testing"
)

// TestEveryCiphertextFieldIsNamedEncrypted: every field of a stored entry that
// holds ciphertext (its Go name ends in Encrypted) is written under a YAML key
// ending in _encrypted, and no other field is, so credentialFileSealed, which
// reads the file by that rule, counts every sealed value and only those. The
// control is that the walk finds the fields at all.
func TestEveryCiphertextFieldIsNamedEncrypted(t *testing.T) {
	seen := 0
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if f.Anonymous && tag == "" {
				walk(f.Type)
				continue
			}
			if strings.HasSuffix(f.Name, "Encrypted") != strings.HasSuffix(tag, "_encrypted") {
				t.Errorf("%s.%s is written as %q: a ciphertext field's key ends in _encrypted, and only its", typ.Name(), f.Name, tag)
			}
			if strings.HasSuffix(f.Name, "Encrypted") {
				seen++
			}
		}
	}
	walk(reflect.TypeFor[credentialEntry]())
	walk(reflect.TypeFor[databaseEntry]())
	if seen < 11 {
		t.Fatalf("found %d ciphertext fields, want every one of the login, SNMP and database families", seen)
	}
}
