// A canceled call must not keep a recovering target's half-open probe,
// in either of the package's two retry loops.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestConnect_ACanceledCallDoesNotKeepTheProbe proves a call whose
// context is already done cannot walk off with a recovering target's
// half-open probe.
//
// The half-open probe is handed out by Allow and given back only by a
// recorded outcome. Both retry loops used to call Allow first and check
// the context second, and a done context returned between the two with
// nothing recorded, so the circuit stayed half-open with no probe in
// flight and every later call was refused without a dial. That is
// FAILURE_PATTERNS 146's wedge reached through a second door: a canceled
// task, a timed-out one, or an operator's interrupt, landing just after a
// cooldown ran out, made a device unreachable for the life of the
// process. As in TestConnect_ProbeSurvivesToTheDial, the assertion is on
// DIALS, because the wedge's error message looks perfectly reasonable.
func TestConnect_ACanceledCallDoesNotKeepTheProbe(t *testing.T) {
	const cooldown = 20 * time.Millisecond

	var dialCalls int32
	dial := func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("simulated unreachable target")
	}
	r := newTestRunner(dial, Options{MaxRetries: 1, BreakerThreshold: 1, BreakerCooldown: cooldown})

	// One failure opens the circuit, and the cooldown then runs out.
	if _, err := r.Connect(context.Background(), nil, testTarget, testAuth); err == nil {
		t.Fatal("expected the first dial to fail")
	}
	time.Sleep(cooldown + 20*time.Millisecond)

	// A call that was canceled before it started arrives first.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Connect(canceled, nil, testTarget, testAuth); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call: error = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 1 {
		t.Fatalf("dial attempts after the canceled call = %d, want still 1", got)
	}

	// A live call must still get the probe and reach the dial.
	if _, err := r.Connect(context.Background(), nil, testTarget, testAuth); err == nil {
		t.Fatal("expected the probe dial to fail")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 2 {
		t.Fatalf("dial attempts after a live call = %d, want 2: the canceled call kept the half-open probe, so this target is now unreachable for the life of the process", got)
	}
}

// TestDialFinalLegWithRetry_ACanceledCallDoesNotKeepTheProbe is the same
// proof for DialThroughHops' final leg, which has its own copy of the
// retry loop and so its own copy of the ordering.
func TestDialFinalLegWithRetry_ACanceledCallDoesNotKeepTheProbe(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	const addr = "recovering:1"

	var dialCalls int32
	dial := func(context.Context, string) (net.Conn, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("simulated unreachable target")
	}
	r := New(Options{MaxRetries: 1, BreakerThreshold: 1, BreakerCooldown: cooldown})

	if _, err := r.dialFinalLegWithRetry(context.Background(), dial, addr); err == nil {
		t.Fatal("expected the first dial to fail")
	}
	time.Sleep(cooldown + 20*time.Millisecond)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.dialFinalLegWithRetry(canceled, dial, addr); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call: error = %v, want context.Canceled", err)
	}

	if _, err := r.dialFinalLegWithRetry(context.Background(), dial, addr); err == nil {
		t.Fatal("expected the probe dial to fail")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 2 {
		t.Fatalf("dial attempts after a live call = %d, want 2: the canceled call kept the half-open probe", got)
	}
}
