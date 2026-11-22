// Tests that a successful run, logging in by a stored key whose fingerprint
// is recorded, writes that fingerprint nowhere but the credential file: not
// its output, its stats, its journal or the facts it stored.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestCLI_ASuccessfulKeyLoginWritesNoKeyFingerprint: web01's credential is a
// key with its fingerprint recorded, and the in-process SSH server accepts
// that key and refuses passwords. A command and a fact gather both log in by
// the key and succeed, and then no file under the project but the credential
// file, neither run's JSON report with its stats, and not the stored facts
// read back decrypted, holds the fingerprint.
func TestCLI_ASuccessfulKeyLoginWritesNoKeyFingerprint(t *testing.T) {
	need.POSIXShell(t)
	keyPath, fingerprint := writeKeyFile(t)
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{AuthorizedKey: signer.PublicKey(), RefusePasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web01", "--type", "linux_server", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
		{"add-credential", "web01", "--username", srv.Username, "--key", keyPath},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if listed := listedCredentials(t, dir)["web01"]; listed.KeyFingerprint != fingerprint {
		t.Fatalf("the control: the record is %q, want %s", listed.KeyFingerprint, fingerprint)
	}

	var outputs []string
	for _, args := range [][]string{
		{"adhoc", "web01", "exec.command", "cmd=echo in", "--json"},
		{"adhoc", "web01", "facts.gather", `filter:=["os.*"]`, "--json"},
	} {
		out, err := runPleiadesWithEnv(t, dir, env, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		outputs = append(outputs, out)
	}
	if srv.KeyLogins() < 2 {
		t.Fatalf("the control: %d logins by key, want one for each run", srv.KeyLogins())
	}
	// The facts file is sealed, so its bytes are read back as facts, decrypted.
	stored, err := runPleiadesWithEnv(t, dir, env, "facts", "web01", "--json")
	if err != nil || !strings.Contains(stored, "os.") {
		t.Fatalf("the control: the stored facts read back as %v\n%s", err, stored)
	}
	outputs = append(outputs, stored)

	body := strings.TrimPrefix(fingerprint, "SHA256:")
	for i, out := range outputs {
		if strings.Contains(out, body) {
			t.Errorf("run %d's output holds the key fingerprint:\n%s", i, out)
		}
	}
	scanned := map[string]bool{}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) == "credentials.yaml" {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) >= 2 && parts[0] == ".pleiades" {
			scanned[parts[1]] = true
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), body) {
			t.Errorf("%s holds the key fingerprint", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The control: the scan read the journal and the facts the runs wrote.
	for _, part := range []string{"journal", "facts"} {
		if !scanned[part] {
			t.Errorf("the scan read nothing under .pleiades/%s; read %v", part, scanned)
		}
	}
}
