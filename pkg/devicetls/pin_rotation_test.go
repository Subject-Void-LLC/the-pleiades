// Tests for key pins and lists of pins against real TLS servers on
// loopback, each presenting a real self-signed certificate: one issued again
// with the same key (an upgrade, a new name, a yearly rollover), one with a
// new key (a rotation), one expired, and one for a name the client never
// dials.
package devicetls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// minted is one self-signed certificate and the key that signed it.
type minted struct {
	key  *ecdsa.PrivateKey
	der  []byte
	cert *x509.Certificate
}

// newKey makes a P-256 key.
func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// mint issues a self-signed certificate for name with key, valid until
// notAfter, as a device does when it makes its own.
func mint(t *testing.T, key *ecdsa.PrivateKey, serial int64, name string, notAfter time.Time) minted {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-400 * 24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		// A certificate for an address names it as one, as Go checks it.
		tmpl.DNSNames, tmpl.IPAddresses = nil, []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return minted{key: key, der: der, cert: cert}
}

// serveMinted starts a TLS server on loopback presenting m.
func serveMinted(t *testing.T, m minted) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{m.der}, PrivateKey: m.key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// pinning parses settings pinning pins, as tls_fingerprint holds them.
func pinning(t *testing.T, pins inventory.PropertyValue) devicetls.Settings {
	t.Helper()
	s, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: pins})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestKeyPin_SurvivesACertificateIssuedAgainWithTheSameKey: a device that
// issues its self-signed certificate again with the same key (new serial,
// new name, new dates) is refused by a pin of the old certificate, whose
// refusal names both forms of the new one, and reached by a pin of the key;
// a device that changed its key is refused by the old key's pin.
func TestKeyPin_SurvivesACertificateIssuedAgainWithTheSameKey(t *testing.T) {
	key := newKey(t)
	first := mint(t, key, 1, "switch1.lab.invalid", time.Now().Add(90*24*time.Hour))
	again := mint(t, key, 2, "switch1-renamed.lab.invalid", time.Now().Add(365*24*time.Hour))
	rotated := mint(t, newKey(t), 3, "switch1.lab.invalid", time.Now().Add(365*24*time.Hour))
	if devicetls.KeyFingerprint(first.cert) != devicetls.KeyFingerprint(again.cert) || devicetls.Fingerprint(first.cert) == devicetls.Fingerprint(again.cert) {
		t.Fatal("the certificates issued again do not share a key, or share a certificate")
	}

	srv := serveMinted(t, again)
	var untrusted *devicetls.UntrustedError
	err := get(t, pinning(t, devicetls.Fingerprint(first.cert)), srv)
	if !errors.As(err, &untrusted) || untrusted.Fingerprint != devicetls.Fingerprint(again.cert) || untrusted.KeyFingerprint != devicetls.KeyFingerprint(again.cert) ||
		!strings.Contains(err.Error(), "key fingerprint "+devicetls.KeyFingerprint(again.cert)) || !strings.Contains(err.Error(), "pin the key fingerprint instead") {
		t.Errorf("a certificate pin of the old certificate: %v, want a refusal naming both forms of the new one", err)
	}
	keyPin := pinning(t, strings.ToUpper(devicetls.KeyFingerprint(first.cert)))
	if err := get(t, keyPin, srv); err != nil {
		t.Errorf("the key pin refused a certificate for its key: %v", err)
	}
	if w := keyPin.Warnings("sw1"); len(w) != 1 || !strings.Contains(w[0], "trusts any certificate for one pinned public key") || !strings.Contains(w[0], devicetls.KeyFingerprint(first.cert)) {
		t.Errorf("a key pin's warning %q does not say it accepts any certificate for the key, or name it", w)
	}
	err = get(t, keyPin, serveMinted(t, rotated))
	if !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "its key fingerprint is not the pinned "+devicetls.KeyFingerprint(first.cert)) {
		t.Errorf("the old key's pin against a new key: %v, want a refusal naming the pinned key", err)
	}
}

