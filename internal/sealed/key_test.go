// Tests of deriving a seal key pair from a secret (DeriveKey): one pair per
// secret, so a service that holds the secret has its pair without storing it.
package sealed_test

import (
	"bytes"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

func TestDeriveKey_GivesOneKeyPairPerSecret(t *testing.T) {
	ikm := bytes.Repeat([]byte{3}, 32)
	a, err := sealed.DeriveKey(ikm)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := sealed.DeriveKey(ikm)
	c, _ := sealed.DeriveKey(bytes.Repeat([]byte{4}, 32))
	if a.Public().String() != b.Public().String() || a.Public().String() == c.Public().String() {
		t.Error("derivation is not one key pair per secret")
	}
	// What one derivation seals, the next derivation opens: the Controller
	// derives the same pair after a restart.
	file, err := sealed.SealToKey(a.Public(), sealed.PurposeFactDigest, []byte("dev\x00file.x\x00digest"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sealed.Parse(file)
	if err != nil || parsed.Purpose != sealed.PurposeFactDigest {
		t.Fatalf("Parse: %v, %v", parsed, err)
	}
	if got, err := parsed.OpenWithKey(b); err != nil || string(got) != "dev\x00file.x\x00digest" {
		t.Errorf("OpenWithKey = %q, %v", got, err)
	}
	if _, err := parsed.OpenWithKey(c); err == nil {
		t.Error("another key opened it")
	}
	if _, err := sealed.DeriveKey(make([]byte, 31)); err == nil {
		t.Error("31 bytes were enough to derive from")
	}
}
