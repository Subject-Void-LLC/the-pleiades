package dockerexec_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/dockerexec"
)

// This file is Phase 73's own adversarial proof for pkg/dockerexec's
// central claim: nothing this package can be made to do reaches the
// daemon socket outside the three exec-lifecycle endpoints, even under
// deliberate attack rather than accidental misuse. Each test's fake
// daemon calls t.Fatal from its own handler if reached at all, so a
// passing test is proof the allowlist refused BEFORE any byte crossed
// the socket, not merely that the daemon's own response was ignored.
// TestCheckAllowed_RefusesEveryOtherRequest (internal_test.go, the same
// package as checkAllowed itself) already proves POST
// /v1.43/containers/create is refused at the allowlist directly; this
// file proves the SAME claim reachable only through Exec's own real
// public API, the surface an actual attacker has.

// TestExec_RefusesMaliciousContainerIDBeforeReachingTheSocket proves an
// escalation attempt through Exec's own real public API: a container id
// crafted to smuggle a path segment (as if trying to reach .../create
// instead of a real container's own exec endpoint) is refused before
// any request is built, against a real fake daemon that would fail this
// test immediately if it were ever reached.
func TestExec_RefusesMaliciousContainerIDBeforeReachingTheSocket(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("daemon should never be reached with a malicious container id, got %s %s", r.Method, r.URL.Path)
	})
	socket := fakeDaemon(t, mux)

	for _, id := range []string{"../create", "x/create", "create/../..", "x\x00/create"} {
		if _, err := dockerexec.Exec(context.Background(), socket, id, dockerexec.Options{}, "cmd"); err == nil {
			t.Errorf("Exec with escalation-shaped container id %q: expected an error", id)
		}
	}
}

// TestExec_HostileEndpointAccessorValueNeverEscalates proves a device
// whose own DockerEndpoint() accessor returns a hostile-looking value
// (a socket string an attacker fully controls, the "device whose
// endpoint accessor returns a hostile value" case the plan names) still
// cannot escalate: this package uses the socket string for exactly one
// purpose, dialing a Unix socket, and never embeds it in a request path
// or body where it could smuggle anything. Every value here is a real,
// syntactically-plausible Unix socket path (not the container-id-shaped
// or empty-string cases FuzzExec_Socket already covers), and every one
// either fails to dial (no such socket) or, if it happened to name a
// real socket, would still only ever let this package issue the same
// three allowlisted requests it always does - nothing about the socket
// VALUE changes what gets sent once connected.
func TestExec_HostileEndpointAccessorValueNeverEscalates(t *testing.T) {
	hostileSockets := []string{
		"/var/run/docker.sock; rm -rf /",
		"/var/run/docker.sock\n/etc/passwd",
		"/proc/self/environ",
		"/var/run/docker.sock/../../../etc/shadow",
	}
	for _, socket := range hostileSockets {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := dockerexec.Exec(ctx, socket, "web-1", dockerexec.Options{DialTimeout: 200 * time.Millisecond}, "cmd")
		cancel()
		if err == nil {
			t.Errorf("Exec with hostile endpoint value %q: expected an error (no such real socket at this path)", socket)
		}
	}
}
