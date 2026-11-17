// Tests for recording a certificate on first use, through the real
// onboarding path and file inventory, against real servers on loopback
// whose self-signed certificate a test can change: recorded only when asked,
// trusted with a warning until confirmed, never in place of what a record
// already trusts, and never with an onboarding that then fails.
package onboard

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
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
)

// deviceCert is a device's own certificate for 127.0.0.1, valid until
// notAfter, and its parsed form.
func deviceCert(t *testing.T, notAfter time.Time) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "device.lab.invalid", Organization: []string{"Brownfield"}},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

// swappable is an HTTPS API on loopback whose certificate a test changes
// between onboardings, as a device does when it issues a new one, and which
// counts the requests that carried its credential.
type swappable struct {
	*httptest.Server
	cert   atomic.Pointer[tls.Certificate]
	logins atomic.Int32
}

// newSwappable starts one presenting cert.
func newSwappable(t *testing.T, cert tls.Certificate) *swappable {
	t.Helper()
	s := &swappable{}
	s.cert.Store(&cert)
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "api" || pass != "api-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.logins.Add(1)
		_, _ = w.Write([]byte("{}"))
	}))
	// Asked on every handshake, unlike GetCertificate, which a client
	// dialing an address (and so sending no SNI) never reaches once
	// Certificates is set.
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*s.cert.Load()}}, nil
	}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// present makes s present cert from its next handshake.
func (s *swappable) present(cert tls.Certificate) {
	s.cert.Store(&cert)
	s.CloseClientConnections()
}

// apiLogin is the credential swappable accepts.
var apiLogin = map[string]string{"username": "api", "password": "api-secret"}

// onboardOnce onboards name in repo with creds, recording on first use when
// firstUse is set.
func onboardOnce(repo inv.Repository, name string, creds map[string]string, firstUse bool) (Result, error) {
	return onboardWith(context.Background(), repo, name, staticSecrets(creds), fixedNow, Lookup, options{firstUse: firstUse})
}

// hasWarning reports whether any of warnings says want.
func hasWarning(warnings []string, want string) bool {
	return slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, want) })
}

// stored returns name's stored record's text property key.
func stored(t *testing.T, repo inv.Repository, name, key string) string {
	t.Helper()
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := item.Properties().String(key)
	return v
}

