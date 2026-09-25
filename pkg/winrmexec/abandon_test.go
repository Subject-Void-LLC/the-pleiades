// Tests for what Execute does with a command it gave up on: stop it and
// close its shell, from a fresh connection, unless the caller asked for it
// to be left running.
package winrmexec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mutualTLS is an authority, a server certificate it issued for
// 127.0.0.1, and a client certificate it issued, all as a WinRM
// certificate listener and its client need them.
type mutualTLS struct {
	caPEM         []byte
	serverCert    tls.Certificate
	clientCertPEM []byte
	clientKeyPEM  []byte
}

// newMutualTLS issues the three.
func newMutualTLS(t *testing.T) mutualTLS {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "abandon test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(serial int64, usage x509.ExtKeyUsage, ip net.IP) (certPEM, keyPEM []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "leaf"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if ip != nil {
			template.IPAddresses = []net.IP{ip}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	serverCertPEM, serverKeyPEM := leaf(2, x509.ExtKeyUsageServerAuth, net.ParseIP("127.0.0.1"))
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPEM, clientKeyPEM := leaf(3, x509.ExtKeyUsageClientAuth, nil)
	return mutualTLS{
		caPEM:      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		serverCert: serverCert, clientCertPEM: clientCertPEM, clientKeyPEM: clientKeyPEM,
	}
}

// stuckService is a WinRM service over mutual TLS whose Receive never
// answers, as a host's does while a command runs past the caller's
// timeout, and which records every other action it is sent.
type stuckService struct {
	server  *httptest.Server
	release chan struct{}

	mu      sync.Mutex
	actions []string
}

// startStuckService starts the service, trusting ca's client certificates.
func startStuckService(t *testing.T, ca mutualTLS) *stuckService {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.caPEM) {
		t.Fatal("the authority did not parse")
	}
	s := &stuckService{release: make(chan struct{})}
	s.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		action := actionOf(string(body))
		s.mu.Lock()
		s.actions = append(s.actions, action)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/soap+xml;charset=UTF-8")
		switch action {
		case "Create":
			_, _ = w.Write([]byte(shellReply))
		case "Command":
			_, _ = w.Write([]byte(cmdReply))
		case "Receive":
			select {
			case <-s.release:
			case <-r.Context().Done():
			}
			_, _ = w.Write([]byte(output("", "", 0)))
		default:
			_, _ = w.Write([]byte(okReply))
		}
	}))
	s.server.TLS = &tls.Config{
		Certificates: []tls.Certificate{ca.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}
	s.server.StartTLS()
	t.Cleanup(func() {
		close(s.release)
		s.server.Close()
	})
	return s
}

// target returns where the service listens.
func (s *stuckService) target(t *testing.T) Target {
	t.Helper()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(s.server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	return Target{Host: host, Port: port}
}

// seen returns the actions recorded so far.
func (s *stuckService) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actions...)
}

func TestExecute_StopsACommandItGaveUpOn(t *testing.T) {
	ca := newMutualTLS(t)
	auth := Auth{CertificatePEM: ca.clientCertPEM, PrivateKeyPEM: ca.clientKeyPEM}

	t.Run("stopped and its shell closed", func(t *testing.T) {
		s := startStuckService(t, ca)
		start := time.Now()
		_, err := Execute(context.Background(), s.target(t), auth, Command{Shell: ShellNone, Script: "prog"},
			Options{CACert: ca.caPEM, Timeout: time.Second})
		if err == nil || !strings.Contains(err.Error(), "stopped the command and closed its shell") {
			t.Fatalf("err = %v, want the command reported stopped", err)
		}
		if elapsed := time.Since(start); elapsed > abandonedCleanupBound {
			t.Errorf("returned after %s, beyond the cleanup bound", elapsed)
		}
		got := strings.Join(s.seen(), " ")
		if !strings.Contains(got, "Signal") || !strings.HasSuffix(got, "Delete") {
			t.Errorf("actions = %s, want a Signal and then a Delete after the stuck Receive", got)
		}
	})

	t.Run("left running when asked", func(t *testing.T) {
		s := startStuckService(t, ca)
		_, err := Execute(context.Background(), s.target(t), auth, Command{Shell: ShellNone, Script: "prog"},
			Options{CACert: ca.caPEM, Timeout: time.Second, LeaveRunningOnTimeout: true})
		if err == nil || !strings.Contains(err.Error(), "may still be running") {
			t.Fatalf("err = %v, want the command reported possibly still running", err)
		}
		for _, action := range s.seen() {
			if action == "Signal" || action == "Delete" {
				t.Errorf("actions = %v: a command asked to be left running was stopped", s.seen())
			}
		}
	})
}
