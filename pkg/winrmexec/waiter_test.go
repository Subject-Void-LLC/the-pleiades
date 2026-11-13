// Tests for the Waiter: what it sends before and after a listener answers,
// that a refused credential is sent once, and what it tells the endpoint's
// circuit. The listener and the refusal are real loopback servers; the
// authenticated success, which needs a whole WinRM service, is the
// Waiter's run seam, and the real-host gate in cmd/pleiades runs the rest.
package winrmexec

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// passwordAuth is the credential every test here waits with.
var passwordAuth = Auth{Username: "lab", Password: "pw"}

// fastWaiter is a Waiter for target whose floor costs no real time: its
// sleeps are recorded and move its clock.
func fastWaiter(target Target) (*Waiter, *[]time.Duration) {
	w := NewWaiter(target, passwordAuth, Options{Timeout: 5 * time.Second})
	var slept []time.Duration
	clock := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }
	w.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		clock = clock.Add(d)
		return nil
	}
	return w, &slept
}

// TestWaiter_AWrongPasswordIsSentOnce proves a host refusing the credential
// hears it once in a whole wait: the first try that finds the listener
// sends it, the refusal ends the wait, and every later try returns that
// refusal without sending anything at all.
func TestWaiter_AWrongPasswordIsSentOnce(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	host := newRefusingHost(t)
	w, _ := fastWaiter(host.target(t))

	_, err := w.Run(context.Background(), Command{Shell: ShellPowerShell, Script: probeScript})
	var refused *CredentialRefusedError
	if !errors.As(err, &refused) || errors.Is(err, ErrNotServing) {
		t.Fatalf("first try: err = %v, want the refusal, ending the wait", err)
	}
	afterFirst := host.requests.Load()
	if got := host.withCredential.Load(); got != 1 {
		t.Fatalf("the host judged %d logon attempts in the first try, want 1", got)
	}
	for i := 0; i < 5; i++ {
		if err := w.Probe(context.Background()); !errors.As(err, &refused) {
			t.Fatalf("later try %d: err = %v, want the same refusal", i, err)
		}
	}
	if host.requests.Load() != afterFirst || w.Sends() != 1 {
		t.Fatalf("later tries sent %d more requests and %d sends in all, want none and 1",
			host.requests.Load()-afterFirst, w.Sends())
	}
	if !strings.Contains(err.Error(), "refused the credential") {
		t.Errorf("err = %v, want it to say the credential was refused", err)
	}
}

// TestWaiter_SendsNoCredentialUntilTheListenerAnswers proves a listener
// that is not WinRM's yet (http.sys's 503 for a service still starting) is
// asked with no credential, try after try, and the credential goes only to
// the 401 that a serving listener gives.
func TestWaiter_SendsNoCredentialUntilTheListenerAnswers(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	host := newRefusingHost(t)
	host.serving.Store(false)
	w, slept := fastWaiter(host.target(t))

	for i := 0; i < 3; i++ {
		err := w.Probe(context.Background())
		if !errors.Is(err, ErrNotServing) || !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("try %d: err = %v, want not serving yet, naming the 503", i, err)
		}
	}
	if host.authorized.Load() != 0 || host.requests.Load() != 3 || w.Sends() != 0 {
		t.Fatalf("before the listener answered: %d of %d requests carried an Authorization header, %d sends; want 0 of 3, 0",
			host.authorized.Load(), host.requests.Load(), w.Sends())
	}
	// The floor spaced the tries: none started within a second of the last.
	for _, d := range *slept {
		if d <= 0 || d > WaitIntervalFloor {
			t.Errorf("slept %s between tries, want up to the %s floor", d, WaitIntervalFloor)
		}
	}
	if len(*slept) != 2 {
		t.Errorf("slept %d times between 3 tries on a clock that does not move, want 2", len(*slept))
	}

	host.serving.Store(true)
	var refused *CredentialRefusedError
	if err := w.Probe(context.Background()); !errors.As(err, &refused) {
		t.Fatalf("once serving: err = %v, want the credential sent and refused", err)
	}
	if host.withCredential.Load() != 1 || w.Sends() != 1 {
		t.Fatalf("once serving: %d logon attempts, %d sends; want 1 and 1", host.withCredential.Load(), w.Sends())
	}
}

// TestWaiter_IsNeitherCountedNorRefused proves a wait against a host that
// is down adds nothing to its circuit however long it tries, and still
// tries when other callers have opened it.
func TestWaiter_IsNeitherCountedNorRefused(t *testing.T) {
	b := useCircuits(t, breaker.DefaultThreshold, time.Hour)
	target := Target{Host: "127.0.0.1", Port: refusing.Port(t)}
	addr := Addr(target, passwordAuth, Options{})
	w, _ := fastWaiter(target)
	// The listener is taken as answering, so every try goes on to the real
	// authenticated call, which the closed port refuses: the half that
	// would be counted, and refused, if the wait went through the circuit.
	w.listen = func(context.Context) error { return nil }
	for i := 0; i < 3*breaker.DefaultThreshold; i++ {
		if err := w.Probe(context.Background()); !errors.Is(err, ErrNotServing) {
			t.Fatalf("try %d: err = %v, want not serving yet", i, err)
		}
	}
	if !b.Permitted(addr) {
		t.Fatal("the wait's failures opened the circuit")
	}

	for i := 0; i < breaker.DefaultThreshold; i++ {
		_ = probe(target)
	}
	if b.Permitted(addr) {
		t.Fatal("five counted failures did not open the circuit")
	}
	err := w.Probe(context.Background())
	if errors.Is(err, breaker.ErrOpen) || !errors.Is(err, ErrNotServing) {
		t.Fatalf("a wait on an open circuit: err = %v, want a real try, not a refusal", err)
	}
}

