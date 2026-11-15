// Package gnmilive: pkg/gnmi against Nokia SR Linux 26.7.2, the real gNMI
// target: its own self-signed certificate (refused until pinned), its
// password login, JSON_IETF, Set's all-or-none transaction, Subscribe in each
// mode, and gNOI. A change made over gNMI is read back over SR Linux's own
// command line (sr_cli in the container), a second path to the same running
// configuration, so a test never only reads back what it wrote through the
// session that wrote it.
package gnmilive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// ctxOf is a context bounded for one test.
func ctxOf(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

// client is a pinned client of SR Linux with its login, closed when t ends.
func client(t *testing.T, s server) *gnmi.Client {
	t.Helper()
	c, err := srlClient(s.target, srlPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// srCLI reads from SR Linux's own command line (sr_cli in the container),
// independent of gNMI.
func srCLI(t *testing.T, s server, command string) string {
	t.Helper()
	out, err := s.srl.CLI(ctxOf(t), command, false)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSRLinux_ItsCertificateIsRefusedUntilPinned holds TLS to failing closed
// on SR Linux's self-signed certificate (no IP subject alternative name), to
// a wrong pin, and to connecting once the presented certificate is pinned.
func TestSRLinux_ItsCertificateIsRefusedUntilPinned(t *testing.T) {
	s := startSRLinux(t)
	for name, props := range map[string]map[string]inventory.PropertyValue{
		"the system roots":       nil,
		"a wrong pin":            {devicetls.FingerprintProperty: "sha256:" + strings.Repeat("ab", 32)},
		"its own name, unpinned": {devicetls.ServerNameProperty: "srl"},
	} {
		device, err := srlDevice(s.target, props)
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := gnmi.ClientFor(device, "srl", srlSecrets(srlPassword))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Capabilities(ctxOf(t))
		_ = c.Close()
		var untrusted *devicetls.UntrustedError
		if !errors.As(err, &untrusted) || untrusted.Fingerprint != srlPin {
			t.Errorf("%s: Capabilities = %v, want the certificate refused, naming the one presented", name, err)
		}
	}
	caps, err := client(t, s).Capabilities(ctxOf(t))
	if err != nil {
		t.Fatal(err)
	}
	// Measured 2026-11-15 on 26.7.2: gNMI 0.10.0; PROTO, ASCII and
	// JSON_IETF, never plain JSON; every model named by a URN ending in its
	// module, its version a revision date.
	if caps.Version == "" || !caps.Supports(gnmi.EncodingJSONIETF) || caps.Supports(gnmi.EncodingJSON) || len(caps.Models) < 100 {
		t.Errorf("Capabilities = version %q, encodings %v, %d models", caps.Version, caps.Encodings, len(caps.Models))
	}
	modules, unread := caps.YANGModules()
	if len(unread) != 0 || len(modules) != len(caps.Models) || modules[0].Revision == "" {
		t.Errorf("YANG modules %d of %d models, unread %v, first %+v", len(modules), len(caps.Models), unread, modules[0])
	}
	if caps.Dropped != 0 || len(caps.VendorEncodings) == 0 {
		t.Errorf("dropped %d %v, vendor encodings %v", caps.Dropped, caps.DroppedSamples, caps.VendorEncodings)
	}
	t.Logf("gNMI %s, %d models, encodings %v and vendor encodings %v", caps.Version, len(caps.Models), caps.Encodings, caps.VendorEncodings)
}

// TestSRLinux_AWrongPasswordIsRefusedAndNotSentAgain holds a wrong password
// to SR Linux's UNAUTHENTICATED, and every later call to the same refusal.
func TestSRLinux_AWrongPasswordIsRefusedAndNotSentAgain(t *testing.T) {
	s := startSRLinux(t)
	c, err := srlClient(s.target, "Not-The-Password-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Capabilities(ctxOf(t))
	var refused *gnmi.RefusedError
	if !errors.As(err, &refused) || gnmi.Code(err) != codes.Unauthenticated {
		t.Fatalf("Capabilities = %v, want the login refused", err)
	}
	if strings.Contains(err.Error(), "Not-The-Password-1") {
		t.Errorf("the refusal %q carries the password", err)
	}
	if _, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/system")}}); !errors.Is(err, refused) && !errors.As(err, &refused) {
		t.Errorf("Get after the refusal = %v", err)
	}
}

// TestSRLinux_SetIsOneTransaction writes a loopback through each operation,
// reads every change back over gNMI and over sr_cli, and holds a Set with
// one bad path to changing nothing at all.
func TestSRLinux_SetIsOneTransaction(t *testing.T) {
	s := startSRLinux(t)
	c := client(t, s)
	ctx := ctxOf(t)
	lo := mustPath(t, "/interface[name=lo17]")
	t.Cleanup(func() { _, _ = c.Set(context.Background(), gnmi.SetRequest{Delete: []gnmi.Path{lo}}) })

	// SR Linux answers a path it holds nothing at with an empty
	// notification, not NOT_FOUND (measured 2026-11-15).
	ns, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{lo}, Encoding: gnmi.EncodingJSONIETF, Type: gnmi.DataConfig})
	if err != nil || len(ns) != 1 || len(ns[0].Updates) != 0 {
		t.Fatalf("Get of an absent interface = %+v, %v", ns, err)
	}

	bad := mustPath(t, "/interface[name=lo17]/no-such-leaf")
	_, err = c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{
		{Path: lo, JSON: []byte(`{"admin-state":"enable"}`), Encoding: gnmi.EncodingJSONIETF},
		{Path: bad, JSON: []byte(`"x"`), Encoding: gnmi.EncodingJSONIETF},
	}})
	if gnmi.Code(err) != codes.InvalidArgument {
		t.Fatalf("a Set with an unknown leaf = %v, want InvalidArgument", err)
	}
	if out := srCLI(t, s, "info from running /interface lo17"); strings.Contains(out, "admin-state") {
		t.Fatalf("the refused Set left part of itself behind: %s", out)
	}

	if _, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: lo, JSON: []byte(`{"admin-state":"enable","description":"pleiades gnmilive"}`), Encoding: gnmi.EncodingJSONIETF}}}); err != nil {
		t.Fatal(err)
	}
	if out := srCLI(t, s, "info from running /interface lo17"); !strings.Contains(out, "pleiades gnmilive") {
		t.Errorf("sr_cli does not show the update: %s", out)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Replace: []gnmi.SetValue{{Path: lo, JSON: []byte(`{"admin-state":"disable"}`), Encoding: gnmi.EncodingJSONIETF}}}); err != nil {
		t.Fatal(err)
	}
	if out := srCLI(t, s, "info from running /interface lo17"); strings.Contains(out, "pleiades gnmilive") || !strings.Contains(out, "disable") {
		t.Errorf("sr_cli after a replace: %s", out)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Delete: []gnmi.Path{lo}}); err != nil {
		t.Fatal(err)
	}
	if out := srCLI(t, s, "info from running /interface lo17"); strings.Contains(out, "admin-state") {
		t.Errorf("sr_cli after a delete: %s", out)
	}
	// A delete of what is not there is accepted, as the specification says.
	if _, err := c.Set(ctx, gnmi.SetRequest{Delete: []gnmi.Path{lo}}); err != nil {
		t.Errorf("a delete of an absent path = %v", err)
	}
}

