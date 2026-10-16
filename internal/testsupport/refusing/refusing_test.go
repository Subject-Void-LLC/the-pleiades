// Tests for Addr and Port: refused at once, held for the test, and never
// returned where the kernel answers anything but a refusal.
package refusing

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// TestAddr_RefusesAndHoldsThePort: a dial to the address is
// refused, and no listener can take its port while the test runs.
func TestAddr_RefusesAndHoldsThePort(t *testing.T) {
	addr := Addr(t)
	if _, err := net.DialTimeout("tcp", addr, refusalWait); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("dialing %s: %v, want connection refused", addr, err)
	}
	if l, err := net.Listen("tcp", addr); err == nil {
		_ = l.Close()
		t.Fatalf("a listener took %s while the test held it", addr)
	}
}

// TestPort_IsTheAddrsPort: the port alone, dialed on 127.0.0.1, is
// refused the same way.
func TestPort_IsTheAddrsPort(t *testing.T) {
	port := Port(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if _, err := net.DialTimeout("tcp", addr, refusalWait); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("dialing %s: %v, want connection refused", addr, err)
	}
}

// cleanupTB stands in for a testing.TB: it records which verdict Addr
// reached, and keeps the cleanups it is handed, so the socket Addr binds is
// still closed when the fake test ends.
type cleanupTB struct {
	testing.TB
	fatal, skip string
	cleanups    []func()
}

func (c *cleanupTB) Helper() {}
func (c *cleanupTB) Fatalf(format string, args ...any) {
	c.fatal = fmt.Sprintf(format, args...)
}
func (c *cleanupTB) Skipf(format string, args ...any) {
	c.skip = fmt.Sprintf(format, args...)
}

func (c *cleanupTB) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }

// TestAddr_OnlyARefusalReturnsTheAddress drives the probe dial
// through the answers this kernel never gives: a dial that connects fails
// the test, one that times out skips it naming the need (and fails a run
// that requires it), and only a refusal returns the address.
func TestAddr_OnlyARefusalReturnsTheAddress(t *testing.T) {
	if _, release, err := bindUnlistened(); err != nil {
		need.Require(t, Need, false, err.Error())
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
		{name: "silent, in a run that requires a refusal", require: Need, dialErr: silent, wantFatal: "requires " + Need},
		{name: "connected", wantFatal: "accepted a connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(need.Env, tt.require)
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
			addr := Addr(fake)
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
			if tt.wantSkip && !strings.HasPrefix(fake.skip, fmt.Sprintf("needs %s: ", Need)) {
				t.Errorf("skip %q does not name the need", fake.skip)
			}
			if (fake.fatal != "") != (tt.wantFatal != "") || !strings.Contains(fake.fatal, tt.wantFatal) {
				t.Errorf("fatal %q; want one containing %q", fake.fatal, tt.wantFatal)
			}
		})
	}
}
