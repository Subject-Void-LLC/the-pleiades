// Package gnmi_test: the client against gnmitest's server over a real
// loopback gRPC connection, through the same TLS, credential and decoding
// path a device's client takes.
package gnmi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"go.uber.org/goleak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestMain fails the package when a test leaves a goroutine running: a
// subscription or a channel a client forgot to close would accumulate per
// task.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// login is the throwaway credential the test servers ask for.
const (
	testUser     = "admin"
	testPassword = "Gnmi-test-pass-7f3a"
)

// clientOf returns a client of s through its device, closed when t ends.
func clientOf(t *testing.T, s *gnmitest.Server) (*gnmi.Client, []string) {
	t.Helper()
	c, warnings, err := gnmi.ClientFor(gnmitest.NewDevice(t, s), "router1", gnmitest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, warnings
}

// ctxOf is a context bounded for one test.
func ctxOf(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// mustPath parses s.
func mustPath(t *testing.T, s string) gnmi.Path {
	t.Helper()
	p, err := gnmi.ParsePath(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCapabilities_OverTLSWithTheSpecificationsMetadata reads Capabilities
// over TLS with the server's authority pinned, and holds the credential to
// the username and password metadata the gNMI specification names.
func TestCapabilities_OverTLSWithTheSpecificationsMetadata(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	c, warnings := clientOf(t, s)
	caps, err := c.Capabilities(ctxOf(t))
	if err != nil {
		t.Fatal(err)
	}
	if caps.Version != "0.7.0" || len(caps.Models) != 3 || !caps.Supports(gnmi.EncodingJSONIETF) || !caps.Supports(gnmi.EncodingProto) {
		t.Fatalf("Capabilities = %+v", caps)
	}
	if caps.Models[0].Name != "openconfig-interfaces" || caps.Models[0].Version != "2.4.1" {
		t.Errorf("first model = %+v, want openconfig-interfaces 2.4.1 (sorted by name)", caps.Models[0])
	}
	if !c.Verified() {
		t.Error("Verified = false over TLS")
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v for a device with a pinned authority and nothing weakened", warnings)
	}
	md := s.Metadata()
	if len(md) != 1 || strings.Join(md[0].Get("username"), "") != testUser || strings.Join(md[0].Get("password"), "") != testPassword {
		t.Fatalf("metadata = %v, want one call with the username and password", md)
	}
	if got := md[0].Get("authorization"); len(got) != 0 {
		t.Errorf("authorization = %v sent beside a password login", got)
	}
}

// TestGetSetGet_RoundTripsAValueAndItsChange writes with each Set operation
// and reads every change back with Get, in JSON_IETF.
func TestGetSetGet_RoundTripsAValueAndItsChange(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	path := mustPath(t, "/interfaces/interface[name=ethernet-1/1]/config")

	_, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{path}, Encoding: gnmi.EncodingJSONIETF, Type: gnmi.DataConfig})
	if gnmi.Code(err) != codes.NotFound {
		t.Fatalf("Get of an absent path = %v, want the server's NotFound", err)
	}

	res, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: path, JSON: []byte(`{"description":"uplink","mtu":9000}`), Encoding: gnmi.EncodingJSONIETF}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Operations) != 1 || res.Operations[0].Op != "update" || !res.Operations[0].Path.Equal(path) {
		t.Errorf("Set result = %+v", res)
	}
	if got := readOne(t, c, path); got != `{"description":"uplink","mtu":9000}` {
		t.Errorf("after update = %s", got)
	}
	// An update merges; a replace takes the value whole.
	if _, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: path, JSON: []byte(`{"mtu":1500}`), Encoding: gnmi.EncodingJSONIETF}}}); err != nil {
		t.Fatal(err)
	}
	if got := readOne(t, c, path); got != `{"description":"uplink","mtu":1500}` {
		t.Errorf("after a merging update = %s", got)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Replace: []gnmi.SetValue{{Path: path, JSON: []byte(`{"mtu":1400}`), Encoding: gnmi.EncodingJSONIETF}}}); err != nil {
		t.Fatal(err)
	}
	if got := readOne(t, c, path); got != `{"mtu":1400}` {
		t.Errorf("after a replace = %s", got)
	}
	// A leaf inside the stored value is read as its own node.
	if got := readOne(t, c, mustPath(t, "/interfaces/interface[name=ethernet-1/1]/config/mtu")); got != "1400" {
		t.Errorf("leaf = %s, want 1400", got)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Delete: []gnmi.Path{path}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{path}, Encoding: gnmi.EncodingJSONIETF}); gnmi.Code(err) != codes.NotFound {
		t.Errorf("Get after delete = %v, want NotFound", err)
	}
	if s.Sets() != 4 {
		t.Errorf("server applied %d Sets, want 4", s.Sets())
	}
}

