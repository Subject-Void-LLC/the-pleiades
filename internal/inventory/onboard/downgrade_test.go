// Tests that onboarding never reaches a device below what its record
// allows: the HTTP probe against servers that speak only a deprecated
// version or only a legacy suite, with and without each flag; the gRPC
// probe against a server capped below TLS 1.2; and a credential over plain
// HTTP, only with its flag and always with the rotation warning.
package onboard

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// oldServer is an HTTPS API restricted to one version range and, when
// suites is set, to those cipher suites.
func oldServer(t *testing.T, minV, maxV uint16, suites []uint16) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: minV, MaxVersion: maxV, CipherSuites: suites}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// httpDevice is a generic_http device for srv, its certificate pinned,
// with extra settings.
func httpDevice(t *testing.T, srv *httptest.Server, extra map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL, devicetls.CAPEMProperty: caPEM(srv)}
	for k, v := range extra {
		props[k] = v
	}
	return build(t, generic.TypeHTTP, props)
}

// TestHTTPProbe_NoDowngradeUnlessAllowed: a deprecated version and a
// legacy suite are each reached only through their own flag, each then
// with its warning, and a modern server negotiates TLS 1.3 whatever the
// record allows.
func TestHTTPProbe_NoDowngradeUnlessAllowed(t *testing.T) {
	tls10 := oldServer(t, tls.VersionTLS10, tls.VersionTLS10, nil)
	desOnly := oldServer(t, tls.VersionTLS12, tls.VersionTLS12, []uint16{tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA})
	modern := oldServer(t, tls.VersionTLS12, tls.VersionTLS13, nil)
	deprecated := map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true}
	legacy := map[string]inventory.PropertyValue{devicetls.AllowLegacyCiphersProperty: true}
	both := map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true, devicetls.AllowLegacyCiphersProperty: true}

	for _, tc := range []struct {
		name     string
		srv      *httptest.Server
		props    map[string]inventory.PropertyValue
		reaches  bool
		version  string
		warnings []string
	}{
		{"default vs TLS 1.0", tls10, nil, false, "", nil},
		{"legacy ciphers vs TLS 1.0", tls10, legacy, false, "", nil},
		{"deprecated vs TLS 1.0", tls10, deprecated, true, "TLS 1.0", []string{"RFC 8996"}},
		{"default vs 3DES", desOnly, nil, false, "", nil},
		{"deprecated vs 3DES", desOnly, deprecated, false, "", nil},
		{"legacy ciphers vs 3DES", desOnly, legacy, true, "TLS 1.2", []string{devicetls.AllowLegacyCiphersProperty}},
		{"both vs modern", modern, both, true, "TLS 1.3", []string{"RFC 8996", devicetls.AllowLegacyCiphersProperty}},
		{"default vs modern", modern, nil, true, "TLS 1.3", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := httpProber{}.Probe(context.Background(), httpDevice(t, tc.srv, tc.props), nil)
			if (err == nil) != tc.reaches {
				t.Fatalf("reached=%v (err %v), want %v", err == nil, err, tc.reaches)
			}
			if !tc.reaches {
				return
			}
			if got.Facts["tls_version"] != tc.version {
				t.Errorf("negotiated %v, want %s", got.Facts["tls_version"], tc.version)
			}
			if len(got.Warnings) != len(tc.warnings) {
				t.Fatalf("warnings %q, want one for each of %q", got.Warnings, tc.warnings)
			}
			for i, want := range tc.warnings {
				if !strings.Contains(got.Warnings[i], want) {
					t.Errorf("warning %q does not name %q", got.Warnings[i], want)
				}
			}
		})
	}
}

// TestHTTPProbe_PlaintextCredentialOnlyWhenAllowed: a credential over
// http:// is refused when the device is built without the flag, and with
// it the credential is sent and the warning says to rotate it.
func TestHTTPProbe_PlaintextCredentialOnlyWhenAllowed(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	ctor, _ := record.LookupType(generic.TypeHTTP)
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic}
	if _, err := ctor(record.Record{Name: "api1", Type: generic.TypeHTTP, Properties: props}); err == nil || !strings.Contains(err.Error(), httpapi.AllowPlaintextCredentialsProperty) {
		t.Fatalf("a plain-HTTP credential without its flag: %v", err)
	}
	props[httpapi.AllowPlaintextCredentialsProperty] = true
	dev := build(t, generic.TypeHTTP, props)
	got, err := httpProber{}.Probe(context.Background(), dev, map[string]string{"username": "u", "password": "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sent, "Basic ") {
		t.Errorf("the server saw authorization %q", sent)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "rotate the credential") {
		t.Errorf("warnings %q", got.Warnings)
	}
}

// TestGRPCProbe_NoDowngrade: a gRPC server capped below TLS 1.2 is not
// reached, and a gRPC device cannot be given either weakening flag.
func TestGRPCProbe_NoDowngrade(t *testing.T) {
	cert, _ := selfSigned(t)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11})))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	addr := grpcServer(t, srv)
	dev := build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: addr, devicetls.CAPEMProperty: certPEM(cert)})
	if _, err := (grpcProber{}).Probe(context.Background(), dev, nil); err == nil {
		t.Fatal("a gRPC server capped at TLS 1.1 was reached")
	}
	ctor, _ := record.LookupType(generic.TypeGRPC)
	for _, extra := range []map[string]inventory.PropertyValue{
		{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true},
		{devicetls.AllowLegacyCiphersProperty: true},
	} {
		props := map[string]inventory.PropertyValue{generic.GRPCTargetProperty: addr}
		for k, v := range extra {
			props[k] = v
		}
		if _, err := ctor(record.Record{Name: "g1", Type: generic.TypeGRPC, Properties: props}); err == nil || !strings.Contains(err.Error(), "HTTP/2") {
			t.Errorf("a gRPC device took %v: %v", extra, err)
		}
	}
}

// TestProbes_RefuseMutualTLSWithNoCertificate: a device whose record asks
// for mutual TLS and has no stored certificate is refused by both probes
// before either dials, rather than probed without one.
func TestProbes_RefuseMutualTLSWithNoCertificate(t *testing.T) {
	var hits int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	api := httpDevice(t, srv, map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true})
	if _, err := (httpProber{}).Probe(context.Background(), api, nil); err == nil || !strings.Contains(err.Error(), "no client certificate") {
		t.Errorf("HTTP probe: %v", err)
	}
	if hits != 0 {
		t.Errorf("the HTTP probe reached the server %d times", hits)
	}

	g := build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "127.0.0.1:1", devicetls.ClientCertificateProperty: true})
	if _, err := (grpcProber{}).Probe(context.Background(), g, nil); err == nil || !strings.Contains(err.Error(), "no client certificate") {
		t.Errorf("gRPC probe: %v", err)
	}
}
