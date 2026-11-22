// Package credential: the credential file's naming rule that tells
// sealedUnderMasterKey which of its fields hold ciphertext.
package credential

import (
	"reflect"
	"slices"
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
	visited := map[reflect.Type]bool{}
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		// Through maps, slices and pointers to the struct they hold.
		for typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || visited[typ] {
			return
		}
		visited[typ] = true
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
			// A named struct inside an entry is read the same way.
			walk(f.Type)
		}
	}
	walk(reflect.TypeFor[credentialsDocument]())
	if seen < 11 {
		t.Fatalf("found %d ciphertext fields, want every one of the login, SNMP and database families", seen)
	}
}

// TestEveryCredentialSectionIsNamed: every section credentialsDocument has is
// one credentialFileSealed counts, under a noun of its own, and the sections
// whose every value is ciphertext are among them. The control is that the
// reflection finds the four sections the file has today.
func TestEveryCredentialSectionIsNamed(t *testing.T) {
	sections := credentialSections()
	if len(sections) < 4 {
		t.Fatalf("found sections %v, want devices, registries, databases and secrets at least", sections)
	}
	for _, section := range sections {
		if sectionNouns[section] == "" {
			t.Errorf("section %q of the credential file has no noun in sectionNouns, so a refusal would name it only as %q", section, section+" entry")
		}
	}
	for section := range everyFieldSealed {
		if !slices.Contains(sections, section) {
			t.Errorf("everyFieldSealed names %q, which the credential file has no section for", section)
		}
	}
}

// TestHoldsCiphertext_ReadsNestedFields: a ciphertext field inside a nested
// mapping or list is found, a plaintext one is not, and a named secret's
// plain field names count.
func TestHoldsCiphertext_ReadsNestedFields(t *testing.T) {
	for _, tc := range []struct {
		entry      any
		everyField bool
		want       bool
	}{
		{map[string]any{"username": "u"}, false, false},
		{map[string]any{"password_encrypted": "c"}, false, true},
		{map[string]any{"tls": map[string]any{"key_encrypted": "c"}}, false, true},
		{map[string]any{"keys": []any{map[string]any{"key_encrypted": "c"}}}, false, true},
		{map[string]any{"keys": []any{map[string]any{"name": "a"}}}, false, false},
		{map[string]any{"psk": "c"}, true, true},
		{"not a mapping", false, false},
	} {
		if got := holdsCiphertext(tc.entry, tc.everyField); got != tc.want {
			t.Errorf("holdsCiphertext(%v, %v) = %v, want %v", tc.entry, tc.everyField, got, tc.want)
		}
	}
}
