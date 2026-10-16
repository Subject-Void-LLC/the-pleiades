// Tests for RefusingAddr: refused at once, held for the test, and never
// returned where the kernel answers anything but a refusal.
package testsupport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRefusingAddr_RefusesAndHoldsThePort: a dial to the address is
// refused, and no listener can take its port while the test runs.
func TestRefusingAddr_RefusesAndHoldsThePort(t *testing.T) {
	addr := RefusingAddr(t)
	if _, err := net.DialTimeout("tcp", addr, refusalWait); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("dialing %s: %v, want connection refused", addr, err)
	}
	if l, err := net.Listen("tcp", addr); err == nil {
		_ = l.Close()
		t.Fatalf("a listener took %s while the test held it", addr)
	}
}

// cleanupTB is a recordingTB that also keeps the cleanups it is handed, so
// the socket RefusingAddr binds is still closed when the fake test ends.
type cleanupTB struct {
	recordingTB
	cleanups []func()
}

func (c *cleanupTB) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }

// TestRefusingAddr_OnlyARefusalReturnsTheAddress drives the probe dial
// through the answers this kernel never gives: a dial that connects fails
// the test, one that times out skips it naming the need (and fails a run
// that requires it), and only a refusal returns the address.
func TestRefusingAddr_OnlyARefusalReturnsTheAddress(t *testing.T) {
	if _, release, err := bindUnlistened(); err != nil {
		Require(t, NeedRefusingPort, false, err.Error())
	} else {
		_ = release()
	}
	silent := &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	tests := []struct {
		name      string
		require   string
		dialErr   error
		wantAddr  bool
		wantSkip  bool
		wantFatal string
	}{
		{name: "refused", dialErr: refused, wantAddr: true},
		{name: "silent", dialErr: silent, wantSkip: true},
		{name: "silent, in a run that requires a refusal", require: NeedRefusingPort, dialErr: silent, wantFatal: "requires " + NeedRefusingPort},
		{name: "connected", wantFatal: "accepted a connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(RequireEnv, tt.require)
			was := probeDial
			t.Cleanup(func() { probeDial = was })
			probeDial = func(string, string, time.Duration) (net.Conn, error) {
				if tt.dialErr != nil {
					return nil, tt.dialErr
				}
				client, server := net.Pipe()
				_ = server.Close()
				return client, nil
			}

			fake := &cleanupTB{}
			addr := RefusingAddr(fake)
			for _, f := range fake.cleanups {
				f()
			}
			if len(fake.cleanups) != 1 {
				t.Errorf("%d cleanups registered, want the one that closes the socket", len(fake.cleanups))
			}
			if got := addr != ""; got != tt.wantAddr {
				t.Errorf("returned %q; want an address: %v", addr, tt.wantAddr)
			}
			if got := fake.skip != ""; got != tt.wantSkip {
				t.Errorf("skip %q; want a skip: %v", fake.skip, tt.wantSkip)
			}
			if tt.wantSkip && !strings.HasPrefix(fake.skip, fmt.Sprintf("needs %s: ", NeedRefusingPort)) {
				t.Errorf("skip %q does not name the need", fake.skip)
			}
			if (fake.fatal != "") != (tt.wantFatal != "") || !strings.Contains(fake.fatal, tt.wantFatal) {
				t.Errorf("fatal %q; want one containing %q", fake.fatal, tt.wantFatal)
			}
		})
	}
}
