// Tests for the WinRM Adapter's share of the circuit breaker. The Adapter
// holds none of its own: it calls the real winrmexec.Execute, whose
// endpoint circuit every WinRM caller in the process shares, so these run
// that Execute against a real closed port rather than a stand-in. The
// breaker's own policy (what opens it, what a probe does) is tested beside
// it, in pkg/winrmexec.
package winrm

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// TestWinRMBreaker_OneFailureCountsOnce proves a failed attempt through the
// winrm_exec transport is counted exactly once, in the one circuit every
// WinRM caller shares. The transport retries a refused connection three
// times; two direct winrmexec.Execute calls first make five failures in
// all, the threshold, so:
//
//   - the transport's third attempt is still sent, which fails if its
//     attempts were counted twice (the circuit would open at its second);
//   - the next direct call is refused with nothing sent, which fails if the
//     transport's attempts were not counted in the shared circuit at all,
//     as when it kept a breaker of its own (the shared count would be two).
func TestWinRMBreaker_OneFailureCountsOnce(t *testing.T) {
	defer winrmexec.ResetCircuitsForTest()()
	port := refusing.Port(t)
	opts := winrmexec.Options{Timeout: 5 * time.Second}
	wTarget := winrmexec.Target{Host: "127.0.0.1", Port: port}
	auth := winrmexec.Auth{Username: "lab", Password: "pw"}
	direct := func() error {
		_, err := winrmexec.Run(context.Background(), wTarget, auth, winrmexec.ShellPowerShell, "'x'", opts)
		return err
	}

	for i := 1; i <= 2; i++ {
		if err := direct(); err == nil || errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("direct call %d: err = %v, want the refused connection itself", i, err)
		}
	}

	tr := New(opts)
	tTarget := transport.Target{Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: port}}
	tCred := cred
	if _, err := tr.Exec(context.Background(), tTarget, tCred, "x"); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("the transport's call: err = %v, want its third attempt sent and refused by the closed port; "+
			"a circuit already open means its attempts were counted more than once", err)
	}

	err := direct()
	if !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("the call after five failures: err = %v, want the shared circuit open; the transport's three "+
			"attempts were not counted in it", err)
	}
	var notStarted *winrmexec.NotStartedError
	if !errors.As(err, &notStarted) || !notStarted.Unreached() || winrmexec.Retryable(err) {
		t.Fatalf("the refusal %v must be a NotStartedError read as unreached and never retried", err)
	}

	// The transport is refused the same way, at once, with nothing sent:
	// one attempt, no backoff between retries.
	start := time.Now()
	if _, err := tr.Exec(context.Background(), tTarget, tCred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("the transport after the circuit opened: err = %v, want the shared circuit's refusal", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("the refused transport call took %s; a refusal is not retried and sends nothing", elapsed)
	}
}

// TestWinRMBreaker_KeyedByTheAddressDialed proves the transport and a
// direct caller share one circuit per address and port: another port on
// the same host is a different listener with a circuit of its own.
func TestWinRMBreaker_KeyedByTheAddressDialed(t *testing.T) {
	defer winrmexec.ResetCircuitsForTest()()
	port := refusing.Port(t)
	other := refusing.Port(t)
	tr := New(winrmexec.Options{Timeout: 5 * time.Second})
	at := func(p int) transport.Target {
		return transport.Target{Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: p}}
	}
	for i := 0; i < 5; i++ {
		_, _ = winrmexec.Run(context.Background(), winrmexec.Target{Host: "127.0.0.1", Port: port},
			winrmexec.Auth{Username: "lab", Password: "pw"}, winrmexec.ShellPowerShell, "'x'", winrmexec.Options{Timeout: 5 * time.Second})
	}
	if _, err := tr.Exec(context.Background(), at(port), cred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("port %d after five failures: err = %v, want its circuit open", port, err)
	}
	if _, err := tr.Exec(context.Background(), at(other), cred, "x"); errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("port %s: err = %v, want a circuit of its own, closed", strconv.Itoa(other), err)
	}
}
