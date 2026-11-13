// The test that a connection with no password reads no password file of
// the machine running pleiades, and says nothing about one.
package pgclient_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
)

// passfileChildEnv carries the server's address to this test re-run as a
// child process, and marks the run as the child.
const passfileChildEnv = "PGCLIENT_TEST_PASSFILE_SERVER"

// passfileChildRefused opens the line the child prints when the server
// refused its login, as it must.
const passfileChildRefused = "passfile child: the login was refused: "

// TestNoAmbientPassfile: with no password, Open reads no password file of
// this machine, neither the one PGPASSFILE names nor ~/.pgpass, though
// each is a working one holding the right password, so the server refuses
// the login; and the driver writes nothing to standard error. It used to
// warn there, on every passwordless connect, that its password file (the
// null device, mode 0666) had group or world access, advice nobody should
// take. The connect runs in a child process so that standard error is its
// own, read here whole.
func TestNoAmbientPassfile(t *testing.T) {
	if addr := os.Getenv(passfileChildEnv); addr != "" {
		passfileChild(t, addr)
		return
	}
	srv := startServer(t, "")
	home, elsewhere := t.TempDir(), t.TempDir()
	line := []byte("*:*:*:*:" + password + "\n")
	for _, file := range []string{filepath.Join(home, ".pgpass"), filepath.Join(elsewhere, "pgpass")} {
		if err := os.WriteFile(file, line, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestNoAmbientPassfile$", "-test.v", "-test.count=1") // #nosec G204 -- this test binary, re-run
	child.Env = append(os.Environ(),
		passfileChildEnv+"="+net.JoinHostPort(srv.host, strconv.Itoa(srv.port)),
		"HOME="+home, "USERPROFILE="+home, "PGPASSFILE="+filepath.Join(elsewhere, "pgpass"))
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("the child: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), passfileChildRefused) {
		t.Fatalf("the child did not reach the server's refusal:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "WARNING") || strings.Contains(stderr.String(), "password file") {
		t.Errorf("the driver wrote to standard error:\n%s", stderr.String())
	}
	// The control: the same planted file is a working one, so reading it
	// would have logged in.
	s := open(t, srv.target(password, nil))
	_ = s.Close()
}

// passfileChild is TestNoAmbientPassfile's child: a connect with no
// password, which must be refused by the server.
func passfileChild(t *testing.T, addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	srv := server{host: host, port: n}
	s, err := pgclient.Open(context.Background(), srv.target("", nil))
	if err == nil {
		_ = s.Close()
		t.Fatal("a login with no password succeeded: a planted password file was read")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("the refusal = %v, want the server refusing the login for want of a password", err)
	}
	fmt.Println(passfileChildRefused + err.Error())
}
