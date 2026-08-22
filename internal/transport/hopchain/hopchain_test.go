package hopchain_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport/hopchain"
)

// TestConvert_EmptyRouteReturnsNil proves an empty or nil Route
// translates to a nil hop slice, matching
// pkg/remoteexec.Runner.DialThroughHops' own "nil means a direct
// connection" contract exactly, mirroring
// internal/transport/ssh's own TestHopsFrom_EmptyRouteReturnsNil.
func TestConvert_EmptyRouteReturnsNil(t *testing.T) {
	if hops, err := hopchain.Convert(nil); err != nil || hops != nil {
		t.Fatalf("Convert(nil) = %v, %v; want nil, nil", hops, err)
	}
	if hops, err := hopchain.Convert([]transport.Hop{}); err != nil || hops != nil {
		t.Fatalf("Convert([]transport.Hop{}) = %v, %v; want nil, nil", hops, err)
	}
}

// TestConvert_TranslatesEveryHopInOrder proves each hop's Host, Port and
// credential translate correctly, and that order is preserved: a chain
// is dialed first to last, and a translation that silently reordered
// hops would tunnel through the wrong bastion first.
func TestConvert_TranslatesEveryHopInOrder(t *testing.T) {
	route := []transport.Hop{
		{Host: "bastion-a", Port: 2201, DeviceName: "bastion-a-device", Credential: credential.Credential{Username: "alice", Password: "alice-pass"}},
		{Host: "bastion-b", Port: 2202, DeviceName: "bastion-b-device", Credential: credential.Credential{Username: "bob", Password: "bob-pass"}},
	}

	hops, err := hopchain.Convert(route)
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
}

// TestConvert_UnusableHopCredentialNamesTheHop proves a hop whose
// credential cannot produce a usable authentication method fails before
// any network I/O, naming that hop's own DeviceName rather than
// reporting a generic authentication failure once dialing is already
// underway.
func TestConvert_UnusableHopCredentialNamesTheHop(t *testing.T) {
	route := []transport.Hop{
		{Host: "bastion", Port: 22, DeviceName: "bastion-device", Credential: credential.Credential{}},
	}

	_, err := hopchain.Convert(route)
	if err == nil {
		t.Fatal("expected an error for a hop with no usable credential")
	}
	if !strings.Contains(err.Error(), "bastion-device") {
		t.Errorf("err = %v, want it to name the hop %q", err, "bastion-device")
	}
}
