// Tests for the tls.certificate.* family over real handshakes on loopback:
// one certificate seen is recorded as stats and facts with the warnings it
// calls for, none records nothing, more than one records none, and a store
// that refuses a value fails the read.
package tlsfacts_test

import (
	"context"
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
)

// recorder is a task's context that keeps what is recorded, failing the
// failAt'th write when it is set.
type recorder struct {
	stats, facts map[string]any
	writes       int
	failAt       int
}

// InjectSecrets implements sdk.RunbookContext.
func (r *recorder) InjectSecrets() map[string]string { return nil }

// SetStat implements sdk.RunbookContext.
func (r *recorder) SetStat(key string, value any) error {
	if r.writes++; r.writes == r.failAt {
		return errors.New("the stat store is full")
	}
	r.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (r *recorder) EmitFact(key string, value any) error {
	if r.writes++; r.writes == r.failAt {
		return errors.New("the fact store is full")
	}
	r.facts[key] = value
	return nil
}

// seenOn returns a Presented holding the certificate of each server, read
// through one configuration pinning every one of them.
func seenOn(t *testing.T, servers ...*httptest.Server) (devicetls.Settings, *devicetls.Presented) {
	t.Helper()
	var pins []any
	for _, srv := range servers {
		pins = append(pins, devicetls.Fingerprint(srv.Certificate()))
	}
	props := map[string]inventory.PropertyValue{}
	if len(pins) > 0 {
		props[devicetls.FingerprintProperty] = pins
	}
	settings, err := devicetls.Parse(inventory.NewProperties(props))
	if err != nil {
		t.Fatal(err)
	}
	ctx, seen := devicetls.WithPresented(context.Background())
	cfg, err := settings.ConfigContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: seen.Transport(&http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}), Timeout: 10 * time.Second}
	for _, srv := range servers {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	return settings, seen
}

// newServer starts a TLS server on loopback with a self-signed certificate
// of its own (httptest's built-in one is shared by every server).
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "device.lab.invalid"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// TestRecord_OneCertificateIsRecordedAsStatsAndFacts: every key is recorded
// both ways with the certificate's own values, and a certificate this far
// from its last day calls for no warning.
func TestRecord_OneCertificateIsRecordedAsStatsAndFacts(t *testing.T) {
	srv := newServer(t)
	settings, seen := seenOn(t, srv)
	rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
	warnings, err := tlsfacts.Record(rc, "api1", settings, seen, time.Now())
	if err != nil || warnings != nil {
		t.Fatalf("warnings %q, %v", warnings, err)
	}
	cert := srv.Certificate()
	for key, want := range map[string]any{
		tlsfacts.FactFingerprint: devicetls.Fingerprint(cert), tlsfacts.FactKeyFingerprint: devicetls.KeyFingerprint(cert),
		tlsfacts.FactSubject: cert.Subject.String(), tlsfacts.FactIssuer: cert.Issuer.String(), tlsfacts.FactNotAfter: cert.NotAfter.UTC().Format(time.RFC3339),
	} {
		if rc.stats[key] != want || rc.facts[key] != want {
			t.Errorf("%s: stat %v, fact %v, want %v", key, rc.stats[key], rc.facts[key], want)
		}
	}
	// A read near the certificate's last day says so.
	if w, _ := tlsfacts.Record(rc, "api1", settings, seen, cert.NotAfter.Add(-24*time.Hour)); len(w) != 1 || !strings.Contains(w[0], "expires on") {
		t.Errorf("a day from the end: warnings %q", w)
	}
	if got := len(tlsfacts.Returns()); got != len(tlsfacts.FactKeys) {
		t.Errorf("%d returns documented for %d facts", got, len(tlsfacts.FactKeys))
	}
}

// TestRecord_NoneOrMoreThanOneRecordsNothing: a read that saw no
// certificate records nothing, and one that saw two records none and says
// why.
func TestRecord_NoneOrMoreThanOneRecordsNothing(t *testing.T) {
	settings, none := seenOn(t)
	rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
	if w, err := tlsfacts.Record(rc, "api1", settings, none, time.Now()); w != nil || err != nil || len(rc.facts)+len(rc.stats) != 0 {
		t.Errorf("no certificate: warnings %q, %v, recorded %v %v", w, err, rc.stats, rc.facts)
	}
	settings, two := seenOn(t, newServer(t), newServer(t))
	w, err := tlsfacts.Record(rc, "api1", settings, two, time.Now())
	if err != nil || len(w) != 1 || !strings.Contains(w[0], "more than one certificate") || len(rc.facts) != 0 {
		t.Errorf("two certificates: warnings %q, %v, facts %v", w, err, rc.facts)
	}
}

// TestRecord_AStoreThatRefusesFailsTheRead: a stat or a fact the store
// refuses comes back as the read's error.
func TestRecord_AStoreThatRefusesFailsTheRead(t *testing.T) {
	settings, seen := seenOn(t, newServer(t))
	for _, failAt := range []int{1, 2} {
		rc := &recorder{stats: map[string]any{}, facts: map[string]any{}, failAt: failAt}
		if _, err := tlsfacts.Record(rc, "api1", settings, seen, time.Now()); err == nil {
			t.Errorf("write %d refused, and the read succeeded", failAt)
		}
	}
}
