// Tests that a hop chain which fails partway closes the hops it already
// opened, read from the bastion's side: the connections it still serves.
package remoteexec

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestHops_AFailedLegClosesTheHopsBeforeIt proves that when a leg past the
// first is refused, the connection to the bastion before it is closed, for a
// hop chain dialed by Connect and by DialThroughHops alike, rather than left
// open on the bastion for as long as it keeps idle connections.
func TestHops_AFailedLegClosesTheHopsBeforeIt(t *testing.T) {
	need.POSIXShell(t)
	bastion, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bastion.Close)
	refusing, err := remoteexectest.Start(remoteexectest.Options{RefusePasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(refusing.Close)

	r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastion.Addr(): bastion.HostKey, refusing.Addr(): refusing.HostKey})})
	first := Hop{Target: Target{Host: bastion.Host, Port: bastion.Port}, Auth: PasswordAuth(bastion.Username, bastion.Password)}
	second := Hop{Target: Target{Host: refusing.Host, Port: refusing.Port}, Auth: PasswordAuth(refusing.Username, refusing.Password)}
	ctx := context.Background()

	for name, dial := range map[string]func() error{
		"Connect": func() error {
			_, err := r.Connect(ctx, []Hop{first}, second.Target, second.Auth)
			return err
		},
		"DialThroughHops": func() error {
			_, err := r.DialThroughHops(ctx, []Hop{first, second}, Target{Host: "127.0.0.1", Port: 9})
			return err
		},
	} {
		if err := dial(); err == nil {
			t.Fatalf("%s: a refused leg connected", name)
		}
		deadline := time.Now().Add(5 * time.Second)
		for bastion.Live() != 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if live := bastion.Live(); live != 0 {
			t.Errorf("%s: the bastion still serves %d connection(s) after the chain behind it failed", name, live)
		}
	}
}
