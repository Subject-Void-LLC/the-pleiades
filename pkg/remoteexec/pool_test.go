// Tests for Pool's reuse, keying and lifetime, against remoteexectest's
// real in-process SSH server, whose own login count is the evidence: a
// pool that only claimed to reuse a connection would still show one
// login per task there.
package remoteexec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// startPoolServer starts a real in-process SSH server for one test.
func startPoolServer(t *testing.T) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the in-process SSH server: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// target is srv's address as a Target.
func target(srv *remoteexectest.Server) Target {
	return Target{Host: srv.Host, Port: srv.Port}
}

// task is one task's use of the pool: borrow, run one command, give
// back.
func task(t *testing.T, p *Pool, r *Runner, device string, srv *remoteexectest.Server) {
	t.Helper()
	conn, err := p.Connect(context.Background(), r, device, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect(%s) error = %v", device, err)
	}
	res, err := conn.Run(context.Background(), "echo ok")
	if err != nil || res.Stdout != "ok\n" {
		t.Fatalf("Run on %s = %q, %v; want \"ok\\n\", nil", device, res.Stdout, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close on %s error = %v", device, err)
	}
}

// waitFor polls cond until it holds or five seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPool_TenTasksOneLogin is the reason Pool exists. The same ten tasks
// without it log in ten times, measured on the same server first, so the
// counter is shown to count.
func TestPool_TenTasksOneLogin(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	for range 10 {
		res, err := r.Run(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password), "true")
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("Run without a pool = %v, %v", res, err)
		}
	}
	if got := srv.Logins(); got != 10 {
		t.Fatalf("without a pool: %d logins, want 10", got)
	}

	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	for range 10 {
		task(t, p, r, "web1", srv)
	}
	if got := srv.Logins() - 10; got != 1 {
		t.Fatalf("with a pool: %d logins, want 1", got)
	}
}

// TestPool_KeyedByEverythingALoginMeans proves a connection is lent only
// for the device, credential and host key policy it was made with.
func TestPool_KeyedByEverythingALoginMeans(t *testing.T) {
	srv := startPoolServer(t)
	insecure := New(Options{InsecureSkipHostKeyVerify: true})
	verified := New(Options{KnownHostsPath: writeKnownHosts(t, srv.Addr(), srv.HostKey)})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	task(t, p, insecure, "web1", srv)
	task(t, p, insecure, "web2", srv)
	if got := srv.Logins(); got != 2 {
		t.Fatalf("two devices: %d logins, want 2 (one each)", got)
	}

	// The same device under verification must not inherit a login made
	// without it: the old connection is closed and a verified one made.
	task(t, p, verified, "web1", srv)
	if got := srv.Logins(); got != 3 {
		t.Fatalf("host key mode changed: %d logins, want 3", got)
	}
	waitFor(t, "the unverified connection to close", func() bool { return srv.Live() == 2 })

	// A different credential for the device closes the pooled login, and
	// its own dial then fails on its own merits.
	_, err := p.Connect(context.Background(), verified, "web1", target(srv), PasswordAuth(srv.Username, "wrong"))
	if err == nil {
		t.Fatal("Connect with the wrong password succeeded; it reused the pooled login")
	}
	waitFor(t, "the old login to close", func() bool { return srv.Live() == 1 })
	task(t, p, verified, "web1", srv)
	if got := srv.Logins(); got != 4 {
		t.Fatalf("after the credential changed back: %d logins, want 4", got)
	}
}

// TestPool_AuthWithoutIdentityIsNotPooled covers an Auth built other than
// through this package's constructors, which cannot be told apart from
// another and so is never shared.
func TestPool_AuthWithoutIdentityIsNotPooled(t *testing.T) {
	p := NewPool(0)
	if _, ok := p.keyFor(New(Options{InsecureSkipHostKeyVerify: true}), Target{Host: "h", Port: 22}, Auth{user: "u"}); ok {
		t.Fatal("an Auth with no identity was given a pool key")
	}
	if got := identityOf("password", []byte("ab"), []byte("c")); got == identityOf("password", []byte("a"), []byte("bc")) {
		t.Fatal("identityOf does not separate its parts")
	}
	if PasswordAuth("u", "p").identity == ([sha256.Size]byte{}) {
		t.Fatal("PasswordAuth has no identity")
	}
}

