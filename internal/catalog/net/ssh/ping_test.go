package ssh_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ssh"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestPing_Registered proves "net.ssh.ping" registered itself through
// this package's own init(), as StatusImplemented with a real Invoke:
// unlike a freshly generated stub, a runbook task naming this fqcn
// reaches Ping directly rather than being refused as declared-but-not-
// implemented.
func TestPing_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ssh.ping")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.ssh.ping")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, want Ping")
	}
	if d.Manifest.ExecutionContext.RequiresElevation != false {
		t.Errorf("Manifest.ExecutionContext.RequiresElevation = %v, want %v",
			d.Manifest.ExecutionContext.RequiresElevation, false)
	}
}

// TestPing_RequiresSSHTransportCapable proves Ping refuses a device that
// does not implement capability.SSHTransportCapable before attempting any
// network I/O, rather than a confusing dial or nil-pointer failure.
func TestPing_RequiresSSHTransportCapable(t *testing.T) {
	device := &inventorytest.Stub{StubName: "plain"}
	_, err := ssh.Ping(context.Background(), nil, device, nil)
	if err == nil {
		t.Fatal("expected an error for a device not implementing SSHTransportCapable")
	}
}

// A real, Docker-backed proof that Ping reaches an actual sshd over an
// actual network connection lives in cmd/runner's own Release Gate test
// suite (Phase 16's own plan), which exercises this method through the
// full dispatch -> Runner -> subprocess boundary chain against a real
// device -- a second, narrower container test here would duplicate that
// coverage rather than add to it.
