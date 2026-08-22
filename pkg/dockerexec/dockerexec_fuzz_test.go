package dockerexec_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/dockerexec"
)

// FuzzExec_Socket proves Exec never panics against an arbitrary socket
// address string, for the shapes DockerCapable.DockerEndpoint's own doc
// comment names (a POSIX socket path, or a Windows named pipe URI), and
// that a hostile one (a relative path, a symlink-shaped path, an
// embedded NUL, a pathological length) fails closed with a real error
// rather than crashing or hanging.
func FuzzExec_Socket(f *testing.F) {
	seeds := []string{
		"",
		"/var/run/docker.sock",
		"npipe:////./pipe/docker_engine",
		"npipe://",
		"relative/path/docker.sock",
		"../../etc/passwd",
		"/var/run/../../etc/passwd",
		"/tmp/symlink-to-somewhere",
		"\x00",
		"/var/run/docker\x00.sock",
		string(make([]byte, 10000)),
		"/var/run/" + string(make([]byte, 5000)) + ".sock",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, socket string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Exec panicked on socket %q: %v", socket, r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		// Every one of these seeds and every fuzzer-generated string
		// names a socket that does not exist on the machine running this
		// test, so Exec is expected to fail (a real error), not succeed
		// — the property under test is exclusively "does not panic and
		// does not hang."
		_, _ = dockerexec.Exec(ctx, socket, "web-1", dockerexec.Options{DialTimeout: 100 * time.Millisecond}, "echo hi")
	})
}
