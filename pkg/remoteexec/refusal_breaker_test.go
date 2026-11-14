// A refused login or host key against a real SSH server on loopback, between
// dials that fail the way an unreachable address does: the refusal neither
// clears the failure streak nor closes an open circuit, and a half-open
// probe it answers goes to the next caller at once.
package remoteexec

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// flakyPath is the network between a Runner and a real loopback server:
// while down is set every dial fails as an unreachable address does, and
// otherwise it is a real dial and handshake. dials counts every attempt.
type flakyPath struct {
	down  atomic.Bool
	dials atomic.Int32
}

// dial implements dialFunc.
func (p *flakyPath) dial(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	p.dials.Add(1)
	if p.down.Load() {
		return nil, errors.New("tcp dial: simulated unreachable address")
	}
	return realDial(ctx, addr, config)
}

// refusalCase is one kind of refusal: a Runner whose dials go through path,
// the server it reaches, the Auth the server refuses, and a check that an
// error is that refusal.
type refusalCase struct {
	runner  *Runner
	target  Target
	refused Auth
	isIt    func(error) bool
}

// refusalCases builds one Runner per refusal this package records as an
// answer, each dialing through path with the given breaker settings: a
// password the server refuses, and a host key known_hosts does not name.
func refusalCases(t *testing.T, path *flakyPath, threshold int, cooldown time.Duration) map[string]refusalCase {
	t.Helper()
	opts := Options{MaxRetries: 1, BreakerThreshold: threshold, BreakerCooldown: cooldown}

	passwordTarget, _ := startCountingSSHListener(t)
	insecure := opts
	insecure.InsecureSkipHostKeyVerify = true
	password := New(insecure)
	password.dial = path.dial

	hostKeyTarget, _, _ := startHandshakeCountingSSHListener(t)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, knownHosts, hostKeyTarget, nil)
	checked := opts
	checked.KnownHostsPath = knownHosts
	hostKey := New(checked)
	hostKey.dial = path.dial

	return map[string]refusalCase{
		"a refused password": {password, passwordTarget, PasswordAuth("u", "wrong"), authRejected},
		"a refused host key": {hostKey, hostKeyTarget, PasswordAuth("u", rightPassword), func(err error) bool {
			var keyErr *knownhosts.KeyError
			return errors.As(err, &keyErr)
		}},
	}
}

// TestConnect_ARefusalDoesNotClearAnOutagesStreak proves failures to reach
// an address still add up to the threshold when the address refuses a login
// between them.
//
// A refusal was recorded as the breaker's success, which cleared the streak
// (FAILURE_PATTERNS 1230): an AAA server that timed out some logins and
// refused the rest reset the count at every refusal, so the circuit never
// opened and every task kept dialing, and sending its credential, into it.
func TestConnect_ARefusalDoesNotClearAnOutagesStreak(t *testing.T) {
	path := &flakyPath{}
	for name, tc := range refusalCases(t, path, 3, time.Hour) {
		t.Run(name, func(t *testing.T) {
			path.down.Store(true)
			for i := 0; i < 2; i++ {
				if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); err == nil || errors.Is(err, breaker.ErrOpen) {
					t.Fatalf("failure %d: err = %v, want the unreachable address", i+1, err)
				}
			}
			path.down.Store(false)
			if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); err == nil || !tc.isIt(err) {
				t.Fatalf("err = %v, want the refusal", err)
			}
			path.down.Store(true)
			if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); err == nil || errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("failure 3: err = %v, want the unreachable address", err)
			}

			before := path.dials.Load()
			_, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused)
			if !errors.Is(err, breaker.ErrOpen) || path.dials.Load() != before {
				t.Fatalf("after three failures with a refusal between them: err = %v and %d more dials, want the circuit open and none: the refusal cleared the streak",
					err, path.dials.Load()-before)
			}
		})
	}
}

// TestConnect_ARefusedProbeDoesNotCloseTheCircuit proves a half-open probe
// the address answers with a refusal leaves the circuit open: the next
// caller is the probe, and when that one fails the circuit is open again
// at once, as after any failed probe.
//
// When the refusal was a success it closed the circuit with a clean streak,
// so the next failure counted as the first of a new run and the calls after
// it went on dialing an address with no evidence it was back.
func TestConnect_ARefusedProbeDoesNotCloseTheCircuit(t *testing.T) {
	const cooldown = 30 * time.Millisecond
	path := &flakyPath{}
	for name, tc := range refusalCases(t, path, 2, cooldown) {
		t.Run(name, func(t *testing.T) {
			path.down.Store(true)
			for i := 0; i < 2; i++ {
				_, _ = tc.runner.Connect(context.Background(), nil, tc.target, tc.refused)
			}
			if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); !errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("err = %v, want the circuit open after two failures", err)
			}
			time.Sleep(cooldown + 20*time.Millisecond)

			// The probe reaches the server, which refuses it.
			path.down.Store(false)
			if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); err == nil || !tc.isIt(err) {
				t.Fatalf("probe: err = %v, want the refusal", err)
			}
			// The next caller is the probe now, and the address is down again.
			path.down.Store(true)
			if _, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused); err == nil || errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("next probe: err = %v, want it dialed and failed", err)
			}

			before := path.dials.Load()
			_, err := tc.runner.Connect(context.Background(), nil, tc.target, tc.refused)
			if !errors.Is(err, breaker.ErrOpen) || path.dials.Load() != before {
				t.Fatalf("after a failed probe: err = %v and %d more dials, want the circuit open and none: the refused probe closed it",
					err, path.dials.Load()-before)
			}
		})
	}
}

// TestConnect_ARightPasswordAfterARefusedProbeIsSentAtOnce proves the cost
// of a wrong password answering the probe is that caller's turn and nothing
// more: the next caller, with the right password, is the probe at once,
// without waiting another cooldown, and its login closes the circuit.
func TestConnect_ARightPasswordAfterARefusedProbeIsSentAtOnce(t *testing.T) {
	const cooldown = 300 * time.Millisecond
	path := &flakyPath{}
	target, presented := startCountingSSHListener(t)
	r := newTestRunner(path.dial, Options{MaxRetries: 1, BreakerThreshold: 2, BreakerCooldown: cooldown})

	path.down.Store(true)
	for i := 0; i < 2; i++ {
		_, _ = r.Connect(context.Background(), nil, target, PasswordAuth("u", "wrong"))
	}
	time.Sleep(cooldown + 20*time.Millisecond)
	path.down.Store(false)

	if _, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", "wrong")); err == nil || !authRejected(err) {
		t.Fatalf("probe: err = %v, want the refusal", err)
	}
	start := time.Now()
	conn, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	if err != nil {
		t.Fatalf("the right password after a refused probe: %v", err)
	}
	_ = conn.Close()
	if took := time.Since(start); took >= cooldown {
		t.Errorf("the right password took %s, a whole cooldown: the refused probe was not handed back", took)
	}
	if got := atomic.LoadInt32(presented); got != 2 {
		t.Errorf("the server was sent %d passwords, want 2: the refused one once and the right one", got)
	}
	// Closed: two more calls in a row go straight through.
	for i := 0; i < 2; i++ {
		conn, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
		if err != nil {
			t.Fatalf("call %d after the circuit closed: %v", i, err)
		}
		_ = conn.Close()
	}
}
