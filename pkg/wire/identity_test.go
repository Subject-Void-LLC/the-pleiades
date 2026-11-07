// Tests for CollectionIdentity: its zero value writes no key under omitzero,
// and Valid accepts exactly the shapes a build writes.
package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// validDigest is a well-formed manifest digest.
var validDigest = ManifestDigestPrefix + strings.Repeat("0a", 32)

// TestCollectionIdentity_ZeroWritesNoKey: a carrier with omitzero leaves a
// zero identity out entirely, so a record written before the field reads the
// same bytes, and a set identity writes its three keys.
func TestCollectionIdentity_ZeroWritesNoKey(t *testing.T) {
	type carrier struct {
		Name       string             `json:"name"`
		Collection CollectionIdentity `json:"collection,omitzero"`
	}
	zero, err := json.Marshal(carrier{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if string(zero) != `{"name":"a"}` {
		t.Errorf("zero identity wrote %s, want no collection key", zero)
	}
	set, err := json.Marshal(carrier{Name: "a", Collection: CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: validDigest}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"a","collection":{"group":"file","version":"0.4.0","manifests":"` + validDigest + `"}}`
	if string(set) != want {
		t.Errorf("set identity wrote %s, want %s", set, want)
	}
	// An external program's identity has no version, and writes no version key.
	external, err := json.Marshal(CollectionIdentity{Group: "acme", Manifests: validDigest})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(external), "version") {
		t.Errorf("an identity with no version wrote %s", external)
	}
	if !(CollectionIdentity{}).IsZero() || (CollectionIdentity{Group: "file"}).IsZero() {
		t.Error("IsZero is not true exactly for the zero identity")
	}
}

// TestCollectionIdentity_Valid accepts a built-in's and an external
// program's identity and refuses each malformed field by name.
func TestCollectionIdentity_Valid(t *testing.T) {
	good := []CollectionIdentity{
		{Group: "file", Version: "0.4.0", Manifests: validDigest},
		{Group: "virt.clab", Version: "0.0.0-dev+0123456789ab.dirty", Manifests: validDigest},
		{Group: "acme-widgets", Manifests: validDigest},
		{Group: strings.Repeat("g", MaxIdentityGroupBytes), Version: strings.Repeat("1", MaxIdentityVersionBytes), Manifests: validDigest},
	}
	for _, id := range good {
		if err := id.Valid(); err != nil {
			t.Errorf("%+v: %v", id, err)
		}
	}
	bad := map[string]CollectionIdentity{
		"zero":                {},
		"no group":            {Version: "0.4.0", Manifests: validDigest},
		"long group":          {Group: strings.Repeat("g", MaxIdentityGroupBytes+1), Manifests: validDigest},
		"group with a slash":  {Group: "a/b", Manifests: validDigest},
		"group with a NUL":    {Group: "a\x00b", Manifests: validDigest},
		"group not UTF-8":     {Group: "a\xffb", Manifests: validDigest},
		"group with escape":   {Group: "acme\x1b[2J", Program: true, Manifests: validDigest},
		"group with newline":  {Group: "acme\n", Program: true, Manifests: validDigest},
		"group with override": {Group: "acme\u202e", Program: true, Manifests: validDigest},
		"long version":        {Group: "file", Version: strings.Repeat("1", MaxIdentityVersionBytes+1), Manifests: validDigest},
		"version with space":  {Group: "file", Version: "0.4.0 rc", Manifests: validDigest},
		"version with escape": {Group: "file", Version: "0.4.0\x1b[2J", Manifests: validDigest},
		"no digest":           {Group: "file", Version: "0.4.0"},
		"63 hex digits":       {Group: "file", Manifests: validDigest[:len(validDigest)-1]},
		"upper-case hex":      {Group: "file", Manifests: validDigest[:len(validDigest)-1] + "A"},
		"upper-case prefix":   {Group: "file", Manifests: "SHA256:" + strings.Repeat("0a", 32)},
		"another algorithm":   {Group: "file", Manifests: "sha512:" + strings.Repeat("0a", 32)},
	}
	for name, id := range bad {
		if err := id.Valid(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("%s: Valid gave %v, want ErrInvalidIdentity", name, err)
		}
	}
}