// readOne reads p in JSON_IETF and returns its one value's JSON.
func readOne(t *testing.T, c *gnmi.Client, p gnmi.Path) string {
	t.Helper()
	ns, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{p}, Encoding: gnmi.EncodingJSONIETF})
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || len(ns[0].Updates) != 1 {
		t.Fatalf("Get %s = %+v, want one update", p, ns)
	}
	return string(ns[0].Updates[0].Value.JSON)
}

// TestSet_RefusesBeforeSending holds a Set to its rules before a byte is
// sent: nothing to do, a wildcard, and a value that is not one JSON document.
func TestSet_RefusesBeforeSending(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	for name, req := range map[string]gnmi.SetRequest{
		"empty":       {},
		"wildcard":    {Delete: []gnmi.Path{mustPath(t, "/interfaces/interface[name=*]")}},
		"not json":    {Update: []gnmi.SetValue{{Path: mustPath(t, "/a"), JSON: []byte(`{"a":`), Encoding: gnmi.EncodingJSONIETF}}},
		"two values":  {Update: []gnmi.SetValue{{Path: mustPath(t, "/a"), JSON: []byte(`1 2`), Encoding: gnmi.EncodingJSONIETF}}},
		"proto value": {Update: []gnmi.SetValue{{Path: mustPath(t, "/a"), JSON: []byte(`1`), Encoding: gnmi.EncodingProto}}},
		"too deep":    {Update: []gnmi.SetValue{{Path: mustPath(t, "/a"), JSON: []byte(strings.Repeat("[", gnmi.MaxValueDepth+1) + strings.Repeat("]", gnmi.MaxValueDepth+1)), Encoding: gnmi.EncodingJSON}}},
	} {
		if _, err := c.Set(ctx, req); err == nil {
			t.Errorf("%s: Set = nil, want a refusal", name)
		}
	}
	if rpcs := s.RPCs(); len(rpcs) != 0 {
		t.Errorf("server saw %v, want nothing sent", rpcs)
	}
}

// TestSet_AnAnswerLostAfterTheChangeIsSaidSo has the server apply a Set and
// then never answer it, and holds the client to saying the outcome is unknown
// rather than that the change failed.
func TestSet_AnAnswerLostAfterTheChangeIsSaidSo(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword, AfterSet: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	c, _ := clientOf(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: mustPath(t, "/system/config"), JSON: []byte(`{"hostname":"r1"}`), Encoding: gnmi.EncodingJSONIETF}}})
	var lost *gnmi.AnswerLostError
	if !errors.As(err, &lost) {
		t.Fatalf("Set = %v, want an AnswerLostError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Set = %v, want it to wrap the caller's deadline", err)
	}
	if s.Sets() != 1 {
		t.Errorf("server applied %d Sets, want 1: the change was made and only its answer lost", s.Sets())
	}
}

// TestSet_AnOKAnswerItCannotReadIsApplied has the server apply a Set and
// answer OK with a path in the field gNMI 0.4.0 deprecated, which the client
// refuses to read, and holds the client to saying the change was applied,
// never that it was refused.
func TestSet_AnOKAnswerItCannotReadIsApplied(t *testing.T) {
	for name, mangle := range map[string]func(*gpb.SetResponse){
		"prefix":    func(r *gpb.SetResponse) { r.Prefix = &gpb.Path{Element: []string{"system"}} },
		"operation": func(r *gpb.SetResponse) { r.Response[0].Path = &gpb.Path{Elem: []*gpb.PathElem{{Name: "bad\nname"}}} },
	} {
		t.Run(name, func(t *testing.T) {
			s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword, SetAnswer: mangle})
			c, _ := clientOf(t, s)
			_, err := c.Set(ctxOf(t), gnmi.SetRequest{Update: []gnmi.SetValue{{Path: mustPath(t, "/system/config"), JSON: []byte(`{"hostname":"r1"}`), Encoding: gnmi.EncodingJSONIETF}}})
			var lost *gnmi.AnswerLostError
			if !errors.As(err, &lost) || !lost.Applied || !strings.Contains(err.Error(), "applied the change") {
				t.Fatalf("Set = %v, want an AnswerLostError saying the change was applied", err)
			}
			if gnmi.Code(err) == codes.InvalidArgument || s.Sets() != 1 {
				t.Errorf("code %v after %d Sets, want the one applied Set never taken for a refusal", gnmi.Code(err), s.Sets())
			}
		})
	}
}

