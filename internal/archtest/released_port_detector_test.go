// Package archtest: the released-port detector's own tests, on snippets it
// must and must not flag, so a detector that matched nothing could not pass.
package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleasedPorts_Detector: each construction of FAILURE_PATTERNS 123's
// class is flagged, and each safe use of a listener is not.
func TestReleasedPorts_Detector(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"closed, then its address dialed": {`package p
func T() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	dial(addr)
}`, 1},
		"closed, then Addr read": {`package p
func T() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ln.Close()
	dial(ln.Addr().String())
}`, 1},
		"a derived port used after the close": {`package p
func T() {
	l, _ := net.ListenTCP("tcp", nil)
	port := l.Addr().(*net.TCPAddr).Port
	s := strconv.Itoa(port)
	l.Close()
	use(s)
}`, 1},
		"a free-port helper": {`package p
func freePort() int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}`, 1},
		"closed in a cleanup, address used while open": {`package p
func T(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = ln.Close() })
	dial(ln.Addr().String())
}`, 0},
		"deferred close, address used while open": {`package p
func T() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	addr := ln.Addr().String()
	dial(addr)
}`, 0},
		"address used before the close": {`package p
func T() {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dial(ln.Addr().String())
	ln.Close()
}`, 0},
		"closed on an error path, named in its error": {`package p
func Start() (*S, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	a, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		return nil, fmt.Errorf("address %v is not TCP", l.Addr())
	}
	return &S{port: a.Port}, nil
}`, 0},
		"closed on an error path, address returned there": {`package p
func Pick() string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	a := l.Addr().String()
	if cond {
		_ = l.Close()
		return a
	}
	return ""
}`, 1},
		"a connection dialed to the address is not the address": {`package p
func pipe() (net.Conn, net.Conn) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	c, _ := net.Dial("tcp", l.Addr().String())
	s, _ := l.Accept()
	return c, s
}`, 0},
		"a Unix socket": {`package p
func T() {
	ln, _ := net.Listen("unix", "/tmp/s")
	p := ln.Addr().String()
	ln.Close()
	use(p)
}`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := releasedPorts("p.go", []byte(tc.src)); len(got) != tc.want {
				t.Errorf("%d findings, want %d: %v", len(got), tc.want, got)
			}
		})
	}
}

// TestReleasedPorts_CatchesTheGateTest: the detector flags the gate test
// as it was before FAILURE_PATTERNS 478's fix, kept in testdata so a
// shallow clone checks it too.
func TestReleasedPorts_CatchesTheGateTest(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "released_port", "gate_tcp_precondition.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := releasedPorts("internal/catalog/gate/run_test.go", src)
	if len(got) != 1 || !strings.Contains(got[0].message, "TestRun_ATCPPreconditionIsTestedFromTheDevice") {
		t.Errorf("findings %v, want the TCP precondition test", got)
	}
}