// TestWaiter_ASuccessClosesTheCircuitAndAListenerEndsItsCooldown proves
// what a wait tells the circuit other callers opened: a listener heard
// answering ends the cooldown, so the next counted call is the probe, and
// a command that ran closes the circuit, so the task after the wait is sent
// at once. The success is the run seam, since a whole WinRM service is the
// real-host gate's to answer.
func TestWaiter_ASuccessClosesTheCircuitAndAListenerEndsItsCooldown(t *testing.T) {
	for name, tc := range map[string]struct {
		run    error
		closed bool
	}{
		"a command that ran":           {nil, true},
		"a listener, then no shell":    {&NotStartedError{Err: errors.New("connection reset")}, false},
		"a listener, then a fault":     {&NotStartedError{Err: errors.New("the service is starting")}, false},
		"a listener, then a refusal":   {&NotStartedError{Err: errLoginRefused}, false},
		"a listener, then a mid-error": {errors.New("winrm: receiving output: connection reset"), false},
	} {
		t.Run(name, func(t *testing.T) {
			b := useCircuits(t, 1, time.Hour)
			target := Target{Host: "win1", Port: 5985}
			addr := Addr(target, passwordAuth, Options{})
			b.RecordFailure(addr) // another caller opened it
			w, _ := fastWaiter(target)
			w.listen = func(context.Context) error { return nil }
			w.run = func(context.Context, Command) (Result, error) { return Result{Stdout: "pleiades-probe"}, tc.run }

			_ = w.Probe(context.Background())
			if !b.Allow(addr) {
				t.Fatal("after the listener answered the next counted call was refused, want the cooldown over")
			}
			// Allow above took the probe if the circuit was still open; a
			// closed circuit lets a second caller through as well.
			if second := b.Allow(addr); second != tc.closed {
				t.Fatalf("closed = %v, want %v", second, tc.closed)
			}
		})
	}
}

// TestWaiter_StopsOnAMistakeAndOnCancellation proves the two other ways a
// wait ends: a credential or command it can never send, before anything is
// sent, and a caller giving up. A try's own deadline is not one of them.
func TestWaiter_StopsOnAMistakeAndOnCancellation(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	host := newRefusingHost(t)
	target := host.target(t)

	w := NewWaiter(target, Auth{}, Options{})
	if err := w.Probe(context.Background()); err == nil || errors.Is(err, ErrNotServing) {
		t.Fatalf("no credential: err = %v, want the mistake, ending the wait", err)
	}
	w = NewWaiter(target, passwordAuth, Options{})
	if _, err := w.Run(context.Background(), Command{Shell: ShellPowerShell}); err == nil || errors.Is(err, ErrNotServing) {
		t.Fatalf("an empty script: err = %v, want the mistake, ending the wait", err)
	}
	if host.requests.Load() != 0 {
		t.Fatalf("a mistake sent %d requests", host.requests.Load())
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Probe(canceled); !errors.Is(err, context.Canceled) || errors.Is(err, ErrNotServing) {
		t.Fatalf("a canceled wait: err = %v, want context.Canceled", err)
	}
	expired, stop := context.WithTimeout(context.Background(), -time.Second)
	defer stop()
	if err := w.Probe(expired); !errors.Is(err, ErrNotServing) {
		t.Fatalf("a try whose own deadline passed: err = %v, want not serving yet", err)
	}
}

// TestWaitUntilReachable_AWrongPasswordEndsIt proves the reconnect wait
// exec.winrm.shell runs after an expected disconnect stops at the first
// refusal, rather than sending the password every interval until its
// timeout.
func TestWaitUntilReachable_AWrongPasswordEndsIt(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	host := newRefusingHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	err := WaitUntilReachable(ctx, host.target(t), passwordAuth, Options{}, 100*time.Millisecond)
	var refused *CredentialRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "after 1 attempt(s)") {
		t.Fatalf("err = %v, want it stopped by the refusal after one attempt", err)
	}
	if time.Since(start) > 5*time.Second || host.withCredential.Load() != 1 {
		t.Fatalf("it took %s and sent the credential %d times, want at once and once", time.Since(start), host.withCredential.Load())
	}
}

// TestCredentialRefused names each transport's refusal and leaves the
// network's failures out.
func TestCredentialRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"the sealed handshake": {errors.Join(errors.New("winrm"), errLoginRefused), true},
		"the library's NTLM":   {errors.New("http response error: 401 - invalid content type"), true},
		"a TLS alert":          {&NotStartedError{Err: errorsOp("remote error")}, true},
		"a certificate's 403":  {errors.New("winrm: https://w:5986/wsman answered 403 with \"text/html\" rather than SOAP"), true},
		"a refused connection": {&NotStartedError{Err: errorsOp("dial")}, false},
		"a fault":              {errors.New("winrm: the service is starting"), false},
	} {
		if got := credentialRefused(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// errorsOp is a *net.OpError for op, the shape Go gives a dial failure and
// a TLS alert alike.
func errorsOp(op string) error { return &net.OpError{Op: op, Err: errors.New("x")} }
