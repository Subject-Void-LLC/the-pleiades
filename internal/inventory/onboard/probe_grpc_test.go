// Tests for the gRPC probe against real in-process gRPC servers: with and
// without health and reflection, over TLS with a credential, and the
// refusals.
package onboard

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grpcServer serves srv on loopback until the test ends and returns its
// address.
func grpcServer(t *testing.T, srv *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// grpcDevice builds a generic_grpc device for target.
func grpcDevice(t *testing.T, target string, plaintext bool) inventory.InventoryItem {
	t.Helper()
	return build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: plaintext})
}

// TestGRPCProbe_HealthAndReflection records the services a server with
// both standard services reports.
func TestGRPCProbe_HealthAndReflection(t *testing.T) {
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, health.NewServer())
	reflection.Register(srv)
	got, err := grpcProber{}.Probe(context.Background(), grpcDevice(t, grpcServer(t, srv), true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Capabilities, []capability.Name{capability.NameGRPC}) || got.Facts["health"] != "SERVING" || got.Facts["reflection"] != "v1" {
		t.Fatalf("probed %+v", got)
	}
	services, _ := got.Facts["services"].([]any)
	if !slices.Contains(services, any("grpc.health.v1.Health")) {
		t.Errorf("services %v", services)
	}
}

// TestGRPCProbe_BareServer: a server with neither standard service still
// answers Unimplemented, which only a gRPC server can, so the probe
// succeeds and records that neither is served.
func TestGRPCProbe_BareServer(t *testing.T) {
	got, err := grpcProber{}.Probe(context.Background(), grpcDevice(t, grpcServer(t, grpc.NewServer()), true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Facts["health"] != "not served" || got.Facts["reflection"] != "not served" {
		t.Errorf("facts %v", got.Facts)
	}
}

// TestGRPCProbe_NotServingIsRefused: a server reporting NOT_SERVING is not
// onboarded.
func TestGRPCProbe_NotServingIsRefused(t *testing.T) {
	srv := grpc.NewServer()
	h := health.NewServer()
	h.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(srv, h)
	if _, err := (grpcProber{}).Probe(context.Background(), grpcDevice(t, grpcServer(t, srv), true), nil); err == nil || !strings.Contains(err.Error(), "NOT_SERVING") {
		t.Fatalf("err %v", err)
	}
}

// TestGRPCProbe_NothingListeningProvesNothing: a closed port is an error.
func TestGRPCProbe_NothingListeningProvesNothing(t *testing.T) {
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := lis.Addr().String()
	_ = lis.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := (grpcProber{}).Probe(ctx, grpcDevice(t, addr, true), nil); err == nil {
		t.Fatal("a closed port proved a gRPC server")
	}
}

// TestGRPCProbe_PlaintextNeverCarriesACredential: with a credential stored
// and grpc_plaintext set, the probe refuses before connecting.
func TestGRPCProbe_PlaintextNeverCarriesACredential(t *testing.T) {
	var saw bool
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		saw = true
		return h(ctx, req)
	}))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	_, err := grpcProber{}.Probe(context.Background(), grpcDevice(t, grpcServer(t, srv), true), map[string]string{"password": "tok"})
	if err == nil || saw {
		t.Fatalf("err %v, server saw a call: %v", err, saw)
	}
}

// TestGRPCProbe_TLSWithCredential sends the stored credential as a bearer
// token over a verified TLS connection.
func TestGRPCProbe_TLSWithCredential(t *testing.T) {
	cert, _ := selfSigned(t)
	var got string
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			got = strings.Join(md.Get("authorization"), ",")
			return h(ctx, req)
		}))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	addr := grpcServer(t, srv)
	pinnedDev := build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: addr, devicetls.CAPEMProperty: certPEM(cert)})
	if _, err := (grpcProber{}).Probe(context.Background(), pinnedDev, map[string]string{"password": "tok"}); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer tok" {
		t.Errorf("the server saw authorization %q", got)
	}
	if _, err := (grpcProber{}).Probe(context.Background(), grpcDevice(t, addr, false), nil); err == nil {
		t.Error("an untrusted certificate was accepted")
	}
}

// selfSigned returns a certificate for 127.0.0.1 and a pool trusting it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "grpc stand-in"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// certPEM is cert's leaf as PEM, for a device's tls_ca_pem.
func certPEM(cert tls.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
}
