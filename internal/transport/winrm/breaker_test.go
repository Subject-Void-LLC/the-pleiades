// Tests for the WinRM Adapter's circuit breaker: what opens a circuit,
// what never does, and how a probe closes one. Like winrm_test.go they
// stand in for winrmexec.Execute, so they prove the Adapter's policy; the
// real-host gate in cmd/pleiades proves the network side.
package winrm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// probeCooldown is the cooldown the probe tests below use. It must be
// longer than the retry loop's first backoff (retryBase plus up to ten
// percent jitter): with a shorter one the loop's own second attempt finds
// the cooldown already over and takes the probe itself, which is correct
// behavior but not the behavior those tests are about.
const probeCooldown = 3 * retryBase

// TestBreaker_OpensOnNetworkFailuresAndThenSendsNothing proves the
// shipped defaults: five network failures in a row against one address
// open its circuit, the retry loop stops as soon as it opens, and every
// call after that is refused without reaching winrmexec at all.
func TestBreaker_OpensOnNetworkFailuresAndThenSendsNothing(t *testing.T) {
	tr, calls := recording(fails(refused))

	// Three attempts, three failures.
	if _, err := tr.Exec(context.Background(), target, cred, "x"); err == nil {
		t.Fatal("expected the first call to fail")
	}
	// Two more reach the default threshold of five, and the third attempt
	// is refused by the circuit rather than sent.
	_, err := tr.Exec(context.Background(), target, cred, "x")
	if !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("second call: err = %v, want the circuit to open partway through", err)
	}
	if len(*calls) != breaker.DefaultThreshold {
		t.Fatalf("calls = %d, want %d: the loop kept sending after the circuit opened", len(*calls), breaker.DefaultThreshold)
	}

	// Now the look before the loop refuses, and nothing is sent.
	_, err = tr.Exec(context.Background(), target, cred, "x")
	if !errors.Is(err, breaker.ErrOpen) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("open circuit: err = %v, want a refusal saying nothing was sent", err)
	}
	if len(*calls) != breaker.DefaultThreshold {
		t.Fatalf("calls = %d after the circuit opened, want no more", len(*calls))
	}
}

// TestBreaker_ASilentHostOpensIt proves a host that never answers opens
// its circuit like one that refuses the connection: the operation's
// deadline passing before any shell opened is a network failure.
func TestBreaker_ASilentHostOpensIt(t *testing.T) {
	tr, calls := recording(fails(silentHost))
	for i := 0; i < 2; i++ {
		_, _ = tr.Exec(context.Background(), target, cred, "x")
	}
	if len(*calls) != breaker.DefaultThreshold {
		t.Fatalf("calls = %d, want the circuit open after %d", len(*calls), breaker.DefaultThreshold)
	}
	if _, err := tr.Exec(context.Background(), target, cred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("err = %v, want the circuit open", err)
	}
}

// TestBreaker_AnAnswerNeverOpensIt proves only the network opens a
// circuit. Each of these fails every call, forever, and none of them may
// cost a different task to the same address its turn: a wrong credential
// or a refused certificate on one task must not become "circuit open"
// for every other task sharing this transport.
func TestBreaker_AnAnswerNeverOpensIt(t *testing.T) {
	for name, failure := range map[string]error{
		"a rejected credential":             badCredential,
		"a certificate refused by TLS":      tlsAlert,
		"a failure after the command began": midCommand,
		"a mistake in the task itself":      authorMistake,
		"the caller canceling":              canceledBeforeShell,
	} {
		t.Run(name, func(t *testing.T) {
			tr, calls := recording(fails(failure))
			for i := 0; i < 3*breaker.DefaultThreshold; i++ {
				_, err := tr.Exec(context.Background(), target, cred, "x")
				if errors.Is(err, breaker.ErrOpen) {
					t.Fatalf("call %d: the circuit opened on %v", i, failure)
				}
			}
			if len(*calls) != 3*breaker.DefaultThreshold {
				t.Fatalf("calls = %d, want every call sent", len(*calls))
			}
		})
	}
}

