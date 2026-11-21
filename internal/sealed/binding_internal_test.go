// A key-sealed file's headers are bound into its encryption (binding, HPKE's
// info). OpenWithKey checks the Recipient header against the key's own
// fingerprint before it decrypts anything, so from outside the package a
// changed header is always refused by that check first; this test steps
// around it to prove the binding itself refuses the change, even under the
// very key the file was sealed to.
package sealed

import (
	"crypto/hpke"
	"maps"
	"testing"
)

func TestBinding_AChangedRecipientDoesNotOpenWithTheKeyItWasSealedTo(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	data, err := SealToKey(k.Public(), PurposeFactBatch, []byte("facts sealed to k"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	// The control: under the headers it was sealed with, the file opens.
	if got, err := hpke.Open(k.key, hpke.HKDFSHA256(), hpke.AES256GCM(), binding(f.headers), f.body); err != nil || string(got) != "facts sealed to k" {
		t.Fatalf("the control failed: the file does not open under its own headers: %q, %v", got, err)
	}
	for name, recipient := range map[string]string{
		"another key's fingerprint": other.Public().Fingerprint(),
		"an empty recipient":        "",
	} {
		changed := maps.Clone(f.headers)
		changed[headerRecipient] = recipient
		if _, err := hpke.Open(k.key, hpke.HKDFSHA256(), hpke.AES256GCM(), binding(changed), f.body); err == nil {
			t.Errorf("%s: the file opened with the key it was sealed to under a changed Recipient header", name)
		}
	}
}