// TestPinList_HoldsARotationThenRetiresItsOldPin: during a planned rotation
// the list holds the old and the new pin and both certificates are reached;
// the warning names every pin; a presented certificate names the pin it
// does not match as one to retire; and once the old pin is removed the old
// certificate is refused.
func TestPinList_HoldsARotationThenRetiresItsOldPin(t *testing.T) {
	old := mint(t, newKey(t), 1, "rtr.lab.invalid", time.Now().Add(20*24*time.Hour))
	replacement := mint(t, newKey(t), 2, "rtr.lab.invalid", time.Now().Add(400*24*time.Hour))
	oldSrv, newSrv := serveMinted(t, old), serveMinted(t, replacement)
	oldPin, newPin := devicetls.Fingerprint(old.cert), devicetls.Fingerprint(replacement.cert)

	for name, v := range map[string]inventory.PropertyValue{
		"a list":                  []any{oldPin, newPin},
		"a list of strings":       []string{oldPin, newPin},
		"text separated by comma": " " + oldPin + " , " + strings.ToUpper(newPin),
	} {
		both := pinning(t, v)
		if got := both.Pins(); len(got) != 2 || got[0] != oldPin || got[1] != newPin {
			t.Errorf("%s: the pins read as %q", name, got)
		}
		for which, srv := range map[string]*httptest.Server{"old": oldSrv, "new": newSrv} {
			if err := get(t, both, srv); err != nil {
				t.Errorf("%s: the %s certificate was refused during the rotation: %v", name, which, err)
			}
		}
		if w := both.Warnings("rtr"); len(w) != 1 || !strings.Contains(w[0], "trusts 2 pins") || !strings.Contains(w[0], oldPin) || !strings.Contains(w[0], newPin) {
			t.Errorf("%s: warnings %q do not name every pin", name, w)
		}
		if w := both.RetireWarnings("rtr", replacement.cert); len(w) != 1 || !strings.Contains(w[0], oldPin) || strings.Contains(w[0], "pin "+newPin) {
			t.Errorf("%s: the new certificate's retire warnings %q, want the old pin alone", name, w)
		}
	}
	if w := pinning(t, newPin).RetireWarnings("rtr", replacement.cert); w != nil {
		t.Errorf("one pin has nothing to retire, got %q", w)
	}

	retired := pinning(t, []any{newPin})
	if err := get(t, retired, newSrv); err != nil {
		t.Errorf("the new certificate was refused after the old pin was retired: %v", err)
	}
	var untrusted *devicetls.UntrustedError
	if err := get(t, retired, oldSrv); !errors.As(err, &untrusted) || untrusted.Fingerprint != oldPin {
		t.Errorf("the old certificate after its pin was retired: %v, want a refusal naming it", err)
	}
	three := pinning(t, []any{devicetls.Fingerprint(mint(t, newKey(t), 9, "x", time.Now().Add(time.Hour)).cert), newPin, devicetls.KeyFingerprint(old.cert)})
	if err := get(t, three, mint2server(t)); !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "it matches none of the 3 pinned") {
		t.Errorf("a certificate no pin of a list matches: %v, want a refusal naming the list", err)
	}
}

// mint2server serves a certificate no test pins.
func mint2server(t *testing.T) *httptest.Server {
	t.Helper()
	return serveMinted(t, mint(t, newKey(t), 42, "stranger.lab.invalid", time.Now().Add(time.Hour)))
}

