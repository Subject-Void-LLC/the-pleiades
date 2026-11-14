// A dial its caller canceled is not counted against the address's circuit,
// in either of the package's two retry loops, while a dial whose own
// deadline passed still is: pkg/winrmexec's rule for the same two cases.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// TestConnect_ACanceledDialIsNotCounted proves a handshake its caller
// canceled leaves the circuit as it was, against a real listener that
// accepts the connection and never speaks, and that a handshake whose own
// deadline passed against the same listener is counted.
//
// A canceled dial was recorded as a failure, so a run stopped while its
// tasks were dialing (an operator's interrupt, a Runner told to stop) could
// open a healthy address's circuit for every other caller of the process.
// pkg/winrmexec records nothing for a cancellation (FAILURE_PATTERNS 1207).
func TestConnect_ACanceledDialIsNotCounted(t *testing.T) {
	host, portText, err := net.SplitHostPort(silentListener(t))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Host: host, Port: port}
	r := New(Options{InsecureSkipHostKeyVerify: true, MaxRetries: 1, BreakerThreshold: 1, BreakerCooldown: time.Hour})

	canceled, cancel := context.WithCancel(context.Background())
	stop := time.AfterFunc(100*time.Millisecond, cancel)
	defer stop.Stop()
	if _, err := r.Connect(canceled, nil, target, PasswordAuth("u", "p")); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("canceled dial: err = %v, want the handshake given up", err)
	}

	// Dialed, not refused: the canceled dial opened nothing. Its own
	// deadline then passes, which is the address not answering.
	timed, cancelTimed := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelTimed()
	if _, err := r.Connect(timed, nil, target, PasswordAuth("u", "p")); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("after a canceled dial: err = %v, want a dial, not the circuit open: the cancellation was counted", err)
	}

	// That one counted: a threshold of one is reached.
	bounded, cancelBounded := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelBounded()
	if _, err := r.Connect(bounded, nil, target, PasswordAuth("u", "p")); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("after a dial whose deadline passed: err = %v, want the circuit open", err)
	}
}

// TestDialFinalLegWithRetry_ACanceledDialIsNotCounted is the same proof for
// DialThroughHops' final leg, which has its own copy of the retry loop.
func TestDialFinalLegWithRetry_ACanceledDialIsNotCounted(t *testing.T) {
	const addr = "silent:1"
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, errors.New("tcp dial: operation was canceled or timed out")
	}
	r := New(Options{MaxRetries: 1, BreakerThreshold: 1, BreakerCooldown: time.Hour})

	canceled, cancel := context.WithCancel(context.Background())
	stop := time.AfterFunc(50*time.Millisecond, cancel)
	defer stop.Stop()
	if _, err := r.dialFinalLegWithRetry(canceled, dial, addr); err == nil {
		t.Fatal("expected the canceled dial to fail")
	}
	if !r.breaker.Permitted(addr) {
		t.Fatal("a canceled final-leg dial opened the circuit")
	}

	timed, cancelTimed := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTimed()
	if _, err := r.dialFinalLegWithRetry(timed, dial, addr); err == nil {
		t.Fatal("expected the timed-out dial to fail")
	}
	if r.breaker.Permitted(addr) {
		t.Fatal("a final-leg dial whose deadline passed did not open a threshold-1 circuit")
	}
}
