// Tests for the credential dial path that reach it from this package: the
// nonce signature every connect presents, a credential source that fails at
// reconnect, the refusals Connect makes before it dials, and the warning
// for a credential that the broker does not ask for.
//
// internal/meshid's gates prove the whole path against a broker that
// requires authentication, but they live in another package and coverage is
// counted per package. This package cannot import internal/meshid to mint a
// real credential either, since meshid imports this package, so these tests
// build one from nkeys with mintTestCredentialAndKey.
package topology_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// TestTheSignatureCallbackSignsTheNonceWithTheCredentialKey proves the
// signature a connect presents is the one a broker accepts.
//
// A broker that requires authentication verifies the nonce signature
// against the user's public key, and nothing else about the connection can
// make up for a signature made with the wrong key. So this verifies the
// callback's output exactly the way the broker does, with the public key
// alone, rather than trusting that a signature came back.
func TestTheSignatureCallbackSignsTheNonceWithTheCredentialKey(t *testing.T) {
	creds, pub := mintTestCredentialAndKey(t)
	opt, err := topology.CredentialOptionForTest(creds)
	if err != nil {
		t.Fatalf("building the option: %v", err)
	}
	var o nats.Options
	if err := opt(&o); err != nil {
		t.Fatalf("applying the option: %v", err)
	}

	nonce := []byte("a nonce the server chose")
	sig, err := o.SignatureCB(nonce)
	if err != nil {
		t.Fatalf("the signature callback refused a well-formed credential: %v", err)
	}

	// Verified with the public key only, which is all a broker holds.
	verifier, err := nkeys.FromPublicKey(pub)
	if err != nil {
		t.Fatalf("parsing the public key: %v", err)
	}
	if err := verifier.Verify(nonce, sig); err != nil {
		t.Fatalf("the signature does not verify under the credential's own public key: %v", err)
	}
	// A signature that verified for any input would prove nothing, so the
	// same signature must fail for a different nonce.
	if err := verifier.Verify([]byte("a different nonce"), sig); err == nil {
		t.Fatal("the signature also verified for a nonce it was not made over")
	}
}

// TestBothCredentialCallbacksReportAFailingSource covers a reconnect whose
// credential source has stopped answering, for example a renewal that
// failed.
//
// nats.go calls these two callbacks on every reconnect and nothing else, so
// they are the only place that failure can surface. It has to arrive as the
// source's own error, so the driver's error handler and the logs name the
// real cause instead of an authentication failure.
//
// The source answers first and fails afterwards, because that is the real
// order and the only one that reaches these callbacks: nats.UserJWT calls
// the jwt callback once while the option is applied, as a smoke test, so a
// source failing from the start is refused at dial instead.
func TestBothCredentialCallbacksReportAFailingSource(t *testing.T) {
	creds := mintTestCredential(t)
	sourceErr := errors.New("the renewal is unavailable")
	renewalFailed := false
	opt, err := topology.CredentialSourceOptionForTest(func() ([]byte, error) {
		if renewalFailed {
			return nil, sourceErr
		}
		return creds, nil
	})
	if err != nil {
		t.Fatalf("building the option: %v", err)
	}
	var o nats.Options
	if err := opt(&o); err != nil {
		t.Fatalf("applying the option: %v", err)
	}

	// The dial succeeded with a good credential; the renewal fails before
	// the next reconnect asks for one.
	renewalFailed = true

	if _, err := o.UserJWT(); !errors.Is(err, sourceErr) {
		t.Errorf("the jwt callback returned %v, want the source's own error", err)
	}
	if _, err := o.SignatureCB([]byte("nonce")); !errors.Is(err, sourceErr) {
		t.Errorf("the signature callback returned %v, want the source's own error", err)
	}
}

// TestConnectRefusesBeforeDialling covers the two refusals Connect makes
// before it opens a socket: a URL over a scheme nats.go does not speak, and
// a credential source that cannot answer.
//
// Both have to fail at once. The URL points at a port nothing listens on,
// so a refusal that happened after the dial would instead take the whole
// connect timeout and be reported as a connectivity problem, which sends
// an operator looking in the wrong place.
func TestConnectRefusesBeforeDialling(t *testing.T) {
	sourceErr := errors.New("the credential store is sealed")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, tc := range []struct {
		name string
		url  string
		opts []topology.ConnectOption
		want func(error) bool
	}{
		{
			name: "a scheme nats.go does not speak",
			url:  "http://127.0.0.1:1",
			want: func(err error) bool { return strings.Contains(err.Error(), "http") },
		},
		{
			name: "a credential source that fails",
			url:  "nats://127.0.0.1:1",
			opts: []topology.ConnectOption{topology.WithCredentialSource(func() ([]byte, error) {
				return nil, sourceErr
			})},
			want: func(err error) bool { return errors.Is(err, sourceErr) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			nc, err := topology.Connect(context.Background(), tc.url, logger, "refusal-test", tc.opts...)
			if err == nil {
				nc.Close()
				t.Fatal("Connect succeeded, want a refusal")
			}
			if !tc.want(err) {
				t.Errorf("Connect error = %v, which does not name the real cause", err)
			}
			// Well under ConnectWaitTimeout, so the refusal cannot have
			// come from a dial that timed out.
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("Connect took %v to refuse, which means it dialled first", elapsed)
			}
		})
	}
}

// TestConnectWarnsWhenTheBrokerDoesNotAskForTheCredential covers the one
// misconfiguration Connect exists to make visible.
//
// A process that presents a credential to a broker that requires none
// connects, works, and looks exactly like a secured deployment, so an
// operator who believes the mesh is closed has no way to learn otherwise.
// This runs that exact arrangement against a real broker with no
// authentication, and asserts both halves: the connection succeeds, since
// credentials first is a legitimate rollout order, and the warning is
// logged.
func TestConnectWarnsWhenTheBrokerDoesNotAskForTheCredential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in short mode")
	}
	records := &recordingHandler{}

	nc, err := topology.Connect(context.Background(), startNats(t), slog.New(records), "credential-test",
		topology.WithCredentials(mintTestCredential(t)))
	if err != nil {
		t.Fatalf("Connect refused a credential the broker does not require: %v", err)
	}
	defer nc.Close()

	// A round trip, so the connection is proven usable and not merely open.
	if err := nc.Flush(); err != nil {
		t.Fatalf("the connection could not reach the broker: %v", err)
	}
	if nc.AuthRequired() {
		t.Fatal("the test broker requires authentication, so this test is not measuring what it claims")
	}

	warning := "this process presented a mesh credential but the broker does not require one"
	if !records.logged(slog.LevelWarn, warning) {
		t.Fatalf("Connect did not log %q at warn level; the misconfiguration would stay silent", warning)
	}
}

// recordingHandler is a slog.Handler that keeps every record it is given,
// so a test can assert on an exact message and level instead of matching
// rendered text.
//
// It is safe for concurrent use, because the driver logs connection events
// from its own goroutines while the test reads.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled reports true for every level, so no record is filtered out
// before the test can see it.
func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle keeps a copy of r.
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

// WithAttrs returns h itself. The attributes are dropped, which is fine
// here: every assertion in this file reads a record's level and message.
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns h itself, for the same reason as WithAttrs.
func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// logged reports whether a record with exactly this level and message was
// handled.
func (h *recordingHandler) logged(level slog.Level, message string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level && r.Message == message {
			return true
		}
	}
	return false
}
