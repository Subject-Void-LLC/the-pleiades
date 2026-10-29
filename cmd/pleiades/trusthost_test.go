// Tests for pleiades trust-host through the command itself: first-connect
// against the in-process SSH server into a known_hosts file the run's
// own connections then verify against, and each refusal.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"golang.org/x/crypto/ssh"
)

// trustProject is a project holding a Linux host at the in-process SSH
// server's address, a Windows host without VirtualBox, and a known_hosts
// file the process's connections verify against.
func trustProject(t *testing.T) (dir, knownHosts string, srv *remoteexectest.Server) {
	t.Helper()
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	dir = t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"lab", "--type", "linux_server", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port), "--dir", dir},
		{"win", "--type", "windows_server", "--set", "host=192.0.2.1", "--dir", dir},
	} {
		captureStdout(t, func() {
			if err := runAddHost(args); err != nil {
				t.Fatal(err)
			}
		})
	}
	knownHosts = filepath.Join(t.TempDir(), "known_hosts")
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	return dir, knownHosts, srv
}

func TestTrustHost_FirstConnect(t *testing.T) {
	dir, knownHosts, srv := trustProject(t)
	var err error
	out := captureStdout(t, func() { err = runTrustHost([]string{"lab", "--first-connect", "--dir", dir}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 key(s) added") {
		t.Errorf("output:\n%s", out)
	}
	// A real connection now verifies against what was written.
	auth := remoteexec.PasswordAuth(srv.Username, srv.Password)
	result, err := remoteexec.New(remoteexec.Options{}).Run(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth, "echo verified")
	if err != nil || result.Stdout != "verified\n" {
		t.Fatalf("a connection against the trusted key: %+v, %v", result, err)
	}
	out = captureStdout(t, func() { err = runTrustHost([]string{"lab", "--first-connect", "--dir", dir}) })
	if err != nil || !strings.Contains(out, "0 key(s) added, 1 already trusted") {
		t.Errorf("trusting it again: %v\n%s", err, out)
	}
	if info, _ := os.Stat(knownHosts); info.Mode().Perm() != 0o600 {
		t.Errorf("known_hosts mode %v", info.Mode().Perm())
	}
}

func TestTrustHost_Refusals(t *testing.T) {
	dir, _, _ := trustProject(t)
	for why, tt := range map[string]struct {
		args []string
		want string
	}{
		"no source":                 {[]string{"lab"}, "name one source"},
		"both sources":              {[]string{"lab", "--first-connect", "--from-console", "win"}, "name one source"},
		"--vm alone":                {[]string{"lab", "--first-connect", "--vm", "x"}, "--vm only applies"},
		"no device":                 {[]string{"--first-connect"}, "usage"},
		"a zero timeout":            {[]string{"lab", "--first-connect", "--timeout", "0s"}, "positive"},
		"a device not there":        {[]string{"nothing", "--first-connect"}, "nothing"},
		"a host without VirtualBox": {[]string{"lab", "--from-console", "win"}, "virtualbox: true"},
		"a console host not there":  {[]string{"lab", "--from-console", "nowhere"}, "nowhere"},
	} {
		err := runTrustHost(append(tt.args, "--dir", dir))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
}

// TestTrustHost_ConsoleAnswer: virt.vbox.vm.host_keys answers a VM the host
// does not have with exists false, which trust-host names as a missing VM
// rather than as one that printed no keys; a VM that exists and printed
// none, and a key that does not parse, keep their own refusals.
func TestTrustHost_ConsoleAnswer(t *testing.T) {
	pub, _, genErr := ed25519.GenerateKey(rand.Reader)
	if genErr != nil {
		t.Fatal(genErr)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	for why, tt := range map[string]struct {
		facts map[string]any
		want  string
	}{
		"a VM that is not there": {map[string]any{"exists": false}, `has no VM named "web1"`},
		"a VM with no keys":      {map[string]any{"exists": true, "ssh_host_keys": []string{}}, "found no host keys for web1"},
		"a key that is not one":  {map[string]any{"exists": true, "ssh_host_keys": []string{"not a key"}}, "does not parse"},
	} {
		if _, err := consoleAnswer(tt.facts, "vengeance", "web1"); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
	keys, err := consoleAnswer(map[string]any{"exists": true, "ssh_host_keys": []string{key}}, "vengeance", "web1")
	if err != nil || len(keys) != 1 {
		t.Errorf("one key: %v, %v", keys, err)
	}
}
