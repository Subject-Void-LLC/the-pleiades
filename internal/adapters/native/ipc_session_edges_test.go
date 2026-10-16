// Tests for the session's failure edges, driven by stand-in child
// programs whose misbehavior is deterministic: one that has already
// exited when a request is sent, one that never answers and ignores
// SIGTERM, and one that echoes the passwords its payload carries.
package native

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// standInChild writes body as an executable shell script and returns its
// path, to stand in for the session child.
func standInChild(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program, run once
		t.Fatal(err)
	}
	return path
}

// TestSession_SendToAnExitedChildFails: a child that exited before its
// request was sent fails the call at the send, naming it, and is ended.
func TestSession_SendToAnExitedChildFails(t *testing.T) {
	device := sessionDevice(nil)
	s := (&ipcCollectionExecutor{exePath: standInChild(t, "exit 0")}).newSession(device.Payload())
	t.Cleanup(s.Close)

	s.mu.Lock()
	child, err := s.start()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	<-child.exited

	desc, _ := collection.Lookup(sessionPIDMethod)
	_, _, err = s.invoke(context.Background(), desc, device, nil, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "failed to send to the session child") {
		t.Fatalf("a send to an exited child returned %v", err)
	}
	if s.child != nil {
		t.Error("the exited child was kept")
	}
}

// TestSession_ChildIgnoringTermIsKilled: a cancelled call to a child that
// never answers and ignores SIGTERM still returns, because the child is
// killed after childWaitDelay, and what it wrote is logged, not lost.
func TestSession_ChildIgnoringTermIsKilled(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	device := sessionDevice(nil)
	script := standInChild(t, "echo stand-in-out\necho stand-in-err >&2\ntrap '' TERM\nexec sleep 60")
	s := (&ipcCollectionExecutor{exePath: script, logger: logger}).newSession(device.Payload())
	t.Cleanup(s.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	desc, _ := collection.Lookup(sessionPIDMethod)
	start := time.Now()
	if _, _, err := s.invoke(ctx, desc, device, nil, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("a cancelled call returned %v", err)
	}
	if took := time.Since(start); took < childWaitDelay || took > childWaitDelay+10*time.Second {
		t.Errorf("the call took %v, want the kill after %v", took, childWaitDelay)
	}
	if out := logged.String(); !strings.Contains(out, "stand-in-out") || !strings.Contains(out, "stand-in-err") {
		t.Errorf("the child's output was not logged: %q", out)
	}
}

// TestSession_MasksEverySecretThePayloadCarries: what a session child
// writes is logged masked with every secret its payload hands it, as a
// one-shot child's is: a database connection's password and a registry
// account's, not only the machine credential's.
func TestSession_MasksEverySecretThePayloadCarries(t *testing.T) {
	const dbPassword, regPassword = "dbsentinel-session-4Kp9", "regsentinel-session-7Hq2"
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	payload := sessionDevice(nil).Payload()
	payload.Databases = []wire.DatabaseConnection{{Name: "appdb", Host: "db", User: "app", Password: dbPassword}}
	payload.Registry = &wire.RegistryCredential{Host: "ghcr.io", Username: "deployer", Password: regPassword}
	// The stand-in echoes both passwords, as a method's error or a
	// driver's debug line could, then exits when its input closes.
	script := standInChild(t, "echo db="+dbPassword+"\necho reg="+regPassword+" >&2\ncat >/dev/null")
	s := (&ipcCollectionExecutor{exePath: script, logger: logger}).newSession(payload)

	s.mu.Lock()
	_, err := s.start()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	out := logged.String()
	if !strings.Contains(out, "db=") || !strings.Contains(out, "reg=") {
		t.Fatalf("the child's output was not logged: %q", out)
	}
	for _, secret := range []string{dbPassword, regPassword} {
		if strings.Contains(out, secret) {
			t.Errorf("the session logged %q unmasked: %q", secret, out)
		}
	}
}
