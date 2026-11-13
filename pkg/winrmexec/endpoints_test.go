// Tests for the endpoint circuit every WinRM call shares: what each kind of
// failure tells it, and that Execute is refused, counted and keyed the way
// the package doc says. The network side runs against real loopback
// sockets: a closed port refuses at once, and an httptest server speaks
// enough NTLM to refuse a password the way a host does.
package winrmexec

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// useCircuits installs a fresh breaker with the given threshold and
// cooldown for one test and puts the process's back when it ends.
func useCircuits(t *testing.T, threshold int, cooldown time.Duration) *breaker.Breaker {
	t.Helper()
	b := breaker.New(threshold, cooldown)
	old := endpoints.swap(b)
	t.Cleanup(func() { endpoints.swap(old) })
	return b
}

// refusingHost is an httptest server speaking the start of NTLM the way a
// WinRM listener does, and refusing every password: a request with no
// Authorization header gets the bare challenge to authenticate, a
// negotiate message gets a real challenge message, and the authenticate
// message that follows, the one carrying proof of the password, is
// refused with 401. While not serving it answers 503 to everything, as
// http.sys does for a URL whose service has not started.
type refusingHost struct {
	server *httptest.Server
	// serving is false until the listener is WinRM's.
	serving atomic.Bool
	// requests counts every request, authorized those carrying any
	// Authorization header, plain those answered with the bare challenge,
	// and withCredential the authenticate messages: each one a logon
	// attempt the host judged.
	requests, authorized, plain, withCredential atomic.Int32
}

// newRefusingHost starts one for the test, serving.
func newRefusingHost(t *testing.T) *refusingHost {
	t.Helper()
	h := &refusingHost{}
	h.serving.Store(true)
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			h.authorized.Add(1)
		}
		if !h.serving.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		header := r.Header.Get("Authorization")
		switch kind := ntlmMessageType(header); {
		case header == "":
			h.plain.Add(1)
			w.Header().Set("WWW-Authenticate", "Negotiate")
		case kind == 1:
			w.Header().Set("WWW-Authenticate", "Negotiate "+base64.StdEncoding.EncodeToString(ntlmChallenge()))
		case kind == 3:
			h.withCredential.Add(1)
			w.Header().Set("WWW-Authenticate", "Negotiate")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(h.server.Close)
	return h
}

// ntlmMessageType reads which NTLM message an Authorization header holds:
// 1 a negotiate, 3 an authenticate, 0 anything else.
func ntlmMessageType(header string) int {
	token, ok := strings.CutPrefix(header, "Negotiate ")
	if !ok {
		return 0
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(raw) < 12 || string(raw[:8]) != "NTLMSSP\x00" {
		return 0
	}
	return int(binary.LittleEndian.Uint32(raw[8:12]))
}

// ntlmChallenge is a minimal NTLM challenge message ([MS-NLMP] 2.2.1.2):
// no target name, a target information list holding only its terminator,
// and the flags a Windows host offers for session security.
func ntlmChallenge() []byte {
	const flags = 0x00000001 | 0x00000010 | 0x00000020 | 0x00000200 | 0x00008000 | // unicode, sign, seal, NTLM, always sign
		0x00080000 | 0x00800000 | 0x02000000 | 0x20000000 | 0x40000000 | 0x80000000 // extended security, target info, version, 128, key exchange, 56
	msg := make([]byte, 60)
	copy(msg, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(msg[8:], 2)
	binary.LittleEndian.PutUint32(msg[16:], 56) // the empty target name's offset
	binary.LittleEndian.PutUint32(msg[20:], flags)
	copy(msg[24:32], "pleiades")               // the server challenge
	binary.LittleEndian.PutUint16(msg[40:], 4) // the target information's length
	binary.LittleEndian.PutUint16(msg[42:], 4)
	binary.LittleEndian.PutUint32(msg[44:], 56)
	msg[48], msg[49], msg[55] = 10, 0, 15 // version 10.0, NTLM revision 15
	return msg                            // bytes 56 to 59 are MsvAvEOL
}

// target is where the server listens.
func (h *refusingHost) target(t *testing.T) Target {
	t.Helper()
	u, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return Target{Host: u.Hostname(), Port: port}
}

// probe is one counted call with a password credential.
func probe(target Target) error {
	_, err := Run(context.Background(), target, Auth{Username: "lab", Password: "pw"}, ShellPowerShell, "'x'", Options{Timeout: 5 * time.Second})
	return err
}

// TestRecord_OnlyTheNetworkOpensACircuit holds the record rule to each kind
// of failure: those before any shell opened that the network caused open a
// circuit, and every other one leaves it closed however often it repeats.
func TestRecord_OnlyTheNetworkOpensACircuit(t *testing.T) {
	for name, tc := range map[string]struct {
		err   error
		opens bool
	}{
		"a refused connection": {&NotStartedError{Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}, true},
		"a network timeout":    {&NotStartedError{Err: &url.Error{Op: "Post", URL: "http://w:5985/wsman", Err: timeoutError{}}}, true},
		"a silent host":        {&NotStartedError{Err: fmt.Errorf("%w: no shell opened", context.DeadlineExceeded)}, true},
		"a refused credential": {&NotStartedError{Err: fmt.Errorf("winrm: %w", errLoginRefused)}, false},
		"a TLS alert":          {&NotStartedError{Err: &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}}, false},
		"a fault":              {&NotStartedError{Err: errors.New("winrm: the service refused the shell")}, false},
		"after the command":    {errors.New("winrm: receiving output: connection reset"), false},
		"the caller canceling": {&NotStartedError{Err: fmt.Errorf("%w: no shell opened", context.Canceled)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			b := useCircuits(t, breaker.DefaultThreshold, time.Hour)
			for i := 0; i < 3*breaker.DefaultThreshold; i++ {
				endpoints.record("w:5985", tc.err)
			}
			if open := !b.Permitted("w:5985"); open != tc.opens {
				t.Fatalf("open = %v, want %v", open, tc.opens)
			}
		})
	}
}

// TestRecord_ACanceledCallClosesNothing is the defect the record rule had
// while it lived in the winrm_exec transport: a call the caller canceled
// before any shell opened is a *NotStartedError that is not unreached, and
// was recorded as the host answering. Holding the half-open probe, it
// closed the circuit on no evidence at all, so the next call went out to a
// host still down. It must leave the probe's lease running instead.
func TestRecord_ACanceledCallClosesNothing(t *testing.T) {
	b := useCircuits(t, 1, time.Hour)
	b.RecordFailure("w:5985")
	b.EndCooldown("w:5985")
	if !b.Allow("w:5985") {
		t.Fatal("expected the probe to be handed out")
	}
	endpoints.record("w:5985", &NotStartedError{Err: fmt.Errorf("%w: no shell opened", context.Canceled)})
	if b.Allow("w:5985") {
		t.Fatal("a canceled call closed the circuit: the next caller was let through with the probe still out")
	}
}

// TestExecute_FiveRefusedConnectionsOpenTheCircuit proves the shipped
// defaults through the real Execute: five refused connections to one
// address and port open its circuit, and the sixth call is refused at
// once, with nothing sent, as unreached and not retryable.
func TestExecute_FiveRefusedConnectionsOpenTheCircuit(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	target := Target{Host: "127.0.0.1", Port: refusing.Port(t)}
	for i := 1; i <= breaker.DefaultThreshold; i++ {
		if err := probe(target); err == nil || errors.Is(err, breaker.ErrOpen) || !Retryable(err) {
			t.Fatalf("call %d: err = %v, want the refused connection, retryable", i, err)
		}
	}
	start := time.Now()
	err := probe(target)
	if !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("call %d: err = %v, want the circuit open", breaker.DefaultThreshold+1, err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("the refusal took %s; it sends nothing", time.Since(start))
	}
	var notStarted *NotStartedError
	if !errors.As(err, &notStarted) || !notStarted.Unreached() || Retryable(err) {
		t.Errorf("the refusal %v must be an unreached NotStartedError that is not retryable", err)
	}
	// The refusal itself is not counted, and neither is an author's mistake.
	if _, err := Run(context.Background(), target, Auth{Username: "lab", Password: "pw"}, ShellPowerShell, "", Options{}); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Errorf("an empty script: err = %v, want the mistake named, before the circuit", err)
	}
}

