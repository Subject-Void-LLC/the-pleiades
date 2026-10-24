// Tests of the IOS secret hashes against hashes a real router produced:
// IOS XE 17.15 (IOL XE in Cisco's DevNet CML sandbox) configured with
// "username pleiades-probe8 algorithm-type sha256 secret Pleiades-Vector-8"
// and "username pleiades-probe9 algorithm-type scrypt secret
// Pleiades-Vector-9" through pleiades' own net.ios.config, read back with
// net.ios.command and rolled back, on 2026-10-24. The passwords are test
// values, nothing else's.
package iossecret_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iossecret"
)

// The router's own hashes.
const (
	routerType8 = "$8$URbw2ETer2Avhk$g8QzJJjMMnAbQesJnpAr5Zu0JlWSUQxt3F6dy7VE2gI"
	routerType9 = "$9$Vuv3rBMXr3bt6E$PghQY6xhiskVREHTQbW/k.opzRjfJooPCrEyWr3w0D."
)

// TestHashWithSalt_MatchesTheRouter: under the router's own salt, each
// type gives exactly the hash the router wrote, and Verify accepts it for
// the password and refuses it for any other.
func TestHashWithSalt_MatchesTheRouter(t *testing.T) {
	for _, tc := range []struct {
		typ            int
		password, hash string
	}{
		{iossecret.Type8, "Pleiades-Vector-8", routerType8},
		{iossecret.Type9, "Pleiades-Vector-9", routerType9},
	} {
		salt := strings.Split(tc.hash, "$")[2]
		got, err := iossecret.HashWithSalt(tc.typ, tc.password, salt)
		if err != nil || got != tc.hash {
			t.Errorf("type %d = %q, %v; the router wrote %q", tc.typ, got, err, tc.hash)
		}
		if ok, err := iossecret.Verify(tc.password, tc.hash); !ok || err != nil {
			t.Errorf("Verify(type %d) = %v, %v", tc.typ, ok, err)
		}
		if ok, err := iossecret.Verify(tc.password+"x", tc.hash); ok || err != nil {
			t.Errorf("Verify(type %d, a wrong password) = %v, %v", tc.typ, ok, err)
		}
	}
}

// TestHash_NewSaltEachTime: two hashes of one password differ by their
// salt, each reads back, and each salt is SaltLen characters IOS accepts.
func TestHash_NewSaltEachTime(t *testing.T) {
	a, err := iossecret.Hash(iossecret.Type9, "same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := iossecret.Hash(iossecret.Type9, "same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(a, "$9$") || len(strings.Split(a, "$")[2]) != iossecret.SaltLen {
		t.Errorf("hashes %q and %q", a, b)
	}
	for _, h := range []string{a, b} {
		if ok, err := iossecret.Verify("same", h); !ok || err != nil {
			t.Errorf("Verify(%q) = %v, %v", h, ok, err)
		}
	}
	h8, err := iossecret.Hash(iossecret.Type8, "same")
	if err != nil || !strings.HasPrefix(h8, "$8$") || len(h8) != len(routerType8) {
		t.Errorf("type 8 = %q, %v", h8, err)
	}
}

// TestRefusals: an empty password, a salt of the wrong length or with a
// character outside Cisco's alphabet, a type other than 8 or 9, and a hash
// in any other form are each refused, never hashed or matched.
func TestRefusals(t *testing.T) {
	for name, call := range map[string]func() error{
		"empty password":           func() error { _, err := iossecret.HashWithSalt(iossecret.Type9, "", "Vuv3rBMXr3bt6E"); return err },
		"short salt":               func() error { _, err := iossecret.HashWithSalt(iossecret.Type9, "p", "abc"); return err },
		"salt with $":              func() error { _, err := iossecret.HashWithSalt(iossecret.Type9, "p", "Vuv3rBMXr3bt6$"); return err },
		"type 5":                   func() error { _, err := iossecret.HashWithSalt(5, "p", "Vuv3rBMXr3bt6E"); return err },
		"type 7 hash":              func() error { _, err := iossecret.Verify("p", "$7$Vuv3rBMXr3bt6E$x"); return err },
		"md5 crypt":                func() error { _, err := iossecret.Verify("p", "$1$abcd$efgh"); return err },
		"too few parts":            func() error { _, err := iossecret.Verify("p", "$9$Vuv3rBMXr3bt6E"); return err },
		"bad salt":                 func() error { _, err := iossecret.Verify("p", "$9$short$PghQ"); return err },
		"empty hash":               func() error { _, err := iossecret.Verify("p", ""); return err },
		"empty password, new salt": func() error { _, err := iossecret.Hash(iossecret.Type8, ""); return err },
	} {
		if call() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
