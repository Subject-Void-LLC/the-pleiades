// A Docker engine fixture behind mutual TLS, and the throwaway authority
// that issues its certificates.
package testsupport

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// EnginePKI is a throwaway authority with a server certificate for
// localhost and 127.0.0.1 and a client certificate, all PEM, valid for an
// hour: enough to stand an engine up behind mutual TLS for one test.
type EnginePKI struct {
	CAPEM, ServerCert, ServerKey, ClientCert, ClientKey string
}

// NewEnginePKI issues a fresh EnginePKI.
func NewEnginePKI(tb testing.TB) EnginePKI {
	tb.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pleiades test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		tb.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		tb.Fatal(err)
	}
	issue := func(serial int64, usage x509.ExtKeyUsage, dns []string, ips []net.IP) (string, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			tb.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "pleiades test"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			DNSNames: dns, IPAddresses: ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			tb.Fatal(err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			tb.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	}
	serverCert, serverKey := issue(2, x509.ExtKeyUsageServerAuth, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	clientCert, clientKey := issue(3, x509.ExtKeyUsageClientAuth, nil, nil)
	return EnginePKI{
		CAPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		ServerCert: serverCert, ServerKey: serverKey, ClientCert: clientCert, ClientKey: clientKey,
	}
}

// engineTLSPort is where the TLS fixture's engine listens inside its
// container.
const engineTLSPort = "2376/tcp"

// StartTLSDockerHost starts the Docker fixture with its engine also
// listening on TCP behind mutual TLS, from a fresh EnginePKI, and returns
// the fixture, the TLS listener's address as this machine reaches it, and
// the PKI (the server's name is "localhost").
func StartTLSDockerHost(tb testing.TB) (*EngineHost, string, EnginePKI) {
	tb.Helper()
	pki := NewEnginePKI(tb)
	d := StartDockerHost(tb, EngineHostOptions{
		Env: map[string]string{
			"DOCKER_TLS_CERTDIR": "",
			"DOCKERD_EXTRA_ARGS": "--host=tcp://0.0.0.0:2376 --tlsverify --tlscacert=/certs/ca.pem --tlscert=/certs/server.pem --tlskey=/certs/server-key.pem",
		},
		Files: []testcontainers.ContainerFile{
			{Reader: strings.NewReader(pki.CAPEM), ContainerFilePath: "/certs/ca.pem", FileMode: 0o644},
			{Reader: strings.NewReader(pki.ServerCert), ContainerFilePath: "/certs/server.pem", FileMode: 0o644},
			{Reader: strings.NewReader(pki.ServerKey), ContainerFilePath: "/certs/server-key.pem", FileMode: 0o600},
		},
		ExposedPorts: []string{engineTLSPort},
	})
	mapped, err := d.Container.MappedPort(context.Background(), engineTLSPort)
	if err != nil {
		tb.Fatalf("the TLS fixture's mapped port: %v", err)
	}
	return d, net.JoinHostPort(d.Host, strconv.Itoa(int(mapped.Num()))), pki
}
