// A refused credential against a real SSH server on loopback: presented
// once per call, and never counted against the shared circuit.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// rightPassword is the one password startCountingSSHListener accepts.
const rightPassword = "right"

// startCountingSSHListener starts a real SSH server on loopback that
// accepts rightPassword, refuses every other password, and counts every
// password it is sent. The count is taken where a real server's lockout
// policy (pam_faillock, a network device's login block) would take it:
// on the server, per password presented.
func startCountingSSHListener(t *testing.T) (target Target, presented *int32) {
	t.Helper()
	presented = new(int32)
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			atomic.AddInt32(presented, 1)
			if string(password) == rightPassword {
				return nil, nil
			}
			return nil, errors.New("password refused")
		},
	}
	config.AddHostKey(generateTestHostKey(t))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveOneFakeConnection(conn, config, func(string) (string, string, int) { return "", "", 0 })
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return Target{Host: addr.IP.String(), Port: addr.Port}, presented
}

// TestConnect_ARejectedPasswordIsPresentedOnce proves a credential the
// server refuses is never tried again inside one call.
//
// The dial retry is for a network that failed; a refused password is the
// server answering, and the answer will not change. Retrying it used to
// present the same wrong password three times per task, which on a host
// that locks an account after three failures (pam_faillock's deny=3, a
// network device's login block) locked the account from a single task.
func TestConnect_ARejectedPasswordIsPresentedOnce(t *testing.T) {
	target, presented := startCountingSSHListener(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})

	_, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", "wrong"))
	if err == nil {
		t.Fatal("expected the wrong password to be refused")
	}
	if !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("error = %v, want the server's refusal", err)
	}
	if got := atomic.LoadInt32(presented); got != 1 {
		t.Fatalf("the server was sent the wrong password %d times by one call, want 1", got)
	}
}

// TestConnect_ARejectedPasswordDoesNotOpenTheCircuit proves a refused
// credential never costs another caller its turn.
//
// A Runner shares one breaker across every task it runs, and a circuit
// is per address, not per credential. When a refusal counted as a dial
// failure, two tasks with a wrong password opened the circuit, and a
// third task with the right one, for a different job, was refused with
// "circuit open" without a dial.
func TestConnect_ARejectedPasswordDoesNotOpenTheCircuit(t *testing.T) {
	target, presented := startCountingSSHListener(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})

	for i := 0; i < 2*breaker.DefaultThreshold; i++ {
		_, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", "wrong"))
		if errors.Is(err, breaker.ErrOpen) || (err != nil && strings.Contains(err.Error(), "circuit open")) {
			t.Fatalf("wrong password %d: the circuit opened: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(presented); got != int32(2*breaker.DefaultThreshold) {
		t.Fatalf("the server was sent %d passwords for %d calls, want one each", got, 2*breaker.DefaultThreshold)
	}

	conn, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	if err != nil {
		t.Fatalf("the right password after the wrong ones: %v", err)
	}
	_ = conn.Close()
}
