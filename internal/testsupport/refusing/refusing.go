// Package refusing: a loopback address that refuses every connection,
// held for the test so nothing else can start answering on it.
//
// It lives apart from internal/testsupport, which imports testcontainers-go,
// because a test of a package pkg/external reaches uses it, and an external
// Collection author's `go mod tidy` loads those tests: through testsupport,
// testcontainers-go's whole module graph landed in every such author's
// go.sum. This package imports only the standard library and need.
package refusing

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// Need is the need.Require name Addr skips under where the kernel cannot
// give one. CI's Linux jobs require it, since Linux always can, so a runner
// that stops refusing turns the job red.
const Need = "refusing-port"

// refusalWait bounds the one dial Addr makes to learn whether the
// kernel refuses. A refusal on loopback is answered inside the connect
// call, so this is reached only by a kernel that stays silent.
const refusalWait = 2 * time.Second

// probeDial is the dial Addr learns the kernel's answer through; a
// test replaces it to reach the answers this machine's kernel never gives.
var probeDial = net.DialTimeout

// Addr returns a loopback TCP address, "127.0.0.1:<port>", that
// refuses every connection until t ends: the port is bound by a socket
// that never listens, closed in t's cleanup.
//
// A port read from a listener and then given back is a different thing.
// The kernel hands a given-back port out again, so under a parallel run
// another test's server can be listening there by the time this test
// dials, and the test then passes or fails for a reason it does not name.
// A bound port is the test's alone: the kernel gives it to no other socket.
//
// Linux answers a connection to a bound, unlistened port with a reset, so
// the dial is refused at once. Not every kernel does: XNU's TCP drops a
// segment for a socket that is not listening without answering, and there
// a dial would wait out its timeout instead (seen on the macOS CI runner:
// "i/o timeout"). Addr therefore dials the address once and returns it
// only when that dial is refused, skipping t through need.Require(Need)
// when the kernel stays silent, so no test reads a timeout as a refusal.
func Addr(t testing.TB) string {
	t.Helper()
	port, release, err := bindUnlistened()
	if errors.Is(err, errNoUnlistenedBind) {
		need.Require(t, Need, false, err.Error())
		return ""
	}
	if err != nil {
		t.Fatalf("binding a loopback port to refuse on: %v", err)
		return ""
	}
	t.Cleanup(func() { _ = release() })
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	conn, err := probeDial("tcp", addr, refusalWait)
	switch {
	case err == nil:
		_ = conn.Close()
		t.Fatalf("%s accepted a connection with nothing listening on it", addr)
		return ""
	case !errors.Is(err, syscall.ECONNREFUSED):
		need.Require(t, Need, false, fmt.Sprintf(
			"this kernel did not refuse a connection to a bound, unlistened port (%v), so a test would wait out its timeout rather than be refused", err))
		return ""
	}
	return addr
}

// Port is the port of an Addr, for a test that gives a device a port on
// 127.0.0.1 rather than a whole address.
func Port(t testing.TB) int {
	t.Helper()
	addr := Addr(t)
	if addr == "" {
		return 0 // t was stopped; only a recording TB carries on to here
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting %s: %v", addr, err)
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("reading the port of %s: %v", addr, err)
		return 0
	}
	return n
}
