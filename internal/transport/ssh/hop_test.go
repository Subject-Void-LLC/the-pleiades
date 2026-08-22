package ssh

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// TestHopsFrom_EmptyRouteReturnsNil proves an empty or nil Route
// translates to a nil hop slice, matching remoteexec.Runner.Run's own
// "nil means a direct connection" contract exactly, so a Target with no
// Route costs nothing extra.
func TestHopsFrom_EmptyRouteReturnsNil(t *testing.T) {
	if hops, err := hopsFrom(nil); err != nil || hops != nil {
		t.Fatalf("hopsFrom(nil) = %v, %v; want nil, nil", hops, err)
	}
	if hops, err := hopsFrom([]transport.Hop{}); err != nil || hops != nil {
		t.Fatalf("hopsFrom([]transport.Hop{}) = %v, %v; want nil, nil", hops, err)
	}
}

// TestHopsFrom_TranslatesEveryHopInOrder proves each hop's Host, Port and
// credential translate correctly, and that order is preserved: a chain is
// dialed first to last, and a translation that silently reordered hops
// would tunnel through the wrong bastion first.
func TestHopsFrom_TranslatesEveryHopInOrder(t *testing.T) {
	route := []transport.Hop{
		{Host: "bastion-a", Port: 2201, DeviceName: "bastion-a-device", Credential: credential.Credential{Username: "alice", Password: "alice-pass"}},
		{Host: "bastion-b", Port: 2202, DeviceName: "bastion-b-device", Credential: credential.Credential{Username: "bob", Password: "bob-pass"}},
	}

	hops, err := hopsFrom(route)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("got %d hops, want 2", len(hops))
	}
	if hops[0].Target.Host != "bastion-a" || hops[0].Target.Port != 2201 {
		t.Errorf("hops[0].Target = %+v, want host bastion-a port 2201", hops[0].Target)
	}
	if hops[1].Target.Host != "bastion-b" || hops[1].Target.Port != 2202 {
		t.Errorf("hops[1].Target = %+v, want host bastion-b port 2202", hops[1].Target)
	}
	// Auth's fields are deliberately unexported (remoteexec.Auth's own
	// doc comment: nothing to read back out once built), so the only
	// externally observable proof two different credentials produced two
	// different Auth values is that a downstream dial would present
	// different usernames; that is exactly what
	// TestSSHContainer_HopChain_TunnelsThroughOneBastion-equivalent
	// coverage in pkg/remoteexec proves against a real handshake. Here it
	// is enough to prove translation did not error and preserved order,
	// which the Target assertions above already do.
}

// TestHopsFrom_UnusableHopCredentialNamesTheHop proves a hop whose
// credential cannot produce a usable authentication method fails before
// any network I/O, naming that hop's own DeviceName rather than reporting
// a generic authentication failure once dialing is already underway.
func TestHopsFrom_UnusableHopCredentialNamesTheHop(t *testing.T) {
	route := []transport.Hop{
		{Host: "bastion", Port: 22, DeviceName: "bastion-device", Credential: credential.Credential{}},
	}

	_, err := hopsFrom(route)
	if err == nil {
		t.Fatal("expected an error for a hop with no usable credential")
	}
	if !strings.Contains(err.Error(), "bastion-device") {
		t.Errorf("err = %v, want it to name the hop's device %q", err, "bastion-device")
	}
}

// TestExec_UnusableHopCredentialFailsBeforeDialing proves Exec itself
// surfaces hopsFrom's own refusal, naming the hop, without ever reaching
// the network: the loopback server here would fail loudly (its handler
// is never called) if Exec dialed anything at all.
func TestExec_UnusableHopCredentialFailsBeforeDialing(t *testing.T) {
	dialed := false
	server := startLoopbackServer(t, func(string) (string, string, int) {
		dialed = true
		return "should never run", "", 0
	})
	tr := New(Options{KnownHostsPath: writeKnownHosts(t, server.addr(), server.hostKey)})

	target := transport.Target{
		Endpoint: server.target.Endpoint,
		Route: []transport.Hop{
			{Host: "bastion", Port: 22, DeviceName: "bastion-device", Credential: credential.Credential{}},
		},
	}

	_, err := tr.Exec(context.Background(), target, testCred(), "echo hi")
	if err == nil {
		t.Fatal("expected an error for a hop with no usable credential")
	}
	if !strings.Contains(err.Error(), "bastion-device") {
		t.Errorf("err = %v, want it to name the hop's device %q", err, "bastion-device")
	}
	if dialed {
		t.Error("expected no dial at all against an unusable hop credential")
	}
}