// TestExecute_ARefusedCredentialNeverOpensTheCircuit proves a host that
// answers, refusing the credential every time, never opens a circuit,
// whatever the count: the call after a wrong password must not be refused
// as if the host were down.
func TestExecute_ARefusedCredentialNeverOpensTheCircuit(t *testing.T) {
	useCircuits(t, breaker.DefaultThreshold, breaker.DefaultCooldown)
	host := newRefusingHost(t)
	target := host.target(t)
	for i := 0; i < 3*breaker.DefaultThreshold; i++ {
		err := probe(target)
		if err == nil || errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("call %d: err = %v, want the host's refusal", i, err)
		}
		if !credentialRefused(err) {
			t.Fatalf("call %d: err = %v is not read as a refused credential", i, err)
		}
	}
}

// TestExecute_ACanceledCallDoesNotTakeTheProbe is FAILURE_PATTERNS 398 for
// the shared circuit: a call that arrives already canceled once the
// cooldown has run out must leave the probe for the next live call.
func TestExecute_ACanceledCallDoesNotTakeTheProbe(t *testing.T) {
	b := useCircuits(t, 1, time.Hour)
	target := Target{Host: "127.0.0.1", Port: refusing.Port(t)}
	if err := probe(target); err == nil {
		t.Fatal("expected the first call to fail")
	}
	b.EndCooldown(Addr(target, Auth{Username: "lab"}, Options{}))

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(canceled, target, Auth{Username: "lab", Password: "pw"}, ShellPowerShell, "'x'", Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call: err = %v, want context.Canceled", err)
	}
	// The probe is still there to take: the live call is sent, and fails.
	if err := probe(target); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("live call: err = %v, want its probe sent and refused by the closed port", err)
	}
}

// TestEndpoints_KeyedByTheAddressDialed proves a bracketed and a bare IPv6
// literal are one circuit, since Addr gives both the address really
// dialed, and another port on the host is its own.
func TestEndpoints_KeyedByTheAddressDialed(t *testing.T) {
	useCircuits(t, 1, time.Hour)
	ctx := context.Background()
	bare := Addr(Target{Host: "2001:db8::1", Port: 5985}, passwordAuth, Options{})
	bracketed := Addr(Target{Host: "[2001:db8::1]", Port: 5985}, passwordAuth, Options{})
	other := Addr(Target{Host: "2001:db8::1", Port: 5986}, passwordAuth, Options{})
	if err := endpoints.admit(ctx, bare); err != nil {
		t.Fatal(err)
	}
	endpoints.record(bare, &NotStartedError{Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}})
	if err := endpoints.admit(ctx, bracketed); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("bracketed literal: err = %v, want the bare literal's circuit, already open", err)
	}
	if err := endpoints.admit(ctx, other); err != nil {
		t.Fatalf("another port: err = %v, want a circuit of its own", err)
	}
}
