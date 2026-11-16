// Real sessions for the flow tests, each over a loopback TCP connection and
// recorded byte for byte at the client: SSH through golang.org/x/crypto/ssh
// (an exec, under the cipher asked for), TLS through crypto/tls (a request
// and its answer, at the version asked for), and plaintext gRPC through
// google.golang.org/grpc with a username and password in its metadata, as
// gNMI sends them. The bytes in each capture are the real protocol's.
package pcapsum_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// throwaway is a random value no test writes down: a password made for one
// test, so no credential-shaped string is in the source.
func throwaway(t *testing.T) string {
	t.Helper()
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// listen is a loopback listener closed when t ends.
func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// sshSession is a real SSH session under cipher: a password login, then an
// exec whose output and exit status come back.
func sshSession(t *testing.T, cipher string) []chunk {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pass := throwaway(t)
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if string(p) != pass {
			return nil, io.EOF
		}
		return &ssh.Permissions{}, nil
	}}
	cfg.Ciphers = []string{cipher}
	cfg.AddHostKey(signer)
	l := listen(t)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, chans, reqs, err := ssh.NewServerConn(c, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			ch, chReqs, err := nc.Accept()
			if err != nil {
				return
			}
			for r := range chReqs {
				_ = r.Reply(r.Type == "exec", nil)
				if r.Type == "exec" {
					_, _ = ch.Write([]byte("<rpc-reply><data>hostname core1</data></rpc-reply>\n"))
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					_ = ch.Close()
				}
			}
		}
	}()
	raw, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{Conn: raw}
	ccfg := &ssh.ClientConfig{
		User:            "netops",
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		Config:          ssh.Config{Ciphers: []string{cipher}},
		Timeout:         10 * time.Second,
	}
	conn, chans, reqs, err := ssh.NewClientConn(rec, l.Addr().String(), ccfg)
	if err != nil {
		t.Fatal(err)
	}
	cl := ssh.NewClient(conn, chans, reqs)
	sess, err := cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := sess.Output("show running-config"); err != nil || len(out) == 0 {
		t.Fatalf("the exec: %q, %v", out, err)
	}
	_ = cl.Close()
	return rec.recorded()
}

// selfSigned is a certificate for 127.0.0.1 and a pool trusting it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "core1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"core1"}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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

// tlsSession is a real TLS session at version: a request of request bytes
// and an answer as long, then a close.
func tlsSession(t *testing.T, version uint16, request []byte) []chunk {
	t.Helper()
	cert, pool := selfSigned(t)
	l := listen(t)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		s := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: version, MaxVersion: version})
		defer func() { _ = s.Close() }()
		buf := make([]byte, len(request))
		if _, err := io.ReadFull(s, buf); err != nil {
			return
		}
		_, _ = s.Write(buf)
	}()
	raw, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{Conn: raw}
	c := tls.Client(rec, &tls.Config{RootCAs: pool, ServerName: "core1", MinVersion: version, MaxVersion: version})
	if _, err := c.Write(request); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(request))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	return rec.recorded()
}

// grpcSession is a real plaintext gRPC connection making two calls, each
// with username and password metadata as gNMI sends them: the first puts
// them in the HPACK dynamic table, the second names them by index.
func grpcSession(t *testing.T, user, pass string) []chunk {
	t.Helper()
	l := listen(t)
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)
	dialed := make(chan *recorder, 1)
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		rec := &recorder{Conn: raw}
		select {
		case dialed <- rec:
		default:
			// A second connection is not the one the test reads.
		}
		return rec, nil
	}
	cc, err := grpc.NewClient("passthrough:///"+l.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(dial))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "username", user, "password", pass)
	for range 2 {
		// No service is registered: each call is answered Unimplemented,
		// after its request headers crossed.
		if err := cc.Invoke(ctx, "/gnmi.gNMI/Capabilities", &emptypb.Empty{}, &emptypb.Empty{}); err == nil {
			t.Fatal("a call to no service succeeded")
		}
	}
	_ = cc.Close()
	srv.Stop()
	select {
	case rec := <-dialed:
		return rec.recorded()
	default:
		t.Fatal("the client never dialed")
	}
	return nil
}
