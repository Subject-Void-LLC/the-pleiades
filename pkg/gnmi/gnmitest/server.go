// Package gnmitest is a gNMI server for tests: a real gRPC server on a
// loopback port, over TLS with a certificate made for it (or plaintext when
// asked), checking the gNMI specification's username and password metadata
// or a bearer token, so pkg/gnmi, onboarding and the net.gnmi methods are
// tested through their real gRPC, TLS and credential path rather than a
// stand-in for it.
//
// It answers Capabilities, Get, Set (deletes, replaces and updates applied as
// one transaction), and Subscribe in ONCE, POLL and STREAM modes, beside gNOI
// System.Time, File.Stat and CertificateManagement.GetCertificates. Its
// Stratum mode answers as Stratum's stratum_bmv2 answered on 2026-11-15: any
// credential accepted, and a Get refused unless asked for in PROTO.
package gnmitest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"sync"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnoi/cert"
	"github.com/openconfig/gnoi/file"
	"github.com/openconfig/gnoi/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// StratumGetRefusal is what Stratum answers a Get in any encoding but PROTO
// with (InvalidArgument), as measured on 2026-11-15.
const StratumGetRefusal = "Get response can only be encoded as PROTO."

// Options sets what a Server answers.
type Options struct {
	// Username and Password are the login the username and password
	// metadata must carry; both empty asks for none.
	Username, Password string
	// Token, when set, is the bearer token an authorization metadata entry
	// must carry instead.
	Token string
	// Plaintext serves without TLS.
	Plaintext bool
	// ClientCertificate requires the client to present a certificate the
	// server's own authority signed (mutual TLS).
	ClientCertificate bool
	// TLS12 serves TLS 1.2 at most. A server refuses a client certificate
	// inside a TLS 1.2 handshake, and in TLS 1.3 (the default) only after the
	// client's side of the handshake has finished, so a test can hold the
	// client to both.
	TLS12 bool
	// Version is the gNMI version Capabilities names; "0.7.0" when empty.
	Version string
	// Models are the models Capabilities lists; Models below when nil.
	Models []*gpb.ModelData
	// Encodings are the encodings Capabilities lists; JSON, JSON_IETF and
	// PROTO when nil.
	Encodings []gpb.Encoding
	// Stratum accepts any credential, or none, and refuses a Get in any
	// encoding but PROTO with StratumGetRefusal.
	Stratum bool
	// Hook, when set, runs at the start of every call after the credential
	// is checked; an error it returns is the call's answer.
	Hook func(ctx context.Context, rpc string) error
	// AfterSet, when set, runs after a Set is applied and before it is
	// answered; an error it returns is the answer, the change kept.
	AfterSet func(ctx context.Context) error
	// SetAnswer, when set, may change a Set's answer after the Set is
	// applied and before the OK is sent, so a test can send an answer a
	// client refuses to read for a change the server made.
	SetAnswer func(resp *gpb.SetResponse)
	// Files are the files File.Stat reports, by path.
	Files map[string]uint64
	// RequireTarget, when set, is the prefix target every Get, Set and
	// Subscribe must name, as SONiC's server requires one: a request naming
	// none is refused Unimplemented with EmptyTargetRefusal, and one naming
	// another InvalidArgument. Get and Subscribe answers echo the target in
	// their prefix, as SONiC's do.
	RequireTarget string
}

// Models is the model list a Server's Capabilities names by default.
var Models = []*gpb.ModelData{
	{Name: "openconfig-interfaces", Organization: "OpenConfig working group", Version: "2.4.1"},
	{Name: "openconfig-system", Organization: "OpenConfig working group", Version: "0.7.0"},
	{Name: "srl_nokia-interfaces", Organization: "Nokia", Version: "2024-10-31"},
}

// Server is a gNMI server on a loopback port.
type Server struct {
	gpb.UnimplementedGNMIServer
	opts   Options
	lis    net.Listener
	srv    *grpc.Server
	ca     authority
	leaf   *x509.Certificate
	client map[string]string
	// stranger is a client certificate another authority signed.
	stranger map[string]string

	mu sync.Mutex
	// data is what the server holds.
	data tree
	// rpcs are the calls answered, in order.
	rpcs []string
	// md is each call's metadata, beside rpcs.
	md []metadata.MD
	// refusals counts the calls refused for their credential.
	refusals int
	// sets counts the Set calls that reached the data.
	sets int
	// targets are the prefix targets of each Get, Set and Subscribe, in
	// order, empty for a request naming none.
	targets []string
	// changed is closed and replaced on every change, for ON_CHANGE.
	changed chan struct{}
}

