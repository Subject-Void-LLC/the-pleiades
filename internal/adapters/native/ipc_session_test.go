// Tests for a dispatch's collection session, through real re-executed
// child processes (TestMain routes this binary into one) and a real SSH
// server whose own login count is the evidence of reuse.
package native

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The session test methods, registered at init so the re-executed child
// has them too.
const (
	sessionPIDMethod     = "nativesessiontest.pid"
	sessionEndsMethod    = "nativesessiontest.ends_login"
	sessionSlowMethod    = "nativesessiontest.slow"
	sessionConnectMethod = "nativesessiontest.connect"
)

// reportPID records the child's process id, which tells a test whether
// two calls ran in one child.
func reportPID(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	return collection.Result{}, rc.SetStat("pid", fmt.Sprint(os.Getpid()))
}

// connectOnce runs one command over sdk.Connect, as an SSH-backed method
// does, and records the child's process id.
func connectOnce(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, sessionConnectMethod)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Run(ctx, "true"); err != nil {
		return collection.Result{}, err
	}
	return reportPID(ctx, rc, device, params)
}

func init() {
	fixture := collection.Reversibility{Notes: "a test fixture that changes nothing"}
	collection.MustRegister(collection.Descriptor{Name: sessionPIDMethod, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixture, SupportsCheck: true}, Invoke: reportPID, Check: reportPID})
	collection.MustRegister(collection.Descriptor{Name: sessionEndsMethod, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixture, SupportsCheck: true, EndsLoginSession: true}, Invoke: reportPID, Check: reportPID})
	collection.MustRegister(collection.Descriptor{Name: sessionSlowMethod, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixture}, Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
		time.Sleep(2 * time.Second)
		return reportPID(ctx, rc, d, p)
	}})
	collection.MustRegister(collection.Descriptor{Name: sessionConnectMethod, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: fixture,
		Doc: collection.Doc{Params: []collection.Param{{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Description: "skip the host key check"}}},
	}, Invoke: connectOnce})
}

// sessionDevice is a device whose dispatch reaches srv, or nowhere when
// srv is nil.
func sessionDevice(srv *remoteexectest.Server) *wireDevice {
	p := wire.DispatchPayload{JobID: "job-1", DeviceID: "d1", DeviceName: "web1", Capabilities: []capability.Name{capability.NameSSHTransport}}
	if srv != nil {
		p.DeviceHost, p.SSHPort = srv.Host, srv.Port
		p.Secrets = map[string]string{wire.SecretUsername: srv.Username, wire.SecretPassword: srv.Password}
	}
	return newWireDevice(p)
}

// callPID invokes name through invoke and returns the child's process id.
func callPID(t *testing.T, invoke func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]interface{}, collection.Mode) (collection.Result, map[string]interface{}, error), name string, mode collection.Mode, device inventory.InventoryItem, params map[string]any) string {
	t.Helper()
	desc, _ := collection.Lookup(name)
	_, facts, err := invoke(context.Background(), desc, device, params, mode)
	if err != nil {
		t.Fatalf("%s (%s): %v", name, mode, err)
	}
	pid, _ := facts["pid"].(string)
	if pid == "" || pid == fmt.Sprint(os.Getpid()) {
		t.Fatalf("%s ran in pid %q, want a child's", name, pid)
	}
	return pid
}

// newTestSession is a session over this test binary, closed at the end.
func newTestSession(t *testing.T, device *wireDevice) *ipcSession {
	t.Helper()
	e, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := e.newSession(device.Payload())
	t.Cleanup(s.Close)
	return s
}

// TestSession_OneChildForTheDispatch proves calls share one child, that a
// real run of a method ending the login session ends the child and the
// next call starts another, that a check of it does not, and that the
// one-shot path starts a child per call.
func TestSession_OneChildForTheDispatch(t *testing.T) {
	device := sessionDevice(nil)
	s := newTestSession(t, device)
	first := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil)
	if again := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil); again != first {
		t.Fatalf("second call ran in %s, first in %s: the session did not keep its child", again, first)
	}
	if checked := callPID(t, s.invoke, sessionEndsMethod, collection.ModeCheck, device, nil); checked != first {
		t.Fatalf("a check of a login-ending method moved to child %s from %s", checked, first)
	}
	if ran := callPID(t, s.invoke, sessionEndsMethod, collection.ModeExecute, device, nil); ran != first {
		t.Fatalf("the login-ending method ran in %s, want the session's child %s", ran, first)
	}
	if next := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil); next == first {
		t.Fatal("the call after a login-ending method reused the ended child")
	}

	oneShot := s.oneShot.invoke
	if a, b := callPID(t, oneShot, sessionPIDMethod, collection.ModeExecute, device, nil), callPID(t, oneShot, sessionPIDMethod, collection.ModeExecute, device, nil); a == b {
		t.Fatal("two one-shot calls ran in one child")
	}
}

// TestSession_CancelEndsTheChild proves a cancelled call ends the child,
// which is the only way to stop a method mid-command, and that the next
// call starts a fresh one.
func TestSession_CancelEndsTheChild(t *testing.T) {
	device := sessionDevice(nil)
	s := newTestSession(t, device)
	first := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	desc, _ := collection.Lookup(sessionSlowMethod)
	start := time.Now()
	if _, _, err := s.invoke(ctx, desc, device, nil, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("a cancelled call returned %v, want the deadline", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("a cancelled call took %v to return", took)
	}
	if next := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil); next == first {
		t.Fatal("the call after a cancel reused the child it cancelled")
	}
}

