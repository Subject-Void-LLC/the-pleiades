// Tests that a refusal reaching the shared WinRM circuit is recorded as an
// answer and nothing more: a credential the host refuses, through the real
// Execute against a host that speaks NTLM and refuses every password, and a
// certificate this side refuses, against a real TLS listener whose
// certificate no authority this process trusts vouches for. Neither clears
// an outage's failure streak, and neither closes an open circuit.
package winrmexec

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// refusingTLSHost starts a TLS listener whose certificate is httptest's own,
// which nothing in this process trusts, and returns where it listens. The
// handshakes the client refuses are not logged: each is the test's point.
func refusingTLSHost(t *testing.T) Target {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return Target{Host: u.Hostname(), Port: port}
}

// refusal is one way a host's answer is a refusal: the call that gets it,
// the options the call's address is keyed with, and how to tell it is that
// refusal.
type refusal struct {
	target func(t *testing.T) Target
	opts   Options
	isIt   func(error) bool
}

// refusals are the two refusals the shared circuit records as answers.
var refusals = map[string]refusal{
	"a refused credential": {
		target: func(t *testing.T) Target { return newRefusingHost(t).target(t) },
		opts:   Options{Timeout: 5 * time.Second},
		isIt:   credentialRefused,
	},
	"a refused certificate": {
		target: refusingTLSHost,
		opts:   Options{HTTPS: true, Timeout: 5 * time.Second},
		isIt:   certificateRefused,
	},
}

// call is one counted call to target with a password credential and opts.
func call(target Target, opts Options) error {
	_, err := Run(context.Background(), target, Auth{Username: "lab", Password: "pw"}, ShellPowerShell, "'x'", opts)
	return err
}

// TestExecute_ARefusalDoesNotClearAnOutagesStreak proves failures to reach
// a host still add up to the threshold when a refusal comes between them.
// The failures before and after it are recorded against the very circuit
// Execute records into; the refusal is the host's own, through Execute.
//
// A refusal was recorded as a success, which cleared the streak, so a host
// whose logins timed out and were refused by turns never opened its circuit
// (FAILURE_PATTERNS 1230).
func TestExecute_ARefusalDoesNotClearAnOutagesStreak(t *testing.T) {
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			b := useCircuits(t, 3, time.Hour)
			target := tc.target(t)
			addr := Addr(target, Auth{Username: "lab"}, tc.opts)
			b.RecordFailure(addr)
			b.RecordFailure(addr)

			err := call(target, tc.opts)
			var notStarted *NotStartedError
			if err == nil || !tc.isIt(err) || !errors.As(err, &notStarted) || notStarted.Unreached() {
				t.Fatalf("err = %v, want the refusal, before any shell and not unreached", err)
			}

			b.RecordFailure(addr)
			if b.Permitted(addr) {
				t.Fatal("three failures with a refusal between them left the circuit closed: the refusal cleared the streak")
			}
		})
	}
}

// TestExecute_ARefusedProbeDoesNotCloseTheCircuit proves a half-open probe
// answered with a refusal hands the probe on and leaves the circuit open:
// the next caller is the probe at once, and a caller beside it is still
// refused, as beside any probe.
func TestExecute_ARefusedProbeDoesNotCloseTheCircuit(t *testing.T) {
	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			b := useCircuits(t, 2, time.Hour)
			target := tc.target(t)
			addr := Addr(target, Auth{Username: "lab"}, tc.opts)
			b.RecordFailure(addr)
			b.RecordFailure(addr)
			b.EndCooldown(addr)

			// The probe goes out and is refused.
			if err := call(target, tc.opts); err == nil || !tc.isIt(err) {
				t.Fatalf("probe: err = %v, want the refusal", err)
			}

			ctx := context.Background()
			if err := endpoints.admit(ctx, addr); err != nil {
				t.Fatalf("the caller after a refused probe: err = %v, want the probe handed on at once", err)
			}
			if err := endpoints.admit(ctx, addr); !errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("a caller beside the next probe: err = %v, want the circuit still open: the refused probe closed it", err)
			}
		})
	}
}

// TestExecute_RefusedCertificatesNeverOpenTheCircuit proves a host whose
// certificate is refused every time is never refused as if it were down.
func TestExecute_RefusedCertificatesNeverOpenTheCircuit(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	target := refusingTLSHost(t)
	opts := Options{HTTPS: true, Timeout: 5 * time.Second}
	for i := 0; i < 3*breaker.DefaultThreshold; i++ {
		err := call(target, opts)
		if err == nil || errors.Is(err, breaker.ErrOpen) || !certificateRefused(err) {
			t.Fatalf("call %d: err = %v, want the certificate refused and the circuit closed", i, err)
		}
	}
}
