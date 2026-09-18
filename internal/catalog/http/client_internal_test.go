// Package http: the one property of the request client that cannot be
// observed from outside this package in a test anybody would tolerate
// running.
//
// The behavior at stake is that a TLS handshake may take as long as the
// task's own timeout allows. Proving that from the outside needs a server
// whose handshake stalls for longer than the old ten-second cap, so the
// test would have to run for over ten seconds to assert anything, and
// would assert it by waiting rather than by checking. This asserts the
// wiring instead, which is exactly where the defect was: the verifying
// path returned a client with no transport at all, so it silently used
// http.DefaultTransport's fixed ten seconds.
package http

import (
	nethttp "net/http"
	"testing"
	"time"
)

// TestClient_HandshakeBudgetIsTheTaskTimeout is the regression test for a
// documented parameter that could not do what it said.
//
// "timeout" is described to operators as how long to wait for the whole
// request. It bounded the request context and nothing else, so a handshake
// against a loaded device or a high-latency link failed after ten seconds
// no matter what number an operator put there. It also bit this
// repository's own suite: under full parallel load a local handshake took
// longer than ten seconds and TestRequest_VerifiesCertificatesByDefault
// failed reporting a handshake timeout where it expected a certificate
// rejection.
//
// Both paths are checked. They are built differently, and it was the
// verifying one, the default, that had the bug.
func TestClient_HandshakeBudgetIsTheTaskTimeout(t *testing.T) {
	for _, tc := range []struct {
		name          string
		validateCerts bool
	}{
		{"verifying, the default", true},
		{"verification skipped", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const budget = 45 * time.Second
			spec := requestSpec{timeout: budget, validateCerts: tc.validateCerts}

			client := spec.client()
			transport, ok := client.Transport.(*nethttp.Transport)
			if !ok {
				t.Fatalf("client carries transport %T, want its own *http.Transport: a nil transport means the shared http.DefaultTransport, whose handshake cap this method cannot reach", client.Transport)
			}
			if transport.TLSHandshakeTimeout != budget {
				t.Errorf("TLSHandshakeTimeout = %s, want the task's own %s: a handshake must not have a deadline the timeout parameter cannot raise",
					transport.TLSHandshakeTimeout, budget)
			}
		})
	}
}

// TestClient_DoesNotShareATransportBetweenTasks is the property the
// original doc comment claimed and the verifying path did not have.
//
// Two tasks must not share a connection pool, because a pooled connection
// established without certificate verification could otherwise be handed
// to a later task that asked for it. The skip-verify path always cloned;
// the verifying path returned a bare client and so shared the process-wide
// default with everything else in the binary.
func TestClient_DoesNotShareATransportBetweenTasks(t *testing.T) {
	first := requestSpec{timeout: time.Second, validateCerts: true}.client()
	second := requestSpec{timeout: time.Second, validateCerts: true}.client()

	if first.Transport == second.Transport {
		t.Error("two tasks were handed the same transport, so they share one connection pool")
	}
	if first.Transport == nethttp.DefaultTransport {
		t.Error("a task was handed http.DefaultTransport, the pool every other client in this process uses")
	}
}

// TestClient_OnlySkipsVerificationWhenAsked keeps the security property
// pinned to the flag now that both paths build a transport the same way.
//
// The refactor that gave the verifying path its own transport made it
// possible to set a TLS config on the wrong branch, and that mistake would
// not fail any existing test: a client that skipped verification would
// pass every test asserting a request succeeds.
func TestClient_OnlySkipsVerificationWhenAsked(t *testing.T) {
	verifying := requestSpec{timeout: time.Second, validateCerts: true}.client()
	tr, ok := verifying.Transport.(*nethttp.Transport)
	if !ok {
		t.Fatalf("transport is %T", verifying.Transport)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("the verifying path built a client that skips certificate verification")
	}

	skipping := requestSpec{timeout: time.Second, validateCerts: false}.client()
	tr, ok = skipping.Transport.(*nethttp.Transport)
	if !ok {
		t.Fatalf("transport is %T", skipping.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("validate_certs: false did not actually skip verification")
	}
}
