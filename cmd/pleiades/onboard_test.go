// Tests for `pleiades onboard` through the real binary, against a real SSH
// login into this machine's own shell (remoteexectest runs each command
// through /bin/sh), with real host key verification.
package main_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestCLI_OnboardGenericSSH adds a generic_ssh host, proves it runs
// nothing while discovered, onboards it, and then runs exec.shell, whose
// ShellExecCapable only the probe can grant it.
func TestCLI_OnboardGenericSSH(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "onboarded")
	for _, args := range [][]string{
		{"init"},
		{"add-host", "edge1", "--type", "generic_ssh", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
		{"add-credential", "edge1", "--username", srv.Username, "--password", srv.Password},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, dir, "runbooks/shell.yaml", "id: shell\nhosts: edge1\ntasks:\n  - name: touch the marker\n    fqcn: exec.shell\n    params:\n      cmd: touch "+marker+"\n")

	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/shell.yaml"); err == nil || !strings.Contains(out, "discovered") {
		t.Fatalf("a discovered device ran a task: %v\n%s", err, out)
	}

	out, err := runPleiadesWithEnv(t, dir, env, "onboard", "edge1", "--json")
	if err != nil {
		t.Fatalf("onboard: %v\n%s", err, out)
	}
	var res struct {
		State        string   `json:"state"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("onboard --json printed %q: %v", out, err)
	}
	if res.State != "active" || !slices.Contains(res.Capabilities, "ShellExecCapable") {
		t.Fatalf("onboarded %+v", res)
	}

	if out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/shell.yaml"); err != nil {
		t.Fatalf("run after onboarding: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("exec.shell did not run: %v", err)
	}
}

// TestCLI_OnboardRefusals: add-host cannot write the discovery, and a
// vendor type is not onboarded.
func TestCLI_OnboardRefusals(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := runPleiades(t, dir, "add-host", "edge1", "--type", "generic_ssh", "--set", "host=10.0.0.1", "--set", "discovered=AptCapable")
	if err == nil || !strings.Contains(out, "discovered") {
		t.Fatalf("add-host wrote the discovery: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "web1", "--type", "linux_server", "--set", "host=10.0.0.2"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "onboard", "web1"); err == nil || !strings.Contains(out, "generic") {
		t.Fatalf("a vendor type was onboarded: %v\n%s", err, out)
	}
}
