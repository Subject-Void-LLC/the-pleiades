// The at-rest keys are their own (atrest.go): none is the keyed-digest key,
// and none is another of the three.
package facts

import (
	"bytes"
	"testing"
)

// TestKeys_AreNotTheDigestKeyersKey: from one secret, the hash key, the token
// key, the chain key, the line key, the removal key and the keyed-digest key
// are six different keys, so nothing keyed for one purpose can be tested
// against another; and the keys' identity covers the chain key.
func TestKeys_AreNotTheDigestKeyersKey(t *testing.T) {
	secret := []byte("facts-at-rest-test-secret-32byte")
	keys := testKeys()
	keyer, err := NewKeyer(secret, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.load(); err != nil {
		t.Fatal(err)
	}
	line := derive(secret, atRestLineLabel)
	all := map[string][]byte{"hash": keys.hash, "token": keys.token, "chain": keys.chain, "line": line, "removal": keys.removal, "digest": keyer.key}
	for a, ka := range all {
		if len(ka) != 32 || bytes.Equal(ka, secret) {
			t.Errorf("the %s key is %d bytes, or the secret itself", a, len(ka))
		}
		for b, kb := range all {
			if a != b && bytes.Equal(ka, kb) {
				t.Errorf("the %s key is the %s key", a, b)
			}
		}
	}
	// The identity covers every key a row is written under: keys alike but
	// for any one of them have another identity.
	if keyID(keys.source, keys.hash, keys.token, keys.chain) != keys.ID() {
		t.Fatal("the control failed: keyID does not give the keys' own identity")
	}
	other := bytes.Repeat([]byte{1}, 32)
	for name, id := range map[string]string{
		"hash":  keyID(keys.source, other, keys.token, keys.chain),
		"token": keyID(keys.source, keys.hash, other, keys.chain),
		"chain": keyID(keys.source, keys.hash, keys.token, other),
	} {
		if id == keys.ID() {
			t.Errorf("keys with another %s key have the same identity", name)
		}
	}
}
