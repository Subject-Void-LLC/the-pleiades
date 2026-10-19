// file.copy's src through the real binary: a project file reaches a real
// SSH device, and nothing outside the project, or inside its .pleiades,
// can be sent.
package main_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TestCLI_FileCopySendsAProjectFile: pleiades adhoc ... file.copy with src
// writes the project file's bytes, binary ones included, to the device; a
// second run changes nothing; a check of a changed file predicts the
// write and makes none; and src naming the project's credential file, a
// path outside the project, or a symbolic link out of it is refused
// before anything is sent.
func TestCLI_FileCopySendsAProjectFile(t *testing.T) {
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "generic_ssh", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
		{"add-credential", "box", "--username", srv.Username, "--password", srv.Password},
		{"onboard", "box"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	const bytes = "\x00\x01tree\xff\nend\n"
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "tree.tar.gz"), []byte(bytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tree.tar.gz")

	adhoc := func(extra ...string) jsonReport {
		t.Helper()
		args := append([]string{"adhoc", "box", "file.copy", "dest=" + dest}, extra...)
		out, _ := runPleiadesWithEnv(t, dir, env, append(args, "--json")...)
		var r jsonReport
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("pleiades %v printed %q: %v", args, out, err)
		}
		return r
	}
	if r := adhoc("src=files/tree.tar.gz"); r.Outcome.ExitCode != 0 || len(r.Tasks) != 1 || r.Tasks[0].Status != "changed" {
		t.Fatalf("the copy: %+v", r)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != bytes { // #nosec G304 -- this test's own directory
		t.Fatalf("the device's file = %q, %v; want the project file's bytes", got, err)
	}
	if r := adhoc("src=files/tree.tar.gz"); r.Outcome.ExitCode != 0 || r.Tasks[0].Status != "ok" {
		t.Errorf("a second run with the same file: %+v", r.Tasks)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "tree.tar.gz"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := adhoc("src=files/tree.tar.gz", "--mode", "check"); r.Outcome.ExitCode != 0 || r.Tasks[0].Status != "would_change" {
		t.Errorf("a check of a changed file: %+v", r.Tasks)
	}
	if got, _ := os.ReadFile(dest); string(got) != bytes { // #nosec G304 -- this test's own directory
		t.Error("the check wrote the file")
	}

	if err := os.Symlink(filepath.Join("..", ".pleiades"), filepath.Join(dir, "files", "secrets")); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		".pleiades/credentials.yaml":     "holds its credentials",
		"../outside":                     "leaves the project directory",
		"/etc/passwd":                    "is not a path relative",
		"files/secrets/credentials.yaml": "symbolic link",
	} {
		r := adhoc("src=" + src)
		if len(r.Tasks) != 1 || !strings.Contains(r.Tasks[0].Error, want) {
			t.Errorf("src=%s: %+v, want it refused saying %q", src, r.Tasks, want)
		}
	}
	if got, _ := os.ReadFile(dest); string(got) != bytes { // #nosec G304 -- this test's own directory
		t.Error("a refused src wrote the file")
	}
}