// New starts a Server answering as opts says, stopped when t ends.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.Version == "" {
		opts.Version = "0.7.0"
	}
	if opts.Models == nil {
		opts.Models = Models
	}
	if opts.Encodings == nil {
		opts.Encodings = []gpb.Encoding{gpb.Encoding_JSON, gpb.Encoding_JSON_IETF, gpb.Encoding_PROTO}
	}
	s := &Server{opts: opts, data: tree{}, changed: make(chan struct{}), ca: newAuthority(t, "gnmitest authority")}
	var serverOpts []grpc.ServerOption
	if !opts.Plaintext {
		pair, _, _, leaf := s.ca.issue(t, "gnmitest", false)
		s.leaf = leaf
		cfg := &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		if opts.TLS12 {
			cfg.MaxVersion = tls.VersionTLS12
		}
		if opts.ClientCertificate {
			pool := x509.NewCertPool()
			pool.AddCert(s.ca.cert)
			cfg.ClientCAs, cfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
			_, certPEM, keyPEM, _ := s.ca.issue(t, "gnmitest client", true)
			s.client = map[string]string{wire.SecretCertificatePEM: certPEM, wire.SecretPrivateKeyPEM: keyPEM}
			// A client certificate from an authority of its own, which this
			// server does not trust.
			_, otherPEM, otherKeyPEM, _ := newAuthority(t, "another authority").issue(t, "gnmitest stranger", true)
			s.stranger = map[string]string{wire.SecretCertificatePEM: otherPEM, wire.SecretPrivateKeyPEM: otherKeyPEM}
		}
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(cfg)))
	}
	serverOpts = append(serverOpts, grpc.UnaryInterceptor(s.unary), grpc.StreamInterceptor(s.stream))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.lis, s.srv = lis, grpc.NewServer(serverOpts...)
	gpb.RegisterGNMIServer(s.srv, s)
	system.RegisterSystemServer(s.srv, gnoiSystem{})
	file.RegisterFileServer(s.srv, gnoiFile{s: s})
	cert.RegisterCertificateManagementServer(s.srv, gnoiCert{s: s})
	go func() { _ = s.srv.Serve(lis) }()
	t.Cleanup(s.Stop)
	return s
}

// Stop stops the server, ending every call it holds.
func (s *Server) Stop() {
	s.srv.Stop()
}

// Addr is the server's host:port.
func (s *Server) Addr() string {
	return s.lis.Addr().String()
}

// AuthorityPEM is the authority that signed the server's certificate, for a
// device's tls_ca_pem.
func (s *Server) AuthorityPEM() string {
	return s.ca.pem
}

// Certificate is the server's own certificate; nil for a plaintext server.
func (s *Server) Certificate() *x509.Certificate {
	return s.leaf
}

// ClientSecrets are a client certificate and key the server trusts, as a
// credential's flattened secrets; nil unless ClientCertificate is set.
func (s *Server) ClientSecrets() map[string]string {
	return s.client
}

// UntrustedClientSecrets are a client certificate and key another authority
// signed, which the server refuses; nil unless ClientCertificate is set.
func (s *Server) UntrustedClientSecrets() map[string]string {
	return s.stranger
}

// RPCs returns the calls the server answered (or refused), in order.
func (s *Server) RPCs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rpcs...)
}

// Metadata returns each call's metadata, beside RPCs.
func (s *Server) Metadata() []metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]metadata.MD, len(s.md))
	for i, md := range s.md {
		out[i] = md.Copy()
	}
	return out
}

// Refusals returns how many calls were refused for their credential.
func (s *Server) Refusals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refusals
}

// Sets returns how many Set calls reached the data.
func (s *Server) Sets() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sets
}

// Put stores the JSON document doc at path, written as gNMI writes a path,
// as a JSON_IETF value.
func (s *Server) Put(t testing.TB, path, doc string) {
	t.Helper()
	p, err := gnmi.ParsePath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.PutValue(p, &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(doc)}})
}

// PutValue stores val at p.
func (s *Server) PutValue(p gnmi.Path, val *gpb.TypedValue) {
	s.mu.Lock()
	s.data.put(p, val)
	s.mu.Unlock()
	s.notify()
}

// Value returns the value stored at path, and whether one is there.
func (s *Server) Value(t testing.TB, path string) (*gpb.TypedValue, bool) {
	t.Helper()
	p, err := gnmi.ParsePath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[p.String()]
	return e.val, ok
}

// notify wakes every ON_CHANGE subscription.
func (s *Server) notify() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// unary checks every unary call's credential.
func (s *Server) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := s.admit(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// stream checks every streaming call's credential.
func (s *Server) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := s.admit(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}

// admit records a call and checks its credential, then runs the hook.
func (s *Server) admit(ctx context.Context, method string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.rpcs = append(s.rpcs, method)
	s.md = append(s.md, md.Copy())
	s.mu.Unlock()
	if !s.credentialOK(md) {
		s.mu.Lock()
		s.refusals++
		s.mu.Unlock()
		return status.Error(codes.Unauthenticated, "authentication failed")
	}
	if s.opts.Hook != nil {
		return s.opts.Hook(ctx, method)
	}
	return nil
}

// credentialOK reports whether md carries the credential the server asks.
func (s *Server) credentialOK(md metadata.MD) bool {
	one := func(key string) string {
		if v := md.Get(key); len(v) == 1 {
			return v[0]
		}
		return ""
	}
	switch {
	case s.opts.Stratum:
		return true
	case s.opts.Token != "":
		return one("authorization") == "Bearer "+s.opts.Token
	case s.opts.Username != "" || s.opts.Password != "":
		return one("username") == s.opts.Username && one("password") == s.opts.Password
	}
	return true
}
