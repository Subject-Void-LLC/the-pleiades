// Tests for checkHost: a host value that would move the WS-Man URL's
// authority is refused before any request, so the listener dialed is the
// one the record names and the one its circuit is keyed by.
package winrmexec

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// TestCheckHost names what a host may and may not hold.
func TestCheckHost(t *testing.T) {
	for host, ok := range map[string]bool{
		"win1":              true,
		"win1.lab.example":  true,
		"192.168.56.31":     true,
		"2001:db8::1":       true,
		"[2001:db8::1]":     true,
		"fe80::1%eth0":      true,
		"":                  false,
		"win1/x":            false,
		"win1?a=b":          false,
		"win1#frag":         false,
		"user@win1":         false,
		`win1\share`:        false,
		"win1 evil":         false,
		"win1\tevil":        false,
		"win1\x00":          false,
		"win1\u2028":        false,
		"evil.example/x:80": false,
	} {
		if err := checkHost(host); (err == nil) != ok {
			t.Errorf("checkHost(%q) = %v, want accepted %v", host, err, ok)
		}
	}
}

// TestExecute_AHostThatWouldMoveTheURLIsRefusedUncounted proves the
// refusal comes before the circuit and before any traffic: a listener
// stands where the injected value would have sent the request, and hears
// nothing, and the circuit for the real host is untouched.
func TestExecute_AHostThatWouldMoveTheURLIsRefusedUncounted(t *testing.T) {
	b := useCircuits(t, 1, time.Hour)
	host := newRefusingHost(t)
	target := host.target(t)
	injected := Target{Host: target.Host + ":" + strconv.Itoa(target.Port) + "/x"}
	for i := 0; i < 3; i++ {
		if err := probe(injected); err == nil || errors.Is(err, breaker.ErrOpen) || Retryable(err) {
			t.Fatalf("try %d: err = %v, want the host refused as a mistake", i, err)
		}
		w := NewWaiter(injected, passwordAuth, Options{})
		if err := w.Probe(context.Background()); err == nil || errors.Is(err, ErrNotServing) {
			t.Fatalf("wait %d: err = %v, want the host refused, ending the wait", i, err)
		}
	}
	if host.requests.Load() != 0 {
		t.Fatalf("the listener behind the injected value heard %d requests", host.requests.Load())
	}
	if !b.Permitted(Addr(injected, passwordAuth, Options{})) {
		t.Fatal("a refused host was counted against a circuit")
	}
}

// FuzzCheckHost_KeepsTheURLAuthority holds the property checkHost exists
// for: any host it accepts, joined to a port as Addr and the WS-Man URL
// join it, gives a URL whose port and path are the ones meant, or one that
// does not parse at all and so reaches nothing.
func FuzzCheckHost_KeepsTheURLAuthority(f *testing.F) {
	for _, seed := range []string{"win1", "[2001:db8::1]", "fe80::1%eth0", "a/b", "a?b", "a#b", "u@h", "a:b", "a]:1/[", "%2F", "h\\x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		if checkHost(host) != nil {
			return
		}
		u := url.URL{Scheme: "http", Host: net.JoinHostPort(unbracket(host), "5985"), Path: "/wsman"}
		parsed, err := url.Parse(u.String())
		if err != nil {
			return
		}
		if parsed.Port() != "5985" || parsed.Path != "/wsman" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatalf("host %q gave %q: port %q path %q user %v query %q fragment %q", host, u.String(),
				parsed.Port(), parsed.Path, parsed.User, parsed.RawQuery, parsed.Fragment)
		}
	})
}
