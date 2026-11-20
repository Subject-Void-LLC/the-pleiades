// The at-rest keys are their own (atrest.go): none is the keyed-digest key,
// and none is another of the three.
package facts

import (
	"bytes"
	"testing"
)

// TestKeys_AreNotTheDigestKeyersKey: from one secret, the hash key, the token
// key and the keyed-digest key are four different keys, so nothing keyed for
// one purpose can be tested against another.
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
	all := map[string][]byte{"hash": keys.hash, "token": keys.token, "line": line, "digest": keyer.key}
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
}