// TestPin_IgnoresDatesAndNamesButSaysWhenTheCertificateExpires: a pinned
// certificate that expired, or names a host the client never dials, is
// reached, as a pin is the identity, and the expiry is warned about in words
// that fit how it is pinned.
func TestPin_IgnoresDatesAndNamesButSaysWhenTheCertificateExpires(t *testing.T) {
	now := time.Now()
	expired := mint(t, newKey(t), 1, "old.lab.invalid", now.Add(-24*time.Hour))
	soon := mint(t, newKey(t), 2, "elsewhere.lab.invalid", now.Add(10*24*time.Hour))
	later := mint(t, newKey(t), 3, "later.lab.invalid", now.Add(200*24*time.Hour))
	for name, m := range map[string]minted{"expired": expired, "another name": soon} {
		if err := get(t, pinning(t, devicetls.Fingerprint(m.cert)), serveMinted(t, m)); err != nil {
			t.Errorf("%s: the pinned certificate was refused: %v", name, err)
		}
	}
	certPin, keyPin := pinning(t, devicetls.Fingerprint(soon.cert)), pinning(t, devicetls.KeyFingerprint(soon.cert))
	defaults, err := parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		s    devicetls.Settings
		cert *x509.Certificate
		want string
	}{
		{"expired, pinned", pinning(t, devicetls.Fingerprint(expired.cert)), expired.cert, "expired on " + expired.cert.NotAfter.UTC().Format("2006-01-02") + ", and its pin accepts it anyway"},
		{"expired, not pinned", defaults, expired.cert, "it was accepted anyway"},
		{"soon, certificate pin", certPin, soon.cert, "its certificate pin refuses the renewed one until it is pinned too"},
		{"soon, key pin", keyPin, soon.cert, "its key pin accepts a renewed certificate for the same key"},
		{"soon, an authority", defaults, soon.cert, "renew it before then"},
		{"far off", certPin, later.cert, ""},
	} {
		got := tc.s.ExpiryWarning("dev", tc.cert, now)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want it to say %q", tc.name, got, tc.want)
		}
	}
	if got := certPin.ExpiryWarning("dev", soon.cert, now); !strings.Contains(got, "in 9 days") && !strings.Contains(got, "in 10 days") {
		t.Errorf("the warning %q does not say how many days are left", got)
	}
}

// TestPinList_Refusals: every malformed list is refused naming the
// property, before anything is dialed.
func TestPinList_Refusals(t *testing.T) {
	one := "sha256:" + strings.Repeat("ab", 32)
	nine := make([]any, 0, devicetls.MaxPins+1)
	for i := 0; i <= devicetls.MaxPins; i++ {
		nine = append(nine, "sha256:"+strings.Repeat(string("0123456789"[i]), 64))
	}
	for name, tc := range map[string]struct {
		v    inventory.PropertyValue
		want string
	}{
		"an empty list":               {[]any{}, "lists no pin"},
		"more than the bound":         {nine, "more than the 8"},
		"a pin twice":                 {[]any{one, strings.ToUpper(one)}, "twice"},
		"a pin twice in text":         {one + "," + one, "twice"},
		"an empty entry":              {one + ",,", "a pin is"},
		"a list entry not text":       {[]any{one, 7}, "must be text"},
		"a number":                    {12, "a pin or a list"},
		"a comma inside an entry":     {[]any{one + "," + one}, "a pin is"},
		"a key pin of the wrong size": {"spki-sha256:abcd", "a pin is"},
	} {
		_, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: tc.v})
		if err == nil || !strings.Contains(err.Error(), devicetls.FingerprintProperty) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming the property and saying %q", name, err, tc.want)
		}
	}
	if got := len(pinning(t, nine[:devicetls.MaxPins]).Pins()); got != devicetls.MaxPins {
		t.Errorf("a list of exactly the bound reads as %d pins", got)
	}
}

// TestNormalizePin_ReadsOnePinAsTheSettingsDo: either form in either case
// comes back as tls_fingerprint holds it, and anything else is refused.
func TestNormalizePin_ReadsOnePinAsTheSettingsDo(t *testing.T) {
	for in, want := range map[string]string{
		" SHA256:" + strings.Repeat("AB", 32):     "sha256:" + strings.Repeat("ab", 32),
		"Spki-Sha256:" + strings.Repeat("Cd", 32): "spki-sha256:" + strings.Repeat("cd", 32),
	} {
		if got, err := devicetls.NormalizePin(in); err != nil || got != want {
			t.Errorf("NormalizePin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", strings.Repeat("ab", 32), "sha256:" + strings.Repeat("ab", 31), "sha1:" + strings.Repeat("ab", 20)} {
		if _, err := devicetls.NormalizePin(bad); err == nil {
			t.Errorf("NormalizePin(%q) accepted it", bad)
		}
	}
}
