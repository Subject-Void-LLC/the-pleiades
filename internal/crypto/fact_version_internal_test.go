// Tests of reading which master key version a fact payload names
// (FactPayloadVersion) and which versions a service holds (CurrentVersion,
// PreviousVersion): the version read is the one a service needs to open the
// payload, and nothing that is not the sealed form names one.
package crypto

import (
	"errors"
	"testing"
)

func TestFactPayloadVersion_NamesTheVersionThatOpensIt(t *testing.T) {
	rotating := factService(t, 'b', "v2", 'a', "v1")
	if rotating.CurrentVersion() != "v2" || rotating.PreviousVersion() != "v1" {
		t.Fatalf("the rotating service holds %q and %q, want v2 and v1", rotating.CurrentVersion(), rotating.PreviousVersion())
	}
	old := factService(t, 'a', "v1", 0, "")
	if old.PreviousVersion() != "" {
		t.Fatalf("a service with no previous key names previous version %q", old.PreviousVersion())
	}
	for _, c := range []struct {
		name string
		svc  *EnvelopeService
		want string
	}{{"sealed under the old key", old, "v1"}, {"sealed under the new key", rotating, "v2"}} {
		sealed, err := SealFactPayload(c.svc, "token", map[string]interface{}{"key": "k", "json": "1"})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := FactPayloadVersion(sealed)
		if !ok || got != c.want {
			t.Errorf("%s: version %q, %v; want %q", c.name, got, ok, c.want)
		}
		// The control: the service holding that version opens it, and the
		// one that does not hold it refuses it by version.
		if _, err := OpenFactPayload(rotating, "token", sealed); err != nil {
			t.Errorf("%s: the service holding %s does not open it: %v", c.name, got, err)
		}
		if got == "v2" {
			if _, err := OpenFactPayload(old, "token", sealed); !errors.Is(err, ErrUnknownKeyVersion) {
				t.Errorf("%s: a service without %s opened it or refused it otherwise: %v", c.name, got, err)
			}
		}
	}
	for _, payload := range []map[string]interface{}{
		{"key": "k", "json": "1"},
		{EncryptedKeyMarker: "v1$AES256GCM$abc$def"},
		{EncryptedKeyMarker: 7},
	} {
		if got, ok := FactPayloadVersion(payload); ok {
			t.Errorf("a payload that is not sealed, %v, names version %q", payload, got)
		}
	}
}
