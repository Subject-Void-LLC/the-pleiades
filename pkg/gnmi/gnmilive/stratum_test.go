// Package gnmilive: pkg/gnmi against Stratum's stratum_bmv2, for framing: a
// second, independently written gNMI server, over plaintext with no
// authentication (what it serves), answering Get in PROTO alone.
package gnmilive

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// stratumClient is a plaintext client of Stratum sending no credential.
func stratumClient(t *testing.T, s server) *gnmi.Client {
	t.Helper()
	c, err := gnmi.New(gnmi.Options{Target: s.target, Plaintext: true, Auth: gnmi.AuthNone})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestStratum_FramingCapabilitiesGetAndSubscribe holds the client to
// Stratum's answers as measured on 2026-11-15: gNMI 0.7.0, twenty-two
// models, PROTO alone, a Get in JSON_IETF refused with its own words and one
// in PROTO answered, and a ONCE subscription up to its sync.
func TestStratum_FramingCapabilitiesGetAndSubscribe(t *testing.T) {
	s := startStratum(t)
	c := stratumClient(t, s)
	ctx := ctxOf(t)
	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Version != "0.7.0" || len(caps.Models) != 22 || !caps.Supports(gnmi.EncodingProto) || caps.Supports(gnmi.EncodingJSONIETF) {
		t.Errorf("Capabilities = %s, %d models, %v", caps.Version, len(caps.Models), caps.Encodings)
	}
	state := mustPath(t, "/interfaces/interface[name=s1-eth1]/state/admin-status")
	_, err = c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{state}, Encoding: gnmi.EncodingJSONIETF})
	var e *gnmi.Error
	if gnmi.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROTO") {
		t.Errorf("Get in JSON_IETF = %v, want Stratum's refusal", err)
	} else if !asError(err, &e) {
		t.Errorf("Get in JSON_IETF = %T, want an *Error", err)
	}
	ns, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{state}, Encoding: gnmi.EncodingProto})
	if err != nil {
		t.Errorf("Get in PROTO = %v", err)
	} else if len(ns) == 0 || len(ns[0].Updates) == 0 || ns[0].Updates[0].Value.Type != gnmi.TypeString {
		t.Errorf("Get in PROTO = %+v", ns)
	}
	once, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/interfaces/interface[name=s1-eth1]/state")}, Encoding: gnmi.EncodingJSONIETF})
	if err != nil || len(once) == 0 {
		t.Fatalf("ONCE = %d notifications, %v", len(once), err)
	}
	if now, err := c.Time(ctx); err != nil || now.IsZero() {
		t.Errorf("gNOI Time = %v, %v", now, err)
	}
}

// TestStratum_TakesAnyCredential measures, rather than assumes, that
// Stratum authenticates nobody: a wrong password over a plaintext connection
// the record accepted is answered.
func TestStratum_TakesAnyCredential(t *testing.T) {
	s := startStratum(t)
	c, err := gnmi.New(gnmi.Options{Target: s.target, Plaintext: true, Auth: gnmi.AuthPassword, AllowPlaintextCredentials: true,
		Secrets: map[string]string{wire.SecretUsername: "nobody", wire.SecretPassword: "certainly-wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Errorf("Capabilities with a wrong password = %v; Stratum answered it on 2026-11-15", err)
	}
}

// TestStratum_OnboardsOverPlaintextWithItsWarnings runs onboarding's prober
// against a plaintext generic_gnmi record, and holds it to saying so.
func TestStratum_OnboardsOverPlaintextWithItsWarnings(t *testing.T) {
	s := startStratum(t)
	device, err := buildGNMI(map[string]inventory.PropertyValue{
		generic.GRPCTargetProperty: s.target, generic.GRPCPlaintextProperty: true, generic.GNMIAuthProperty: gnmi.AuthNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	prober, _ := onboard.Lookup(generic.TypeGNMI)
	probed, err := prober.Probe(ctxOf(t), device, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(probed.Warnings, "\n")
	if probed.Facts["gnmi_model_count"] != "22" || !strings.Contains(joined, "without TLS") || !strings.Contains(joined, "nothing proves which server answered") {
		t.Errorf("probe = %+v", probed)
	}
}

// asError reports whether err is an *gnmi.Error, setting target.
func asError(err error, target **gnmi.Error) bool {
	e, ok := err.(*gnmi.Error)
	if ok {
		*target = e
	}
	return ok
}
