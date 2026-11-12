// Package ssh_test: tests of net.ssh.ping's check.
package ssh_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestPingCheck_OnlyEchoes runs net.ssh.ping's registered Check against
// the SSH harness, which records every command and runs each through a real
// sh: the check sends exactly one command, a printf of the data, even when
// the data is written to look like a second command, a redirect, an option
// echo would read (-n printed nothing) or an escape dash's echo would turn
// into a line break, and it answers exactly as a real run does, with no
// change and the data back whole (FAILURE_PATTERNS 1174).
func TestPingCheck_OnlyEchoes(t *testing.T) {
	need.POSIXShell(t)
	d, ok := collection.Lookup("net.ssh.ping")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("net.ssh.ping does not declare a check: %+v", d.Manifest)
	}
	for _, data := range []string{"", "hello", "x > /tmp/pwned; touch /tmp/pwned", "-n", "-e", "--", `a\nb`} {
		srv, err := remoteexectest.Start(remoteexectest.Options{})
		if err != nil {
			t.Fatal(err)
		}
		device := &sshStub{host: srv.Host, port: srv.Port}
		params := map[string]any{"insecure_skip_host_key_verify": true}
		if data != "" {
			params["data"] = data
		}
		checkRC := pingContext(srv.Secrets())
		checked, err := d.Check(context.Background(), checkRC, device, params)
		if err != nil {
			t.Fatalf("data %q: check: %v", data, err)
		}
		commands := srv.Commands()
		srv.Close()
		if len(commands) != 1 || !strings.HasPrefix(commands[0], `printf '%s\n' `) {
			t.Errorf("data %q: the check sent %q, want one printf", data, commands)
		}
		want := data
		if want == "" {
			want = "pong"
		}
		if checked.Changed || checkRC.stats["reply"] != want {
			t.Errorf("data %q: check = %+v, reply %v; want no change and the data echoed back whole", data, checked, checkRC.stats["reply"])
		}
	}
}
