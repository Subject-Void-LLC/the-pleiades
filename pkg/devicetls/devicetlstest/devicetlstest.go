// Package devicetlstest is a TLS server for tests that asks for a client
// certificate and refuses the connection over it, in the shapes a client
// meets: an alert after the client's side of a TLS 1.3 handshake, the same
// after a delay, a reset that loses the alert (as a host that discards
// queued data on a reset loses it), and a server that accepts the
// certificate and sends nothing first, as an HTTP/1.1 server waits for its
// request. It serves no protocol past the handshake, so a gNMI, RESTCONF or
// HTTP client is tested against it alike.
package devicetlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Mode is how a Server answers a client's handshake.
type Mode int

// The ways a Server answers.
const (
	// Refuse refuses every connection, certificate or none, with a TLS
	// alert (bad_certificate), then closes it.
	Refuse Mode = iota
	// LoseFirst resets the first connection after the client's side of its
	// handshake, with no alert, and refuses every later one as Refuse does.
	LoseFirst
	// Silent accepts any certificate, or none, and sends nothing.
	Silent
)

// Options sets how a Server answers.
type Options struct {
	// Mode is how the server answers.
	Mode Mode
	// TLS12 serves TLS 1.2 at most, where a refusal ends the handshake.
	TLS12 bool
	// Delay is how long the server waits, once the client's certificate (or
	// its absence) has arrived, before it refuses.
	Delay time.Duration
}

// Server is a TLS server on a loopback port.
type Server struct {
	lis        net.Listener
	pem        string
	opts       Options
	handshakes atomic.Int32
	wg         sync.WaitGroup
	mu         sync.Mutex
	conns      []net.Conn
}

// New starts a Server answering as opts says, stopped when t ends.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	cert, certPEM := selfSigned(t)
	s := &Server{pem: certPEM, opts: opts}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		// The request names no authority, so a client presents whatever it
		// holds, and every refusal is the server's own.
		ClientAuth: tls.RequestClientCert,
		NextProtos: []string{"h2", "http/1.1"},
	}
	if opts.TLS12 {
		cfg.MaxVersion = tls.VersionTLS12
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.lis = lis
	s.wg.Add(1)
	go s.serve(cfg)
	t.Cleanup(s.stop)
	return s
}

// Addr is the server's host:port.
func (s *Server) Addr() string { return s.lis.Addr().String() }

// URL is the server's https origin.
func (s *Server) URL() string { return "https://" + s.Addr() }

// CAPEM is the server's certificate, which signs itself, for a device's
// tls_ca_pem.
func (s *Server) CAPEM() string { return s.pem }

// Handshakes returns how many handshakes clients began.
func (s *Server) Handshakes() int { return int(s.handshakes.Load()) }

// serve accepts until the listener closes.
func (s *Server) serve(cfg *tls.Config) {
	defer s.wg.Done()
	for {
		raw, err := s.lis.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, raw)
		s.mu.Unlock()
		n := s.handshakes.Add(1)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.answer(raw, cfg, n)
		}()
	}
}

// answer runs one connection's handshake, the nth, as the server's mode says.
func (s *Server) answer(raw net.Conn, base *tls.Config, n int32) {
	defer func() { _ = raw.Close() }()
	cfg := base.Clone()
	cfg.VerifyConnection = func(tls.ConnectionState) error {
		switch {
		case s.opts.Mode == Silent:
			return nil
		case s.opts.Mode == LoseFirst && n == 1:
			// A reset, and no alert, after the client's side of the
			// handshake: what a client meets when the alert is discarded.
			if tcp, ok := raw.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = raw.Close()
			return errors.New("reset")
		}
		time.Sleep(s.opts.Delay)
		return errors.New("this certificate is refused")
	}
	conn := tls.Server(raw, cfg)
	if err := conn.Handshake(); err != nil {
		return
	}
	// Silent: nothing is sent, and the connection lasts until the client
	// closes it or the server stops.
	_, _ = io.Copy(io.Discard, conn)
}

// stop closes the listener and every connection, and waits for them.
func (s *Server) stop() {
	_ = s.lis.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// selfSigned makes a certificate for 127.0.0.1 and localhost that signs
// itself, so it can be pinned as an authority.
func selfSigned(t testing.TB) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "devicetlstest"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
