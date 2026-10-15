// Tests for TLS to a database through the device, in-process and through
// pg_dump, and for what the methods declare.
package pg_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestTLS_VerifiedFromTheDeviceInProcessAndByPgDump: a connection with a
// pinned CA reaches a TLS server through the device, encrypted and
// verified, in-process and through the device's pg_dump (the CA reaching
// it as a private file), by its address and by a server_name; a CA that
// did not sign the server's certificate is refused by both.
func TestTLS_VerifiedFromTheDeviceInProcessAndByPgDump(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	ca := selfSigned(t, dir)
	h := startHarness(t, dir)
	other := selfSigned(t, t.TempDir())
	address := "127.0.0.1"
	add := func(name, caPEM, serverName string) {
		h.rc.connections = append(h.rc.connections, wire.DatabaseConnection{Name: name, Host: address, Port: h.port, User: adminUser, Password: adminPassword, CAPEM: caPEM, ServerName: serverName})
	}
	add("pinned", string(ca), "")
	add("named", string(ca), "localhost")
	add("stranger", string(other), "")

	for _, conn := range []string{"pinned", "named"} {
		stats, err := query(h, map[string]any{"connection": conn, "database": "app", "sql": "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()", "allow_privileged_role": true})
		if err != nil || stats["csv"] != "ssl\r\ntrue\r\n" {
			t.Fatalf("%s in-process: %v, %v", conn, stats["csv"], err)
		}
		dest := filepath.Join(t.TempDir(), conn+".dump")
		if _, err := pg.Dump(context.Background(), h.fresh(), h.device, params(map[string]any{"connection": conn, "database": "app", "dest": dest})); err != nil {
			t.Fatalf("%s through pg_dump: %v", conn, err)
		}
	}
	if _, err := query(h, map[string]any{"connection": "stranger", "database": "app", "sql": "SELECT 1", "allow_privileged_role": true}); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("another authority in-process = %v", err)
	}
	h.rc.connections = append(h.rc.connections, wire.DatabaseConnection{Name: "named-host", Host: "localhost", Port: h.port, User: adminUser, Password: adminPassword, CAPEM: string(ca), ServerName: "localhost"})
	if _, err := pg.Dump(context.Background(), h.fresh(), h.device, params(map[string]any{"connection": "named-host", "database": "app", "dest": filepath.Join(t.TempDir(), "x.dump")})); err == nil || !strings.Contains(err.Error(), "the host must be an address") {
		t.Errorf("server_name with a host name through pg_dump = %v", err)
	}
}

// TestRegistered declares what the design note promises.
func TestRegistered(t *testing.T) {
	for name, want := range map[string]struct{ readOnly, reversible, checkCall bool }{
		"db.pg.query": {false, false, true}, "db.pg.dump": {false, true, false}, "db.pg.restore": {false, false, false},
		"db.pg.migrate": {false, false, false}, "db.pg.grants": {true, false, false}, "db.pg.rowcounts": {true, false, false},
	} {
		d, ok := collection.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		r := d.Manifest.Reversibility
		if r.ReadOnly != want.readOnly || r.Reversible != want.reversible || (d.CheckCall != nil) != want.checkCall || !d.Manifest.SupportsCheck || d.Check == nil {
			t.Errorf("%s declares %+v, check call %v", name, r, d.CheckCall != nil)
		}
	}
}

// selfSigned writes a self-signed certificate for localhost and
// 127.0.0.1, and its key, to dir as cert.pem and key.pem, returning the
// certificate's PEM.
func selfSigned(t *testing.T, dir string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPEM
}