// TestSRLinux_ThroughTheDatastorePort merges and deletes a loopback through
// pkg/datastore's port, as a caller written against NETCONF would, and reads
// each change back over sr_cli.
func TestSRLinux_ThroughTheDatastorePort(t *testing.T) {
	s := startSRLinux(t)
	var store datastore.Store = client(t, s)
	ctx := ctxOf(t)
	p := datastore.Path{Elem: []datastore.PathElem{{Name: "interface", Keys: map[string]string{"name": "lo16"}}}}
	if err := store.SetConfig(ctx, p, datastore.Payload{Encoding: datastore.EncodingJSONIETF, Bytes: []byte(`{"description":"through the port"}`)}, datastore.OperationMerge); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetConfig(ctx, p)
	if err != nil || !strings.Contains(string(got.Bytes), "through the port") {
		t.Fatalf("GetConfig = %s, %v", got.Bytes, err)
	}
	if out := srCLI(t, s, "info from running /interface lo16"); !strings.Contains(out, "through the port") {
		t.Errorf("sr_cli after the merge: %s", out)
	}
	if err := store.SetConfig(ctx, p, datastore.Payload{}, datastore.OperationDelete); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetConfig(ctx, p); err != nil || string(got.Bytes) != "{}" {
		t.Errorf("GetConfig after the delete = %s, %v", got.Bytes, err)
	}
}

// TestSRLinux_SubscribeInEachMode reads an interface's statistics with ONCE,
// POLL and a sampled STREAM.
func TestSRLinux_SubscribeInEachMode(t *testing.T) {
	s := startSRLinux(t)
	c := client(t, s)
	ctx := ctxOf(t)
	req := gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/interface[name=mgmt0]/statistics")}, Encoding: gnmi.EncodingJSONIETF}
	once, err := c.SubscribeOnce(ctx, req)
	if err != nil || len(once) == 0 || len(once[0].Updates) == 0 || !strings.Contains(string(once[0].Updates[0].Value.JSON), "in-octets") {
		t.Fatalf("ONCE = %+v, %v", once, err)
	}
	poll, first, err := c.SubscribePoll(ctx, req)
	if err != nil || len(first) == 0 {
		t.Fatalf("POLL = %+v, %v", first, err)
	}
	second, err := poll.Poll()
	poll.Close()
	if err != nil || len(second) == 0 {
		t.Fatalf("a second poll = %+v, %v", second, err)
	}
	synced, after := false, 0
	err = c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: req, Mode: gnmi.StreamSample, SampleInterval: time.Second}, func(ev gnmi.StreamEvent) error {
		switch {
		case ev.Sync:
			synced = true
		case synced:
			after++
			if after == 3 {
				return gnmi.ErrStop
			}
		}
		return nil
	})
	if err != nil || !synced || after != 3 {
		t.Errorf("STREAM = %v, synced %v, %d samples after it", err, synced, after)
	}
}

// TestSRLinux_GNOI reads the clock and a file through gNOI, and records that
// SR Linux serves certificates over gNSI rather than gNOI's cert service.
func TestSRLinux_GNOI(t *testing.T) {
	s := startSRLinux(t)
	c := client(t, s)
	ctx := ctxOf(t)
	now, err := c.Time(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(now); d < -time.Minute || d > time.Minute {
		t.Errorf("Time = %s, %s from here", now, d)
	}
	files, err := c.FileStat(ctx, "/etc/hostname")
	if err != nil || len(files) != 1 || files[0].Size == 0 {
		t.Errorf("FileStat = %+v, %v", files, err)
	}
	if _, err := c.Certificates(ctx); gnmi.Code(err) != codes.Unimplemented {
		t.Errorf("Certificates = %v, measured Unimplemented on 26.7.2", err)
	}
}