// TestPool_TaintedConnectionIsClosed proves a connection whose state is
// uncertain after a task is closed at that task's Close rather than lent
// to the next: one that started a streamed process, and one whose command
// was cut off by its context.
func TestPool_TaintedConnectionIsClosed(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	auth := PasswordAuth(srv.Username, srv.Password)

	conn, err := p.Connect(context.Background(), r, "web1", target(srv), auth)
	if err != nil {
		t.Fatal(err)
	}
	proc, err := conn.Start(context.Background(), "true")
	if err != nil {
		t.Fatal(err)
	}
	_ = proc.CloseWrite()
	_, _ = proc.Wait()
	_ = conn.Close()
	waitFor(t, "the process's connection to close", func() bool { return srv.Live() == 0 })

	conn, err = p.Connect(context.Background(), r, "web1", target(srv), auth)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := conn.Run(ctx, "sleep 2"); err == nil {
		t.Fatal("a command cut off by its context reported success")
	}
	_ = conn.Close()
	waitFor(t, "the cut-off connection to close", func() bool { return srv.Live() == 0 })

	task(t, p, r, "web1", srv)
	if got := srv.Logins(); got != 3 {
		t.Fatalf("%d logins, want 3: each tainted connection replaced", got)
	}
}

// TestPool_UseAfterCloseIsRefused proves a borrowed Conn cannot be used
// after its Close, when its connection may already be lent to another
// task, and that a second Close is an error as it is for a real one.
func TestPool_UseAfterCloseIsRefused(t *testing.T) {
	srv := startPoolServer(t)
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	conn, err := p.Connect(context.Background(), New(Options{InsecureSkipHostKeyVerify: true}), "web1", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Run(context.Background(), "true"); !errors.Is(err, errReleased) {
		t.Errorf("Run after Close error = %v, want errReleased", err)
	}
	if _, err := conn.Shell(context.Background(), ShellOptions{}); !errors.Is(err, errReleased) {
		t.Errorf("Shell after Close error = %v, want errReleased", err)
	}
	if _, err := conn.Start(context.Background(), "true"); !errors.Is(err, errReleased) {
		t.Errorf("Start after Close error = %v, want errReleased", err)
	}
	if err := conn.Close(); !errors.Is(err, errReleased) {
		t.Errorf("second Close error = %v, want errReleased", err)
	}
}

// TestPool_IdleConnectionCloses proves an unused connection is closed
// after the idle period, and the next task logs in again.
func TestPool_IdleConnectionCloses(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(50 * time.Millisecond)
	t.Cleanup(func() { _ = p.Close() })
	task(t, p, r, "web1", srv)
	waitFor(t, "the idle connection to close", func() bool { return srv.Live() == 0 })
	task(t, p, r, "web1", srv)
	if got := srv.Logins(); got != 2 {
		t.Fatalf("%d logins, want 2", got)
	}
	if NewPool(0).idle != DefaultPoolIdle {
		t.Fatal("NewPool(0) does not use DefaultPoolIdle")
	}
}

// TestPool_DiscardAndClose proves Discard and Close end an idle
// connection at once and a lent one at its borrower's Close, and that a
// Connect after Close still works, unpooled.
func TestPool_DiscardAndClose(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	auth := PasswordAuth(srv.Username, srv.Password)
	p := NewPool(0)

	task(t, p, r, "idle", srv)
	lent, err := p.Connect(context.Background(), r, "lent", target(srv), auth)
	if err != nil {
		t.Fatal(err)
	}
	p.Discard("idle")
	p.Discard("lent")
	waitFor(t, "the discarded idle connection to close", func() bool { return srv.Live() == 1 })
	_ = lent.Close()
	waitFor(t, "the discarded lent connection to close", func() bool { return srv.Live() == 0 })

	task(t, p, r, "idle", srv)
	lent, err = p.Connect(context.Background(), r, "lent", target(srv), auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Close to end the idle connection", func() bool { return srv.Live() == 1 })
	_ = lent.Close()
	waitFor(t, "the lent connection to close at its Close", func() bool { return srv.Live() == 0 })

	before := srv.Logins()
	task(t, p, r, "idle", srv)
	task(t, p, r, "idle", srv)
	if got := srv.Logins() - before; got != 2 {
		t.Fatalf("after Close: %d logins for two tasks, want 2 (unpooled)", got)
	}
	waitFor(t, "unpooled connections to close", func() bool { return srv.Live() == 0 })
}

// TestPool_ConcurrentTasks runs many tasks at once across a few devices,
// under -race, and proves every connection ends once the pool is closed
// and no goroutine outlives it.
func TestPool_ConcurrentTasks(t *testing.T) {
	srv := startPoolServer(t)
	leakOpts := goleak.IgnoreCurrent()
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for w := range 8 {
		wg.Go(func() {
			device := fmt.Sprintf("dev%d", w%3)
			for range 20 {
				conn, err := p.Connect(context.Background(), r, device, target(srv), PasswordAuth(srv.Username, srv.Password))
				if err != nil {
					errs <- err
					return
				}
				_, err = conn.Run(context.Background(), "true")
				_ = conn.Close()
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := srv.Logins(); got >= 160 {
		t.Errorf("%d logins for 160 tasks: nothing was reused", got)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "every connection to close", func() bool { return srv.Live() == 0 })
	goleak.VerifyNone(t, leakOpts)
}
