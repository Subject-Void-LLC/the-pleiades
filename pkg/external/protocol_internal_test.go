// Package external: tests of protocol negotiation.
package external

import (
	"strings"
	"testing"
)

// withProtocols runs a test against another window than this build's.
func withProtocols(t *testing.T, ps []Protocol) {
	t.Helper()
	saved := Protocols
	Protocols = ps
	t.Cleanup(func() { Protocols = saved })
}

func TestNegotiateTakesTheHighestVersionBothSpeak(t *testing.T) {
	// A window of two: 1 deprecated (the N-1), 2 supported (the N).
	withProtocols(t, []Protocol{
		{Version: 1, Status: ProtocolDeprecated, RemovedIn: "0.6.0"},
		{Version: 2, Status: ProtocolSupported},
	})
	for _, tc := range []struct {
		low, high   int
		want        int
		wantStatus  ProtocolStatus
		wantRefusal string
	}{
		{low: 1, high: 1, want: 1, wantStatus: ProtocolDeprecated},
		{low: 1, high: 2, want: 2, wantStatus: ProtocolSupported},
		{low: 2, high: 3, want: 2, wantStatus: ProtocolSupported},
		{low: 1, high: 9, want: 2, wantStatus: ProtocolSupported},
		{low: 3, high: 4, wantRefusal: "describe speaks protocol 3 to 4, and this build speaks protocol 1 (deprecated, removed in 0.6.0), 2"},
		{low: 3, high: 2, wantRefusal: "inverted protocol range, 3 to 2"},
		{low: 0, high: 0, wantRefusal: "names no protocol version"},
	} {
		got, err := Negotiate(tc.low, tc.high)
		switch {
		case tc.wantRefusal != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantRefusal) {
				t.Errorf("%d..%d: error %v, want %q", tc.low, tc.high, err, tc.wantRefusal)
			}
		case err != nil:
			t.Errorf("%d..%d: refused: %v", tc.low, tc.high, err)
		case got.Version != tc.want || got.Status != tc.wantStatus:
			t.Errorf("%d..%d: agreed %+v, want %d %s", tc.low, tc.high, got, tc.want, tc.wantStatus)
		}
	}
}

func TestADescriptionsRangeFallsBackToItsOneVersion(t *testing.T) {
	// A program built before ranges names only protocol.
	if low, high := (Description{Protocol: 1}).Range(); low != 1 || high != 1 {
		t.Errorf("Range() = %d..%d, want 1..1", low, high)
	}
	if low, high := (Description{Protocol: 2, ProtocolMin: 1, ProtocolMax: 2}).Range(); low != 1 || high != 2 {
		t.Errorf("Range() = %d..%d, want 1..2", low, high)
	}
}

func TestProtocolVersionIsTheNewestThisBuildSpeaks(t *testing.T) {
	// A loader from before ranges reads Protocol alone, so a program built
	// with this package must name there the newest version it speaks.
	if _, high := spokenRange(); high != ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, and the newest in Protocols is %d", ProtocolVersion, high)
	}
	for _, p := range Protocols {
		if p.Status == ProtocolDeprecated && p.RemovedIn == "" {
			t.Errorf("protocol %d is deprecated and names no release that removes it", p.Version)
		}
	}
}