// TestRefusedLogin_IsSentOnce sends a wrong password once, and holds every
// later call to the same refusal without sending anything.
func TestRefusedLogin_IsSentOnce(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	secrets := gnmitest.Secrets(s)
	secrets[wire.SecretPassword] = "wrong-password-1"
	c, _, err := gnmi.ClientFor(gnmitest.NewDevice(t, s), "router1", secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := ctxOf(t)
	_, err = c.Capabilities(ctx)
	var refused *gnmi.RefusedError
	if !errors.As(err, &refused) || !refused.Refused() || refused.NoneSent {
		t.Fatalf("Capabilities = %v, want a RefusedError for a sent credential", err)
	}
	if gnmi.Code(err) != codes.Unauthenticated {
		t.Errorf("Code = %v, want Unauthenticated", gnmi.Code(err))
	}
	if _, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/")}}); !errors.As(err, &refused) {
		t.Errorf("Get after the refusal = %v, want the same refusal", err)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Delete: []gnmi.Path{mustPath(t, "/a")}}); !errors.As(err, &refused) {
		t.Errorf("Set after the refusal = %v, want the same refusal", err)
	}
	if _, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/")}}); !errors.As(err, &refused) {
		t.Errorf("Subscribe after the refusal = %v, want the same refusal", err)
	}
	if s.Refusals() != 1 || len(s.RPCs()) != 1 {
		t.Errorf("server saw %d refusals over %v, want one login only", s.Refusals(), s.RPCs())
	}
}

// TestUntrustedCertificate_FailsClosed points a device at a server whose
// certificate another authority signed, and at the right authority under the
// wrong name, and holds both to an UntrustedError with nothing sent.
func TestUntrustedCertificate_FailsClosed(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	other := gnmitest.New(t, gnmitest.Options{})
	for name, props := range map[string]map[string]inventory.PropertyValue{
		"another authority": {devicetls.CAPEMProperty: other.AuthorityPEM()},
		"the system roots":  {},
		"a mismatched name": {devicetls.CAPEMProperty: s.AuthorityPEM(), devicetls.ServerNameProperty: "router.example.net"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, err := gnmi.ClientFor(gnmitest.NewDeviceWith(t, s, props), "router1", gnmitest.Secrets(s))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_, err = c.Capabilities(ctxOf(t))
			var untrusted *devicetls.UntrustedError
			if !errors.As(err, &untrusted) || !untrusted.Refused() {
				t.Fatalf("Capabilities = %v, want an UntrustedError", err)
			}
			if untrusted.Fingerprint != devicetls.Fingerprint(s.Certificate()) {
				t.Errorf("fingerprint = %s, want the server's", untrusted.Fingerprint)
			}
			if _, err := c.Set(ctxOf(t), gnmi.SetRequest{Delete: []gnmi.Path{mustPath(t, "/a")}}); !errors.As(err, &untrusted) {
				t.Errorf("Set = %v, want the same refusal before anything is sent", err)
			}
		})
	}
	if rpcs := s.RPCs(); len(rpcs) != 0 {
		t.Errorf("server answered %v through an untrusted handshake", rpcs)
	}
}

// TestPinnedFingerprint_Connects reaches a server by its certificate's pin,
// and says so in the warnings, since a renewed certificate needs a new one.
func TestPinnedFingerprint_Connects(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{})
	c, warnings, err := gnmi.ClientFor(gnmitest.NewDeviceWith(t, s, map[string]inventory.PropertyValue{
		devicetls.FingerprintProperty: devicetls.Fingerprint(s.Certificate()),
	}), "router1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Error("no warning for a pinned certificate")
	}
}

// TestMutualTLS_PresentsTheStoredCertificate requires a client certificate,
// and holds the client to presenting the credential's, and to failing the
// handshake without one.
func TestMutualTLS_PresentsTheStoredCertificate(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{ClientCertificate: true})
	c, _ := clientOf(t, s)
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Fatalf("Capabilities with the client certificate = %v", err)
	}
	if md := s.Metadata(); len(md) != 1 || len(md[0].Get("password")) != 0 {
		t.Errorf("metadata = %v, want no password beside a certificate alone", md)
	}
	noCert, _, err := gnmi.ClientFor(gnmitest.NewDeviceWith(t, s, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.AuthorityPEM()}), "router1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = noCert.Close() }()
	if _, err := noCert.Capabilities(ctxOf(t)); err == nil {
		t.Error("Capabilities without a client certificate = nil, want the handshake refused")
	}
}

