// Tests for secret references.
package secretref_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// store is a lookup over a fixed table, for tests.
func store(values map[string]string) secretref.Lookup {
	return func(r secretref.Ref) (string, bool, error) {
		v, ok := values[r.Name+"."+r.Field]
		return v, ok, nil
	}
}

// TestResolve_SubstitutesEachReference: every reference in a line is replaced
// by its value, the values come back for masking, and text without one is
// returned as it is.
func TestResolve_SubstitutesEachReference(t *testing.T) {
	lookup := store(map[string]string{"dmvpn.psk": "S3cretPSK", "ca.passphrase": "Pass-Phrase"})
	got, values, err := secretref.Resolve("  pre-shared-key local {{ secret.dmvpn.psk }} remote {{secret.dmvpn.psk}}", lookup)
	if err != nil || got != "  pre-shared-key local S3cretPSK remote S3cretPSK" || len(values) != 2 || values[0] != "S3cretPSK" {
		t.Fatalf("Resolve = %q, %q, %v", got, values, err)
	}
	if got, values, err := secretref.Resolve("hostname R1", lookup); err != nil || got != "hostname R1" || len(values) != 0 {
		t.Fatalf("plain text = %q, %q, %v", got, values, err)
	}
	if refs := secretref.Find("a {{ secret.ca.passphrase }} b"); len(refs) != 1 || refs[0] != (secretref.Ref{Name: "ca", Field: "passphrase"}) {
		t.Fatalf("Find = %+v", refs)
	}
}

// TestResolve_RefusesWithoutTheValue: a missing secret, a lookup failure and a
// value that could change the command are errors that name the reference and
// never carry the value.
func TestResolve_RefusesWithoutTheValue(t *testing.T) {
	for name, c := range map[string]struct {
		values map[string]string
		lookup secretref.Lookup
		text   string
		want   string
	}{
		"no such secret":  {values: map[string]string{}, text: "{{ secret.dmvpn.psk }}", want: "pleiades add-secret dmvpn --field psk"},
		"a space":         {values: map[string]string{"dmvpn.psk": "two words"}, text: "{{ secret.dmvpn.psk }}", want: "whitespace"},
		"a newline":       {values: map[string]string{"dmvpn.psk": "ok\nreload"}, text: "{{ secret.dmvpn.psk }}", want: "control"},
		"a help request":  {values: map[string]string{"dmvpn.psk": "abc?"}, text: "{{ secret.dmvpn.psk }}", want: "?"},
		"a store failure": {lookup: func(secretref.Ref) (string, bool, error) { return "", false, errors.New("store unreadable") }, text: "{{ secret.dmvpn.psk }}", want: "store unreadable"},
	} {
		lookup := c.lookup
		if lookup == nil {
			lookup = store(c.values)
		}
		_, _, err := secretref.Resolve(c.text, lookup)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, c.want)
			continue
		}
		for _, v := range c.values {
			if strings.Contains(err.Error(), v) {
				t.Errorf("%s: the error carries the value: %v", name, err)
			}
		}
	}
}

// TestProblems_OnlyExactReferencesStandAlone: a reference with a filter, an
// upper case name, or another expression beside it is refused; a plain
// reference and text without one are not.
func TestProblems_OnlyExactReferencesStandAlone(t *testing.T) {
	for text, bad := range map[string]bool{
		"key {{ secret.dmvpn.psk }}":                           false,
		"no template here":                                     false,
		"{{ vars.site }} only":                                 false,
		"key {{ secret.dmvpn.psk | quote }}":                   true,
		"key {{ secret.DMVPN.psk }}":                           true,
		"key {{ secret.dmvpn }}":                               true,
		"key {{- secret.dmvpn.psk }}":                          true,
		"{{ vars.peer }} key {{ secret.dmvpn.psk }}":           true,
		"key {{ secret.dmvpn.psk }}{{ secret.dmvpn.other }}":   false,
		"{% if vars.x %}key {{ secret.dmvpn.psk }}{% endif %}": true,
		"key {{ secret.dmvpn.psk }} {# a comment #}":           true,
	} {
		if got := len(secretref.Problems(text)) > 0; got != bad {
			t.Errorf("Problems(%q) = %v, want refused %v", text, secretref.Problems(text), bad)
		}
	}
	if _, _, err := secretref.Resolve("{{ vars.x }} {{ secret.a.b }}", store(map[string]string{"a.b": "v"})); err == nil {
		t.Error("Resolve substituted beside another expression")
	}
}

// FuzzResolve: no input makes Resolve panic, a resolved text never holds a
// reference or a value CheckValue refuses, and every value it returns came
// from the lookup.
func FuzzResolve(f *testing.F) {
	for _, s := range []string{"{{ secret.a.b }}", "x {{secret.a.b}} y", "{{ secret.", "{{ secret.a.b | quote }}", "{{{{ secret.a.b }}}}", "\x00{{ secret.zz.b }}"} {
		f.Add(s)
	}
	lookup := store(map[string]string{"a.b": "VALUE1", "zz.b": "VALUE2"})
	f.Fuzz(func(t *testing.T, text string) {
		got, values, err := secretref.Resolve(text, lookup)
		if err != nil {
			return
		}
		if len(secretref.Find(got)) > 0 && len(secretref.Find(text)) == 0 {
			t.Fatalf("resolution made a reference: %q from %q", got, text)
		}
		for _, v := range values {
			if v != "VALUE1" && v != "VALUE2" {
				t.Fatalf("a value the lookup never gave: %q", v)
			}
		}
	})
}
