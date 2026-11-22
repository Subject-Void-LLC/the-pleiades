// Tests that a route's bastion warnings are noted once on a task, whichever
// way they are read, and that nothing else is noted.
package sdk_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestNoteRoute_NotesABastionOnceAndNothingElse proves NoteRoute notes the
// bastion of a connection DialThroughHops opened, once however often it is
// noted, and notes nothing for a connection with no hops this package
// dialed, for a nil connection, or under a context with no collector.
func TestNoteRoute_NotesABastionOnceAndNothingElse(t *testing.T) {
	need.POSIXShell(t)
	keyPEM, pub, err := remoteexectest.RSAUserKey()
	if err != nil {
		t.Fatal(err)
	}
	bastion, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: pub, PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoRSA}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bastion.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	known := writeKnownHosts(t, bastion.Addr(), bastion.HostKey)
	hopAuth, err := remoteexec.AuthFrom(bastion.Username, bastion.Password, keyPEM, "")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	conn, err := remoteexec.New(remoteexec.Options{KnownHostsPath: known}).DialThroughHops(context.Background(),
		[]remoteexec.Hop{{Target: remoteexec.Target{Host: bastion.Host, Port: bastion.Port}, Auth: hopAuth}},
		remoteexec.Target{Host: addr.IP.String(), Port: addr.Port})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx := sdk.WithConnectionNotes(context.Background(), "console-sw")
	sdk.NoteRoute(ctx, conn)
	sdk.NoteRoute(ctx, conn)
	warnings := sdk.ConnectionWarnings(ctx)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `device "bastion `+bastion.Addr()+` carrying console-sw" did not use the stored RSA key`) {
		t.Errorf("warnings %q, want the bastion's skipped key once", warnings)
	}

	plain, other := net.Pipe()
	defer plain.Close()
	defer other.Close()
	quiet := sdk.WithConnectionNotes(context.Background(), "console-sw")
	sdk.NoteRoute(quiet, plain)
	sdk.NoteRoute(quiet, nil)
	if w := sdk.ConnectionWarnings(quiet); w != nil {
		t.Errorf("a connection with no hops noted %q", w)
	}
	sdk.NoteRoute(context.Background(), conn) // no collector: nothing to note into
}

// writeKnownHosts writes a known_hosts file trusting key for addr.
func writeKnownHosts(t *testing.T, addr string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(knownhosts.Line([]string{addr}, key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