// TestBearerToken_IsSentAsAuthorizationOnly sends a token, and holds it to
// the authorization metadata alone.
func TestBearerToken_IsSentAsAuthorizationOnly(t *testing.T) {
	const token = "tok-9c2e41d7a0b35f86"
	s := gnmitest.New(t, gnmitest.Options{Token: token})
	c, _ := clientOf(t, s)
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Fatal(err)
	}
	md := s.Metadata()
	if len(md) != 1 || strings.Join(md[0].Get("authorization"), "") != "Bearer "+token {
		t.Fatalf("metadata = %v", md)
	}
	for key, values := range md[0] {
		if key == "authorization" {
			continue
		}
		for _, v := range values {
			if strings.Contains(v, token) {
				t.Errorf("metadata %s = %q carries the token", key, v)
			}
		}
	}
}

// TestErrors_MaskTheCredentialADeviceEchoes has the server answer with the
// password and token it was given, and holds every error to masking both.
func TestErrors_MaskTheCredentialADeviceEchoes(t *testing.T) {
	for name, opts := range map[string]gnmitest.Options{
		"password": {Username: testUser, Password: testPassword},
		"token":    {Token: "tok-5e1b0c9a77d24f13"},
	} {
		t.Run(name, func(t *testing.T) {
			secret := opts.Password + opts.Token
			opts.Hook = func(context.Context, string) error {
				return status.Errorf(codes.PermissionDenied, "user %s with secret %s may not read this", testUser, secret)
			}
			s := gnmitest.New(t, opts)
			c, _ := clientOf(t, s)
			_, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/system")}})
			var e *gnmi.Error
			if !errors.As(err, &e) || e.Code != codes.PermissionDenied {
				t.Fatalf("Get = %v, want the server's PermissionDenied", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q carries the secret", err)
			}
			if !strings.Contains(err.Error(), "pleiades-masked-") || !strings.Contains(err.Error(), testUser) {
				t.Errorf("error %q, want the secret masked and the username kept", err)
			}
		})
	}
}

// TestPlaintext_IsAnOptInAndACredentialAFurtherOne holds a plaintext
// connection to its two opt-ins: none needed for a server that asks no
// credential, a refusal before dialing for one carrying a credential the
// record does not accept over plaintext, and warnings for each that is used.
func TestPlaintext_IsAnOptInAndACredentialAFurtherOne(t *testing.T) {
	open := gnmitest.New(t, gnmitest.Options{Plaintext: true})
	c, warnings := clientOf(t, open)
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Fatal(err)
	}
	if c.Verified() || len(warnings) != 1 {
		t.Errorf("Verified = %v, warnings = %v, want unverified with one plaintext warning", c.Verified(), warnings)
	}

	login := gnmitest.New(t, gnmitest.Options{Plaintext: true, Username: testUser, Password: testPassword})
	device := gnmitest.NewDevice(t, login)
	if _, _, err := gnmi.ClientFor(device, "router1", gnmitest.Secrets(login)); err == nil {
		t.Fatal("ClientFor = nil, want a credential over plaintext refused")
	}
	device.AllowPlaintext = true
	c2, warnings, err := gnmi.ClientFor(device, "router1", gnmitest.Secrets(login))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	if _, err := c2.Capabilities(ctxOf(t)); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want the plaintext one and the credential one", warnings)
	}
	if len(login.RPCs()) != 1 {
		t.Errorf("server saw %v, want one call (the refused client sent nothing)", login.RPCs())
	}
}

// TestNew_RefusesWhatItCannotSendSafely holds New to every refusal it makes
// before anything is dialed.
func TestNew_RefusesWhatItCannotSendSafely(t *testing.T) {
	for name, opts := range map[string]gnmi.Options{
		"no target":          {Plaintext: true, Auth: gnmi.AuthNone},
		"resolver scheme":    {Target: "dns:///router:57400", Plaintext: true, Auth: gnmi.AuthNone},
		"no port":            {Target: "router", Plaintext: true, Auth: gnmi.AuthNone},
		"no tls, no opt-in":  {Target: "router:57400", Auth: gnmi.AuthNone},
		"unknown auth":       {Target: "router:57400", Plaintext: true, Auth: "basic"},
		"password, none":     {Target: "router:57400", Plaintext: true, Auth: gnmi.AuthPassword, AllowPlaintextCredentials: true},
		"bearer, none":       {Target: "router:57400", Plaintext: true, Auth: gnmi.AuthBearer, AllowPlaintextCredentials: true},
		"plaintext password": {Target: "router:57400", Plaintext: true, Auth: gnmi.AuthPassword, Secrets: map[string]string{wire.SecretUsername: "a", wire.SecretPassword: "b"}},
	} {
		if c, err := gnmi.New(opts); err == nil {
			_ = c.Close()
			t.Errorf("%s: New = nil, want a refusal", name)
		}
	}
}