// TestBreaker_AProbeTheHostAnswersClosesIt proves the half-open probe
// works through the Adapter: after the cooldown one call is let through,
// and a host that answers it, even with a refusal, closes the circuit.
func TestBreaker_AProbeTheHostAnswersClosesIt(t *testing.T) {
	answer := refused
	tr, calls := recording(func() (winrmexec.Result, error) { return winrmexec.Result{}, answer })
	tr.breaker = breaker.New(1, probeCooldown)

	if _, err := tr.Exec(context.Background(), target, cred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("first call: err = %v, want the circuit open after one failure", err)
	}
	sent := len(*calls)

	time.Sleep(probeCooldown + 50*time.Millisecond)
	answer = badCredential // the host is back, and refuses this credential
	if _, err := tr.Exec(context.Background(), target, cred, "x"); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("probe: err = %v, want the host's own refusal", err)
	}
	if len(*calls) != sent+1 {
		t.Fatalf("calls = %d, want exactly one probe sent", len(*calls))
	}

	// The answer closed the circuit, so the next call is sent at once.
	if _, err := tr.Exec(context.Background(), target, cred, "x"); errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("after the probe: err = %v, want the circuit closed", err)
	}
	if len(*calls) != sent+2 {
		t.Fatalf("calls = %d, want the call after the probe sent", len(*calls))
	}
}

// TestBreaker_ACanceledCallDoesNotTakeTheProbe is FAILURE_PATTERNS 398
// for this transport: a call that arrives already canceled once the
// cooldown has run out must leave the probe for the next live call.
func TestBreaker_ACanceledCallDoesNotTakeTheProbe(t *testing.T) {
	tr, calls := recording(fails(refused))
	tr.breaker = breaker.New(1, probeCooldown)

	if _, err := tr.Exec(context.Background(), target, cred, "x"); err == nil {
		t.Fatal("expected the first call to fail")
	}
	time.Sleep(probeCooldown + 50*time.Millisecond)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.Exec(canceled, target, cred, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call: err = %v, want context.Canceled", err)
	}
	sent := len(*calls)

	// The probe fails, as this host still refuses, and reopens the
	// circuit; what matters is that it was sent at all.
	if _, err := tr.Exec(context.Background(), target, cred, "x"); err == nil {
		t.Fatal("expected the live call's probe to fail")
	}
	if len(*calls) != sent+1 {
		t.Fatalf("calls = %d, want the live call's probe sent: the canceled call kept it", len(*calls))
	}
}

// TestBreaker_KeyedByTheAddressDialed proves the circuit belongs to the
// address winrmexec really dials: a bracketed and a bare IPv6 literal are
// one device and share a circuit, while another port on the same host is
// a different listener with a circuit of its own.
func TestBreaker_KeyedByTheAddressDialed(t *testing.T) {
	endpoint := func(host string, port int) transport.Target {
		return transport.Target{Endpoint: transport.NetworkEndpoint{Host: host, Port: port}}
	}
	tr, calls := recording(fails(refused))
	tr.breaker = breaker.New(1, time.Hour)

	// One failure opens the bare literal's circuit; the loop's second
	// attempt meets it.
	if _, err := tr.Exec(context.Background(), endpoint("2001:db8::1", 5985), cred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("bare literal: err = %v, want its circuit open", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	if _, err := tr.Exec(context.Background(), endpoint("[2001:db8::1]", 5985), cred, "x"); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("bracketed literal: err = %v, want the same circuit, already open", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want the bracketed literal refused unsent", len(*calls))
	}
	// Another port is sent: its circuit is its own, and closed.
	_, _ = tr.Exec(context.Background(), endpoint("2001:db8::1", 5986), cred, "x")
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want the other port's call sent on a circuit of its own", len(*calls))
	}
}