// TestOnboard_FirstUseRecordsConfirmsAndThenVerifies follows one device's
// self-signed certificate from a refusal, through a pin recorded on first use
// (asked for, warned about, listed with both of its forms), a changed
// certificate refused under it, a person's confirmation, to a pin a person
// checked that a changed certificate is refused under too.
func TestOnboard_FirstUseRecordsConfirmsAndThenVerifies(t *testing.T) {
	first, firstCert := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	other, otherCert := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := newSwappable(t, first)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic})
	pin := devicetls.Fingerprint(firstCert)

	// Not asked for: refused, naming the certificate, nothing recorded, and
	// no credential sent.
	var untrusted *devicetls.UntrustedError
	if _, err := onboardOnce(repo, "api1", apiLogin, false); !errors.Is(err, ErrProbe) || !errors.As(err, &untrusted) || untrusted.Fingerprint != pin {
		t.Fatalf("without --tls-first-use: %v, want the certificate refused by its fingerprint", err)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != "" || srv.logins.Load() != 0 {
		t.Fatalf("a refused onboarding recorded %q or sent the credential %d times", got, srv.logins.Load())
	}

	// Asked for: recorded, listed for review, warned about, and the device
	// proved through it.
	res, err := onboardOnce(repo, "api1", apiLogin, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "active" || res.FirstUse == nil || res.FirstUse.Fingerprint != pin || res.FirstUse.KeyFingerprint != devicetls.KeyFingerprint(firstCert) ||
		!strings.Contains(res.FirstUse.Subject, "device.lab.invalid") || res.FirstUse.NotAfter != "2030-01-01" || !strings.Contains(res.FirstUse.Untrusted, "unknown authority") {
		t.Fatalf("result %+v, first use %+v", res, res.FirstUse)
	}
	if !hasWarning(res.Warnings, "recorded the certificate it presented as its pin on first use") || !hasWarning(res.Warnings, "no person has confirmed") {
		t.Errorf("warnings %q do not say a pin was recorded on first use and is unconfirmed", res.Warnings)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != pin || stored(t, repo, "api1", devicetls.FingerprintProperty) != "" {
		t.Errorf("recorded first use %q, tls_fingerprint %q; want the pin as first use alone", got, stored(t, repo, "api1", devicetls.FingerprintProperty))
	}
	if res.Facts[factCertificateFingerprint] != pin || res.Facts[factCertificateKeyFingerprint] != devicetls.KeyFingerprint(firstCert) || res.Facts[factCertificateNotAfter] != "2030-01-01T00:00:00Z" {
		t.Errorf("certificate facts %v", res.Facts)
	}

	// Again with nothing asked: trusted through the first-use pin, said to
	// be unconfirmed, nothing written.
	again, err := onboardOnce(repo, "api1", apiLogin, false)
	if err != nil || again.Changed || again.FirstUse != nil || !hasWarning(again.Warnings, "no person has confirmed") {
		t.Errorf("onboarding again: %+v, %v", again, err)
	}

	// A changed certificate under the first-use pin is refused, asked or not.
	srv.present(other)
	if _, err := onboardOnce(repo, "api1", apiLogin, true); !errors.As(err, &untrusted) || untrusted.Fingerprint != devicetls.Fingerprint(otherCert) ||
		!strings.Contains(err.Error(), "never replaces") || !strings.Contains(err.Error(), devicetls.FirstUseProperty) {
		t.Fatalf("a changed certificate with --tls-first-use: %v, want it refused and the first-use pin kept", err)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != pin {
		t.Fatalf("the first-use pin became %q", got)
	}

	// A person confirms it: the pin is theirs, the discovery holds, and
	// nothing says it is unconfirmed any more.
	srv.present(first)
	item, _ := repo.GetByName(context.Background(), "api1")
	if err := item.(interface{ ConfirmFirstUsePin(string, string) error }).ConfirmFirstUsePin(pin, devicetls.FingerprintProperty); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	confirmed, err := onboardOnce(repo, "api1", apiLogin, false)
	if err != nil || confirmed.Changed || hasWarning(confirmed.Warnings, "no person has confirmed") || !hasWarning(confirmed.Warnings, "trusts one pinned certificate") {
		t.Errorf("after confirming: %+v, %v", confirmed, err)
	}
	reread, _ := repo.GetByName(context.Background(), "api1")
	if !reread.HasCapability(capability.NameHTTPAPI) || stored(t, repo, "api1", inventory.TLSFirstUseProperty) != "" {
		t.Error("the confirmed device lost its discovery, or kept its first-use pin")
	}

	srv.present(other)
	if _, err := onboardOnce(repo, "api1", apiLogin, true); !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "names "+devicetls.FingerprintProperty) {
		t.Errorf("a changed certificate under a confirmed pin: %v, want it refused", err)
	}
}

// TestOnboard_FirstUseNeverReplacesAnAuthority: a device whose record pins
// an authority that did not sign the certificate presented is refused with
// --tls-first-use too, and nothing is recorded.
func TestOnboard_FirstUseNeverReplacesAnAuthority(t *testing.T) {
	cert, _ := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := newSwappable(t, cert)
	unrelated := apiServer(t)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{
		generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic, devicetls.CAPEMProperty: caPEM(unrelated),
	})
	// Through the exported entry point and option, as the CLI and the
	// Controller call it.
	if _, err := Onboard(context.Background(), repo, "api1", staticSecrets(apiLogin), fixedNow, WithFirstUsePins()); err == nil || !strings.Contains(err.Error(), "names "+devicetls.CAPEMProperty) {
		t.Fatalf("err %v, want a refusal naming the pinned authority", err)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != "" || srv.logins.Load() != 0 {
		t.Errorf("recorded %q, sent the credential %d times", got, srv.logins.Load())
	}
}

// TestOnboard_FirstUseRecordsNothingWhenTheProbeThenFails: the certificate
// is trusted for the second probe, the API refuses the credential, and no
// pin is recorded, since onboarding proved nothing through it.
func TestOnboard_FirstUseRecordsNothingWhenTheProbeThenFails(t *testing.T) {
	cert, _ := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := newSwappable(t, cert)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic})
	res, err := onboardOnce(repo, "api1", map[string]string{"username": "api", "password": "wrong"}, true)
	if !errors.Is(err, ErrProbe) || !strings.Contains(err.Error(), "refused the credential") || res.FirstUse != nil {
		t.Fatalf("err %v, first use %+v; want the credential refused and nothing listed", err, res.FirstUse)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != "" {
		t.Errorf("a failed onboarding recorded the pin %q", got)
	}
}

// TestOnboard_RemovesAFirstUsePinTheRecordOverrides: a person who pinned the
// certificate by hand beside a first-use pin overrides it, and the next
// onboarding removes it and says so.
func TestOnboard_RemovesAFirstUsePinTheRecordOverrides(t *testing.T) {
	cert, parsed := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := newSwappable(t, cert)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{
		generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic, devicetls.FingerprintProperty: devicetls.KeyFingerprint(parsed),
	})
	item, _ := repo.GetByName(context.Background(), "api1")
	item.(firstUseWriter).RecordFirstUsePin("sha256:" + strings.Repeat("ab", 32))
	if err := repo.Save(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	res, err := onboardOnce(repo, "api1", apiLogin, false)
	if err != nil || !hasWarning(res.Warnings, "removed the pin recorded on first use") || !res.Changed {
		t.Fatalf("%+v, %v; want the unused first-use pin removed", res, err)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != "" {
		t.Errorf("the unused first-use pin %q is still recorded", got)
	}
}

// TestOnboard_RecordsTheCertificateAndWarnsBeforeItExpires: a device pinned
// through a rotation presents a certificate ten days from its last day; the
// certificate is recorded, its expiry is warned about in a pin's words, and
// the list's other pin is named as one to retire.
func TestOnboard_RecordsTheCertificateAndWarnsBeforeItExpires(t *testing.T) {
	cert, parsed := deviceCert(t, fixedNow().Add(10*24*time.Hour))
	srv := newSwappable(t, cert)
	retired := "sha256:" + strings.Repeat("cd", 32)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{
		generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic,
		devicetls.FingerprintProperty: []any{retired, devicetls.Fingerprint(parsed)},
	})
	res, err := onboardOnce(repo, "api1", apiLogin, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Facts[factCertificateFingerprint] != devicetls.Fingerprint(parsed) || res.Facts[factCertificateSubject] == "" || res.Facts[factCertificateIssuer] == "" {
		t.Errorf("facts %v", res.Facts)
	}
	if !hasWarning(res.Warnings, "expires on "+parsed.NotAfter.Format("2006-01-02")+", in 10 days") || !hasWarning(res.Warnings, "pin "+retired+" does not match") {
		t.Errorf("warnings %q, want the expiry and the pin to retire", res.Warnings)
	}
}

// TestCertificateFacts_MoreThanOneCertificateRecordsNone: two certificates
// seen at one address record no certificate fact, and say why.
func TestCertificateFacts_MoreThanOneCertificateRecordsNone(t *testing.T) {
	a, aCert := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	b, bCert := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := newSwappable(t, a)
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: []any{devicetls.Fingerprint(aCert), devicetls.Fingerprint(bCert)}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, seen := devicetls.WithPresented(context.Background())
	cfg, err := settings.ConfigContext(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: seen.Transport(&http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true})}
	for _, cert := range []tls.Certificate{a, b} {
		srv.present(cert)
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	facts := map[string]any{}
	if w := certificateFacts("api1", settings, seen, fixedNow(), facts); len(facts) != 0 || !hasWarning(w, "more than one certificate") {
		t.Errorf("facts %v, warnings %q", facts, w)
	}
	_, none := devicetls.WithPresented(context.Background())
	if w := certificateFacts("api1", settings, none, fixedNow(), facts); w != nil || len(facts) != 0 {
		t.Errorf("no handshake: facts %v, warnings %q", facts, w)
	}
}

// TestWithFirstUsePin_RefusesATypeThatTakesNoPin: a Kubernetes cluster, which
// is verified against its authority alone, and an SSH device, which reads no
// TLS from its record, cannot have a pin recorded.
func TestWithFirstUsePin_RefusesATypeThatTakesNoPin(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	factory := inv.NewItemFactory()
	for typ, props := range map[string]map[string]inventory.PropertyValue{
		kubernetes.TypeCluster: {kubernetes.APIServerProperty: "https://k.example"},
		generic.TypeSSH:        {"host": "10.0.0.1"},
	} {
		item, err := factory.Build(record.Record{ID: "d", Name: "d", Type: typ, Properties: props})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := withFirstUsePin(item, pin); err == nil {
			t.Errorf("%s took a first-use pin", typ)
		}
	}
}

// TestOnboard_FirstUseOverGRPC: the gRPC probe names a certificate nothing
// trusts by its fingerprints, though gRPC flattens a failed handshake into a
// status, so onboarding records it on first use there too, and its facts;
// a handshake that failed for another reason is no certificate refusal.
func TestOnboard_FirstUseOverGRPC(t *testing.T) {
	cert, leaf := deviceCert(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	addr := grpcServer(t, srv)
	repo, _ := newRepo(t, "rpc1", generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: addr})
	pin := devicetls.Fingerprint(leaf)

	var untrusted *devicetls.UntrustedError
	if _, err := onboardOnce(repo, "rpc1", nil, false); !errors.As(err, &untrusted) || untrusted.Fingerprint != pin || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("without --tls-first-use: %v, want the certificate refused by its fingerprint", err)
	}
	res, err := onboardOnce(repo, "rpc1", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstUse == nil || res.FirstUse.Fingerprint != pin || stored(t, repo, "rpc1", inventory.TLSFirstUseProperty) != pin || res.Facts[factCertificateFingerprint] != pin {
		t.Errorf("first use %+v, facts %v; want %s recorded", res.FirstUse, res.Facts, pin)
	}

	other := &handshakeRecord{last: errors.New("connection reset by peer")}
	if err := other.refusedCertificate(); err != nil {
		t.Errorf("a reset read as a refused certificate: %v", err)
	}
}

// TestOnboard_FirstUseOverGNMIAndRESTCONF: the gNMI and RESTCONF probes,
// each through its real client to a real server whose certificate nothing in
// the record trusts, record the certificate on first use and its facts.
func TestOnboard_FirstUseOverGNMIAndRESTCONF(t *testing.T) {
	g := gnmitest.New(t, gnmiLogin)
	gRepo, _ := newRepo(t, "g1", generic.TypeGNMI, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: g.Addr()})
	r := restconftest.New(t, restconftest.Options{Username: "admin", Password: "restconf-Onboard-5"})
	rRepo, _ := newRepo(t, "r1", generic.TypeRestconf, map[string]inventory.PropertyValue{generic.BaseURLProperty: r.URL, generic.HTTPAuthProperty: httpapi.AuthBasic})
	for _, tc := range []struct {
		name  string
		repo  inv.Repository
		creds map[string]string
		cert  *x509.Certificate
	}{
		{"g1", gRepo, gnmitest.Secrets(g), g.Certificate()},
		{"r1", rRepo, restconftest.Secrets(r), r.Certificate()},
	} {
		if _, err := onboardOnce(tc.repo, tc.name, tc.creds, false); !errors.Is(err, ErrProbe) {
			t.Fatalf("%s without --tls-first-use: %v, want a refusal", tc.name, err)
		}
		res, err := onboardOnce(tc.repo, tc.name, tc.creds, true)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		pin := devicetls.Fingerprint(tc.cert)
		if res.FirstUse == nil || res.FirstUse.Fingerprint != pin || stored(t, tc.repo, tc.name, inventory.TLSFirstUseProperty) != pin || res.Facts[factCertificateFingerprint] != pin {
			t.Errorf("%s: first use %+v, facts %v; want %s recorded", tc.name, res.FirstUse, res.Facts, pin)
		}
	}
}