// TestSubscribe_OncePollAndStream reads with each mode: ONCE up to its sync,
// POLL twice around a Set, and STREAM on change until the handler stops it.
func TestSubscribe_OncePollAndStream(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	s.Put(t, "/interfaces/interface[name=e1]/state/oper-status", `"UP"`)
	s.Put(t, "/interfaces/interface[name=e2]/state/oper-status", `"DOWN"`)
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	req := gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/interfaces/interface[name=*]/state")}, Encoding: gnmi.EncodingJSONIETF}

	once, err := c.SubscribeOnce(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(once) != 2 || string(once[0].Updates[0].Value.JSON) != `"UP"` {
		t.Fatalf("ONCE = %+v, want both interfaces", once)
	}

	poll, first, err := c.SubscribePoll(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer poll.Close()
	if len(first) != 2 {
		t.Fatalf("POLL first round = %+v", first)
	}
	s.Put(t, "/interfaces/interface[name=e3]/state/oper-status", `"UP"`)
	second, err := poll.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 3 {
		t.Errorf("POLL second round = %d notifications, want 3 after a third interface", len(second))
	}

	synced, changes := false, 0
	err = c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: req, Mode: gnmi.StreamOnChange}, func(ev gnmi.StreamEvent) error {
		if ev.Sync {
			synced = true
			s.Put(t, "/interfaces/interface[name=e1]/state/oper-status", `"DOWN"`)
			return nil
		}
		if synced {
			changes++
			if changes == 3 {
				return gnmi.ErrStop
			}
		}
		return nil
	})
	if err != nil || !synced || changes != 3 {
		t.Errorf("STREAM = %v, synced %v, %d changes after it", err, synced, changes)
	}
}

// TestStratumMode_RefusesAGetInJSON answers as Stratum did: any credential,
// and a Get in JSON refused with its own words.
func TestStratumMode_RefusesAGetInJSON(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Stratum: true, Plaintext: true})
	p := mustPath(t, "/interfaces/interface[name=s1-eth1]/state/admin-status")
	s.PutValue(p, &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "UP"}})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	_, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{p}, Encoding: gnmi.EncodingJSONIETF})
	var e *gnmi.Error
	if !errors.As(err, &e) || e.Code != codes.InvalidArgument || !strings.Contains(e.Message, gnmitest.StratumGetRefusal) {
		t.Fatalf("Get in JSON_IETF = %v, want Stratum's refusal", err)
	}
	ns, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{p}, Encoding: gnmi.EncodingProto})
	if err != nil {
		t.Fatal(err)
	}
	if v := ns[0].Updates[0].Value; v.Type != gnmi.TypeString || string(v.JSON) != `"UP"` {
		t.Errorf("Get in PROTO = %+v", v)
	}
}

// TestGNOI_ReadsTimeFilesAndCertificates calls the gNOI subset.
func TestGNOI_ReadsTimeFilesAndCertificates(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword, Files: map[string]uint64{"/etc/hostname": 7}})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	now, err := c.Time(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(now); d < -time.Minute || d > time.Minute {
		t.Errorf("Time = %s, %s from here", now, d)
	}
	files, err := c.FileStat(ctx, "/etc/hostname")
	if err != nil || len(files) != 1 || files[0].Size != 7 {
		t.Errorf("FileStat = %+v, %v", files, err)
	}
	if _, err := c.FileStat(ctx, "/missing"); gnmi.Code(err) != codes.NotFound {
		t.Errorf("FileStat of a missing file = %v, want NotFound", err)
	}
	if _, err := c.FileStat(ctx, "bad\x00path"); err == nil {
		t.Error("FileStat of a path holding NUL = nil, want it refused")
	}
	certs, err := c.Certificates(ctx)
	if err != nil || len(certs) != 1 || certs[0].Fingerprint != devicetls.Fingerprint(s.Certificate()) || certs[0].Type != "CT_X509" {
		t.Errorf("Certificates = %+v, %v", certs, err)
	}
}
