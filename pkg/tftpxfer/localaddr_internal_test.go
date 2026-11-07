// Tests of resolveServer itself: the lookup ends with the task's context,
// and a name or literal with an IPv4 address takes it.
package tftpxfer

import (
	"context"
	"errors"
	"testing"
)

// TestResolveServer_EndsWithItsContext: a lookup under a context already
// ended returns the context's error rather than resolving, which
// net.ResolveUDPAddr, with no context, could not.
func TestResolveServer_EndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveServer(ctx, "tftp-server.pleiades.invalid", 69)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("resolveServer under an ended context = %v, want context.Canceled", err)
	}
}

// TestResolveServer_TakesTheIPv4Address: an IPv4 literal and an
// IPv4-mapped IPv6 literal both resolve to an address localIPFor reads as
// IPv4; an IPv6 literal stays IPv6; a port out of range is refused.
func TestResolveServer_TakesTheIPv4Address(t *testing.T) {
	for host, ipv4 := range map[string]bool{"127.0.0.1": true, "::ffff:127.0.0.1": true, "::1": false} {
		raddr, err := resolveServer(context.Background(), host, 69)
		if err != nil {
			t.Fatalf("resolveServer(%s) = %v", host, err)
		}
		if got := raddr.IP.To4() != nil; got != ipv4 || raddr.Port != 69 {
			t.Errorf("resolveServer(%s) = %v, IPv4 %v; want IPv4 %v on port 69", host, raddr, got, ipv4)
		}
	}
	for _, port := range []int{-1, 65536} {
		if _, err := resolveServer(context.Background(), "127.0.0.1", port); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}
