// Holds homeState to a home named "-", which cd reads as $OLDPWD even after
// "--" (FAILURE_PATTERNS 1179). homeState is unexported, so this test is
// internal; it runs on the same real SSH harness and /bin/sh as the rest.
package user

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestHomeState_ComparesAHomeNamedDash compares the relative home "-", as
// empty as the skeleton, while OLDPWD names a decoy holding a file. Read as
// $OLDPWD, the home would be the decoy and read "changed".
func TestHomeState_ComparesAHomeNamedDash(t *testing.T) {
	need.POSIXShell(t)
	root := t.TempDir()
	skel, decoy := filepath.Join(root, "skel"), filepath.Join(root, "decoy")
	for _, dir := range []string{filepath.Join(root, "-"), skel, decoy} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(decoy, "added"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The harness's shell starts in this process's directory, with its
	// environment, so "-" is root's and OLDPWD is the decoy.
	t.Chdir(root)
	t.Setenv("OLDPWD", decoy)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	auth, err := remoteexec.AuthFrom(srv.Username, srv.Password, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	got, err := homeState(context.Background(), conn, "-", skel)
	if err != nil || got != homeUnchanged {
		t.Errorf("homeState(%q, skel) = %q, %v; want %q, since the home is as empty as the skeleton", "-", got, err, homeUnchanged)
	}
}
