// Package gnmi_test: the request prefix's target, which a server serving more
// than one data tree requires (SONiC refuses every request naming none with
// Unimplemented "Empty target data not supported", measured 2026-11-16).
package gnmi_test

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// ocYANG is SONiC's target for its OpenConfig tree.
const ocYANG = "OC-YANG"

// targetedClient is a client of s through a device whose record names
// target, closed when t ends.
func targetedClient(t *testing.T, s *gnmitest.Server, target string) *gnmi.Client {
	t.Helper()
	device := gnmitest.NewDevice(t, s)
	device.PrefixTarget = target
	c, _, err := gnmi.ClientFor(device, "switch1", gnmitest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestPrefixTarget_ANamelessRequestIsRefusedAsSONiCRefusesIt holds the test
// server to SONiC's measured answer, so the tests below prove the target is
// what lets a request through.
func TestPrefixTarget_ANamelessRequestIsRefusedAsSONiCRefusesIt(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{RequireTarget: ocYANG})
	s.Put(t, "/a/b", `1`)
	c, _ := clientOf(t, s)
	_, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}})
	if gnmi.Code(err) != codes.Unimplemented || !strings.Contains(err.Error(), gnmitest.EmptyTargetRefusal) {
		t.Fatalf("Get naming no target = %v, want Unimplemented %q", err, gnmitest.EmptyTargetRefusal)
	}
}

// TestPrefixTarget_EveryCallCarriesTheDevicesTarget sends Get, Set and each
// subscription mode through a device whose record names a target, and holds
// each request's prefix to carrying it, in Path.target and nowhere else: the
// server reads the prefix's elements as its path, so a target sent as an
// element would be read under it and found absent. The answers echo the
// target, as SONiC's do, and it never becomes an element of a path read back.
func TestPrefixTarget_EveryCallCarriesTheDevicesTarget(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{RequireTarget: ocYANG})
	s.Put(t, "/a/b", `1`)
	c := targetedClient(t, s, ocYANG)
	if c.PrefixTarget() != ocYANG {
		t.Fatalf("PrefixTarget = %q", c.PrefixTarget())
	}
	ctx := ctxOf(t)
	read := gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/a")}, Encoding: gnmi.EncodingJSONIETF}
	notes, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}, Encoding: gnmi.EncodingJSONIETF})
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	for _, n := range notes {
		if n.Prefix.Origin != "" || len(n.Prefix.Elems) != 0 {
			t.Errorf("the echoed target was read as part of the prefix: %+v", n.Prefix)
		}
		for _, u := range n.Updates {
			if got := gnmi.Join(n.Prefix, u.Path).String(); got != "/a/b" {
				t.Errorf("Get answered %s, want /a/b with no target in it", got)
			}
		}
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: mustPath(t, "/a/c"), JSON: []byte(`2`)}}}); err != nil {
		t.Fatalf("Set = %v", err)
	}
	if _, err := c.Set(ctx, gnmi.SetRequest{Prefix: mustPath(t, "/a"), Update: []gnmi.SetValue{{Path: mustPath(t, "/d"), JSON: []byte(`3`)}}}); err != nil {
		t.Fatalf("Set under a prefix = %v", err)
	}
	if _, ok := s.Value(t, "/a/d"); !ok {
		t.Error("the Set under /a with a target did not write /a/d: the prefix's elements were lost beside the target")
	}
	once, err := c.SubscribeOnce(ctx, read)
	if err != nil || len(once) == 0 {
		t.Fatalf("SubscribeOnce = %v, %v", once, err)
	}
	for _, n := range once {
		if len(n.Prefix.Elems) != 0 {
			t.Errorf("the echoed target was read as an element: %+v", n.Prefix)
		}
	}
	poll, _, err := c.SubscribePoll(ctx, read)
	if err != nil {
		t.Fatalf("SubscribePoll = %v", err)
	}
	if _, err := poll.Poll(); err != nil {
		t.Fatalf("Poll = %v", err)
	}
	poll.Close()
	if err := c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: read}, func(e gnmi.StreamEvent) error {
		if e.Sync {
			return gnmi.ErrStop
		}
		return nil
	}); err != nil {
		t.Fatalf("SubscribeStream = %v", err)
	}
	targets := s.Targets()
	if len(targets) != 6 {
		t.Fatalf("the server saw %d targeted requests (%v), want 6", len(targets), targets)
	}
	for i, got := range targets {
		if got != ocYANG {
			t.Errorf("request %d named target %q, want %q", i, got, ocYANG)
		}
	}
}

// TestPrefixTarget_ARequestsOwnTargetWins sends a request naming its own
// target through a client whose device names another.
func TestPrefixTarget_ARequestsOwnTargetWins(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{RequireTarget: ocYANG})
	s.Put(t, "/a/b", `1`)
	c := targetedClient(t, s, "CONFIG_DB")
	if _, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}, PrefixTarget: ocYANG}); err != nil {
		t.Fatalf("Get naming its own target = %v", err)
	}
	if _, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}}); gnmi.Code(err) != codes.InvalidArgument {
		t.Errorf("Get taking the device's target = %v, want the server's refusal of CONFIG_DB", err)
	}
	if _, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}, PrefixTarget: gnmi.NoPrefixTarget}); gnmi.Code(err) != codes.Unimplemented {
		t.Errorf("Get naming no target over the device's = %v, want the server's refusal of none", err)
	}
	if got := s.Targets(); len(got) != 3 || got[0] != ocYANG || got[1] != "CONFIG_DB" || got[2] != "" {
		t.Errorf("targets = %q, want [%s CONFIG_DB \"\"]", got, ocYANG)
	}
}

// TestPrefixTarget_OnlyAPlainIdentifierIsSent refuses a target that is not a
// plain identifier, on the device and on a request, before anything is sent:
// a slash, a bracket or a space could make it read as a path.
func TestPrefixTarget_OnlyAPlainIdentifierIsSent(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{})
	for _, bad := range []string{"OC-YANG/interfaces", "a[b=c]", "two words", "-lead", "x" + strings.Repeat("y", 128), "nul\x00"} {
		if err := gnmi.ValidatePrefixTarget(bad); err == nil {
			t.Errorf("ValidatePrefixTarget(%q) = nil", bad)
		}
		device := gnmitest.NewDevice(t, s)
		device.PrefixTarget = bad
		if _, _, err := gnmi.ClientFor(device, "switch1", nil); err == nil {
			t.Errorf("ClientFor with target %q = nil error", bad)
		}
	}
	for _, good := range []string{"", ocYANG, "CONFIG_DB", "COUNTERS_DB", "dev1.lab"} {
		if err := gnmi.ValidatePrefixTarget(good); err != nil {
			t.Errorf("ValidatePrefixTarget(%q) = %v", good, err)
		}
	}
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	p := []gnmi.Path{mustPath(t, "/a")}
	for name, call := range map[string]func() error{
		"get": func() error { _, err := c.Get(ctx, gnmi.GetRequest{Paths: p, PrefixTarget: "a/b"}); return err },
		"set": func() error {
			_, err := c.Set(ctx, gnmi.SetRequest{Delete: p, PrefixTarget: "a/b"})
			return err
		},
		"once": func() error {
			_, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: p, PrefixTarget: "a/b"})
			return err
		},
	} {
		err := call()
		var e *gnmi.Error
		if err == nil || errors.As(err, &e) {
			t.Errorf("%s with a bad target = %v, want a refusal before sending", name, err)
		}
	}
	if rpcs := s.RPCs(); len(rpcs) != 0 {
		t.Errorf("server saw %v, want nothing sent", rpcs)
	}
}