// TestSession_BusyChildFallsBackToOneShot proves a call arriving while
// another holds the child does not wait for it.
func TestSession_BusyChildFallsBackToOneShot(t *testing.T) {
	device := sessionDevice(nil)
	s := newTestSession(t, device)
	var wg sync.WaitGroup
	var slowFacts map[string]interface{}
	var slowErr error
	slow, _ := collection.Lookup(sessionSlowMethod)
	wg.Go(func() {
		_, slowFacts, slowErr = s.invoke(context.Background(), slow, device, nil, collection.ModeExecute)
	})
	time.Sleep(300 * time.Millisecond) // the slow call has the child by now
	quick := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil)
	wg.Wait()
	if slowErr != nil {
		t.Fatalf("the slow call: %v", slowErr)
	}
	if quick == slowFacts["pid"] {
		t.Fatal("the concurrent call ran in the busy session child")
	}
}

// TestSession_SharesOneLogin is the point of the session: three calls
// that each connect over SSH log in once in a session and three times
// one-shot, counted by the real SSH server.
func TestSession_SharesOneLogin(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := sessionDevice(srv)
	params := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}
	s := newTestSession(t, device)
	for range 3 {
		callPID(t, s.invoke, sessionConnectMethod, collection.ModeExecute, device, params)
	}
	if got := srv.Logins(); got != 1 {
		t.Fatalf("session: %d logins for three calls, want 1", got)
	}
	for range 3 {
		callPID(t, s.oneShot.invoke, sessionConnectMethod, collection.ModeExecute, device, params)
	}
	if got := srv.Logins(); got != 4 {
		t.Fatalf("one-shot: %d logins in all, want 4", got)
	}
	s.Close()
	deadline := time.Now().Add(5 * time.Second)
	for srv.Live() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections still open after the session closed", srv.Live())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAdapter_Execute_PersistConnections runs a three-task runbook
// through the whole adapter, with persistence on and off, and counts the
// server's logins.
func TestAdapter_Execute_PersistConnections(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	var rb strings.Builder
	rb.WriteString("id: pb-1\ntasks:\n")
	for i := range 3 {
		fmt.Fprintf(&rb, "  - name: step %d\n    fqcn: %s\n    params:\n      %s: true\n", i, sessionConnectMethod, sdk.ParamInsecureSkipHostKeyVerify)
	}
	adapter, err := NewAdapter(&mockBus{}, writeRunbook(t, "pb-1", rb.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := sessionDevice(srv).Payload()
	payload.RunbookID = "pb-1"
	for _, tc := range []struct {
		persist bool
		want    int64
	}{{true, 1}, {false, 3}} {
		before := srv.Logins()
		payload.PersistConnections = tc.persist
		if _, err := adapter.Execute(context.Background(), payload); err != nil {
			t.Fatalf("persist %v: %v", tc.persist, err)
		}
		if got := srv.Logins() - before; got != tc.want {
			t.Errorf("persist %v: %d logins for three tasks, want %d", tc.persist, got, tc.want)
		}
	}
}

// sessionExitMethod ends its child process without answering, as a
// method that crashes the child would.
const sessionExitMethod = "nativesessiontest.exits"

func init() {
	collection.MustRegister(collection.Descriptor{Name: sessionExitMethod, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}}, Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		fmt.Fprintln(os.Stderr, "the child is leaving")
		os.Exit(3)
		return collection.Result{}, nil
	}})
}

// TestSession_ChildThatDiesIsReplaced proves a child that exits without
// answering fails that call, naming its stderr, and the next call starts
// a fresh child.
func TestSession_ChildThatDiesIsReplaced(t *testing.T) {
	device := sessionDevice(nil)
	s := newTestSession(t, device)
	first := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil)
	desc, _ := collection.Lookup(sessionExitMethod)
	_, _, err := s.invoke(context.Background(), desc, device, nil, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "the child is leaving") {
		t.Fatalf("a dead child's call returned %v, want an error naming its stderr", err)
	}
	if next := callPID(t, s.invoke, sessionPIDMethod, collection.ModeExecute, device, nil); next == first {
		t.Fatal("the call after a dead child reused it")
	}
}

// TestSession_Refusals covers the calls a session does not run in its
// child: a device other than the one dispatched, an external Collection
// (its own process already, so the one-shot path runs it in this one),
// and a child binary that cannot start.
func TestSession_Refusals(t *testing.T) {
	device := sessionDevice(nil)
	s := newTestSession(t, device)
	desc, _ := collection.Lookup(sessionPIDMethod)
	if _, _, err := s.invoke(context.Background(), desc, &inventorytest.Stub{StubName: "x"}, nil, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "bound to device") {
		t.Errorf("a device other than the dispatched one: %v, want a refusal naming both", err)
	}

	external := desc
	external.Provider = &collection.Provider{Program: "fixture"}
	_, facts, err := s.invoke(context.Background(), external, device, nil, collection.ModeExecute)
	if err != nil || facts["pid"] != fmt.Sprint(os.Getpid()) {
		t.Errorf("an external method ran with facts %v, err %v; want this process's pid", facts, err)
	}

	broken := (&ipcCollectionExecutor{exePath: "/nonexistent/pleiades-runner"}).newSession(device.Payload())
	t.Cleanup(broken.Close)
	if _, _, err := broken.invoke(context.Background(), desc, device, nil, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "failed to start the session child") {
		t.Errorf("a child that cannot start returned %v", err)
	}
}

// TestCappedBuffer keeps what fits, counts the rest, and never makes the
// copy feeding it fail.
func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{max: 4}
	if n, err := b.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if n, err := b.Write([]byte("defg")); n != 4 || err != nil {
		t.Fatalf("Write past the cap = %d, %v; want every byte reported written", n, err)
	}
	if got := b.String(); got != "abcd\n[3 more bytes not kept]" {
		t.Fatalf("String = %q", got)
	}
}
